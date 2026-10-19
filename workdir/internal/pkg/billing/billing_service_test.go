package billing

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v82"

	"app/internal/database"
	"app/internal/db"
	apperrors "app/internal/errors"
	"app/internal/pkg/auth"
	"app/internal/pkg/tenants"
)

// fakeGateway records its calls and returns canned URLs.
type fakeGateway struct {
	customers []string // emails passed to CreateCustomer
	checkouts []string // customer IDs passed to CreateCheckoutSession
	portals   []string // customer IDs passed to CreatePortalSession
	priceID   string
	urls      CheckoutURLs
	returnURL string
	err       error
}

func (g *fakeGateway) CreateCustomer(_ context.Context, email, _ string, _ int32) (string, error) {
	if g.err != nil {
		return "", g.err
	}
	g.customers = append(g.customers, email)
	return "cus_new", nil
}

func (g *fakeGateway) CreateCheckoutSession(_ context.Context, customerID, priceID, successURL, cancelURL string, _ int32) (string, error) {
	if g.err != nil {
		return "", g.err
	}
	g.checkouts = append(g.checkouts, customerID)
	g.priceID, g.urls = priceID, CheckoutURLs{SuccessURL: successURL, CancelURL: cancelURL}
	return "https://checkout.stripe.test/" + customerID, nil
}

func (g *fakeGateway) CreatePortalSession(_ context.Context, customerID, returnURL string) (string, error) {
	if g.err != nil {
		return "", g.err
	}
	g.portals = append(g.portals, customerID)
	g.returnURL = returnURL
	return "https://portal.stripe.test/" + customerID, nil
}

// memTenants is an in-memory tenants.Repository keyed by tenant ID. Like the
// SQL queries, it drops subscription updates older than the last one applied
// to the stored subscription, whose event time it keeps in eventAt.
type memTenants struct {
	tenants.Repository
	byID    map[int32]tenants.Tenant
	eventAt map[int32]time.Time
	updates int
	err     error // returned by UpdateSubscription
	setErr  error // returned by SetStripeCustomer
}

func newMemTenants(ts ...tenants.Tenant) *memTenants {
	m := &memTenants{byID: map[int32]tenants.Tenant{}, eventAt: map[int32]time.Time{}}
	for _, t := range ts {
		if t.Plan == "" {
			t.Plan = tenants.PlanFree
		}
		m.byID[t.ID] = t
	}
	return m
}

func (m *memTenants) GetByID(_ context.Context, id int32) (tenants.Tenant, error) {
	t, ok := m.byID[id]
	if !ok {
		return tenants.Tenant{}, apperrors.NewNotFound("tenant not found")
	}
	return t, nil
}

func (m *memTenants) GetByStripeCustomerID(_ context.Context, customerID string) (tenants.Tenant, error) {
	for _, t := range m.byID {
		if customerID != "" && t.StripeCustomerID == customerID {
			return t, nil
		}
	}
	return tenants.Tenant{}, apperrors.NewNotFound("tenant not found")
}

func (m *memTenants) SetStripeCustomer(_ context.Context, tenantID int32, customerID string) (tenants.Tenant, error) {
	if m.setErr != nil {
		return tenants.Tenant{}, m.setErr
	}
	t, ok := m.byID[tenantID]
	if !ok {
		return tenants.Tenant{}, apperrors.NewNotFound("tenant not found")
	}
	t.StripeCustomerID = customerID
	m.byID[tenantID] = t
	return t, nil
}

func (m *memTenants) UpdateSubscription(ctx context.Context, in tenants.SubscriptionUpdate) (tenants.Tenant, error) {
	if m.err != nil {
		return tenants.Tenant{}, m.err
	}
	t, err := m.GetByStripeCustomerID(ctx, in.CustomerID)
	if err != nil {
		return tenants.Tenant{}, err
	}
	if last, ok := m.eventAt[t.ID]; ok && t.StripeSubscriptionID == in.SubscriptionID && in.EventAt.Before(last) {
		return tenants.Tenant{}, apperrors.NewNotFound("tenant not found")
	}
	m.updates++
	t.StripeSubscriptionID, t.SubscriptionStatus, t.CurrentPeriodEnd, t.Plan =
		in.SubscriptionID, in.Status, in.CurrentPeriodEnd, in.Plan
	m.byID[t.ID] = t
	m.eventAt[t.ID] = in.EventAt
	return t, nil
}

func (m *memTenants) LinkSubscription(ctx context.Context, customerID, subscriptionID string) (tenants.Tenant, error) {
	t, err := m.GetByStripeCustomerID(ctx, customerID)
	if err != nil {
		return tenants.Tenant{}, err
	}
	if t.StripeSubscriptionID != subscriptionID {
		t.SubscriptionStatus, t.CurrentPeriodEnd = "", nil
		delete(m.eventAt, t.ID)
	}
	t.StripeSubscriptionID = subscriptionID
	m.byID[t.ID] = t
	return t, nil
}

// memEvents is an in-memory EventLog.
type memEvents struct {
	seen map[string]string
	err  error
}

func (m *memEvents) MarkProcessed(_ context.Context, id, eventType string) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	if _, ok := m.seen[id]; ok {
		return false, nil
	}
	m.seen[id] = eventType
	return true, nil
}

type testEnv struct {
	svc      *Service
	gateway  *fakeGateway
	tenants  *memTenants
	events   *memEvents
	users    *memUsers
	notifier *fakeNotifier
	tx       *database.FakeTxRunner
}

// memUsers is an in-memory Users keyed by user ID.
type memUsers struct {
	byID map[int32]auth.User
}

func (m *memUsers) GetByID(_ context.Context, id int32) (auth.User, error) {
	u, ok := m.byID[id]
	if !ok {
		return auth.User{}, apperrors.NewNotFound("user not found")
	}
	return u, nil
}

// fakeNotifier records the owners it was asked to notify of a failed
// payment, or returns err when set.
type fakeNotifier struct {
	paymentFailed []auth.User
	err           error
}

func (f *fakeNotifier) SendPaymentFailed(_ context.Context, owner auth.User) error {
	if f.err != nil {
		return f.err
	}
	f.paymentFailed = append(f.paymentFailed, owner)
	return nil
}

func newTestEnv(t *testing.T, ts ...tenants.Tenant) testEnv {
	t.Helper()
	env := testEnv{
		gateway:  &fakeGateway{},
		tenants:  newMemTenants(ts...),
		events:   &memEvents{seen: map[string]string{}},
		users:    &memUsers{byID: map[int32]auth.User{}},
		notifier: &fakeNotifier{},
		tx:       &database.FakeTxRunner{},
	}
	svc, err := NewService(env.gateway, env.tenants, Options{
		PriceID:    "price_pro",
		Tx:         env.tx,
		TenantsFor: func(db.Querier) tenants.Repository { return env.tenants },
		EventsFor:  func(db.Querier) EventLog { return env.events },
		UsersFor:   func(db.Querier) Users { return env.users },
		Notifier:   env.notifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	env.svc = svc
	return env
}

func assertCode(t *testing.T, err error, want apperrors.Code) {
	t.Helper()
	appErr, ok := apperrors.As(err)
	if !ok || appErr.Code != want {
		t.Fatalf("err = %v, want code %s", err, want)
	}
}

// newEvent returns an event of type typ whose object is obj, as JSON.
func newEvent(t *testing.T, id string, typ stripe.EventType, obj map[string]any) stripe.Event {
	t.Helper()
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return stripe.Event{ID: id, Type: typ, Data: &stripe.EventData{Raw: raw}}
}

// subscriptionObject returns a subscription JSON object with one item whose
// period ends at periodEnd.
func subscriptionObject(id, customer string, status stripe.SubscriptionStatus, periodEnd int64) map[string]any {
	return map[string]any{
		"id": id, "object": "subscription", "customer": customer, "status": status,
		"items": map[string]any{"object": "list", "data": []any{
			map[string]any{"id": "si_1", "object": "subscription_item", "current_period_end": periodEnd},
		}},
	}
}

// newEventAt is newEvent created at the Unix time created.
func newEventAt(t *testing.T, id string, typ stripe.EventType, created int64, obj map[string]any) stripe.Event {
	t.Helper()
	ev := newEvent(t, id, typ, obj)
	ev.Created = created
	return ev
}

func invoiceObject(customer, subscription string, nextAttempt int64) map[string]any {
	return map[string]any{
		"id": "in_1", "object": "invoice", "customer": customer, "next_payment_attempt": nextAttempt,
		"parent": map[string]any{"type": "subscription_details", "subscription_details": map[string]any{"subscription": subscription}},
	}
}

var testUser = auth.User{ID: 7, Username: "alice", Email: "alice@example.com"}

func TestNewServiceValidatesOptions(t *testing.T) {
	if _, err := NewService(&fakeGateway{}, newMemTenants(), Options{Tx: &database.FakeTxRunner{}}); err == nil {
		t.Fatal("want error without a price ID")
	}
	if _, err := NewService(&fakeGateway{}, newMemTenants(), Options{PriceID: "price_pro"}); err == nil {
		t.Fatal("want error without a transaction runner")
	}
}

func TestStartCheckoutCreatesAndStoresCustomer(t *testing.T) {
	env := newTestEnv(t, tenants.Tenant{ID: 1, Name: "acme"})
	urls := CheckoutURLs{SuccessURL: "https://app.test/ok", CancelURL: "https://app.test/no"}

	url, err := env.svc.StartCheckout(context.Background(), env.tenants.byID[1], testUser, urls)
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://checkout.stripe.test/cus_new" {
		t.Fatalf("url = %q", url)
	}
	if len(env.gateway.customers) != 1 || env.gateway.customers[0] != testUser.Email {
		t.Fatalf("customers = %v, want one for %s", env.gateway.customers, testUser.Email)
	}
	if got := env.tenants.byID[1].StripeCustomerID; got != "cus_new" {
		t.Fatalf("stored customer = %q, want cus_new", got)
	}
	if env.gateway.priceID != "price_pro" || env.gateway.urls != urls {
		t.Fatalf("checkout price/urls = %q/%+v", env.gateway.priceID, env.gateway.urls)
	}
}

func TestStartCheckoutReusesCustomer(t *testing.T) {
	env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_old"})

	if _, err := env.svc.StartCheckout(context.Background(), env.tenants.byID[1], testUser, CheckoutURLs{}); err != nil {
		t.Fatal(err)
	}
	if len(env.gateway.customers) != 0 {
		t.Fatalf("created customers %v, want none", env.gateway.customers)
	}
	if len(env.gateway.checkouts) != 1 || env.gateway.checkouts[0] != "cus_old" {
		t.Fatalf("checkouts = %v, want [cus_old]", env.gateway.checkouts)
	}
}

func TestStartCheckoutRejectsProTenant(t *testing.T) {
	env := newTestEnv(t, tenants.Tenant{ID: 1, Plan: tenants.PlanPro, StripeCustomerID: "cus_1"})

	_, err := env.svc.StartCheckout(context.Background(), env.tenants.byID[1], testUser, CheckoutURLs{})
	assertCode(t, err, apperrors.CodeConflict)
	if len(env.gateway.checkouts) != 0 {
		t.Fatal("checkout session created for a pro tenant")
	}
}

func TestStartCheckoutSubscriptionState(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   apperrors.Code // "" when the checkout must start
	}{
		{"link pending", "", apperrors.CodeConflict},
		{"past_due", "past_due", apperrors.CodeConflict},
		{"unpaid", "unpaid", apperrors.CodeConflict},
		{"canceled", "canceled", ""},
		{"incomplete_expired", "incomplete_expired", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_1", SubscriptionStatus: tt.status})

			_, err := env.svc.StartCheckout(context.Background(), env.tenants.byID[1], testUser, CheckoutURLs{})
			if tt.want == "" {
				if err != nil || len(env.gateway.checkouts) != 1 {
					t.Fatalf("err = %v, checkouts = %v; want one checkout", err, env.gateway.checkouts)
				}
				return
			}
			assertCode(t, err, tt.want)
			if len(env.gateway.checkouts) != 0 {
				t.Fatalf("checkouts = %v, want none", env.gateway.checkouts)
			}
		})
	}
}

func TestStartCheckoutGatewayError(t *testing.T) {
	env := newTestEnv(t, tenants.Tenant{ID: 1})
	env.gateway.err = apperrors.NewInternal(stderrors.New("stripe down"))

	_, err := env.svc.StartCheckout(context.Background(), env.tenants.byID[1], testUser, CheckoutURLs{})
	assertCode(t, err, apperrors.CodeInternal)
	if env.tenants.byID[1].StripeCustomerID != "" {
		t.Fatal("customer stored after a failed creation")
	}
}

func TestOpenPortal(t *testing.T) {
	env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1"}, tenants.Tenant{ID: 2})
	ctx := context.Background()

	url, err := env.svc.OpenPortal(ctx, env.tenants.byID[1], "https://app.test/app/billing")
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://portal.stripe.test/cus_1" || env.gateway.returnURL != "https://app.test/app/billing" {
		t.Fatalf("url = %q, return = %q", url, env.gateway.returnURL)
	}

	_, err = env.svc.OpenPortal(ctx, env.tenants.byID[2], "https://app.test/app/billing")
	assertCode(t, err, apperrors.CodeNotFound)
}

func TestHandleEventCheckoutThenSubscriptionUpgrades(t *testing.T) {
	env := newTestEnv(t, tenants.Tenant{ID: 1})
	ctx := context.Background()

	err := env.svc.HandleEvent(ctx, newEvent(t, "evt_1", stripe.EventTypeCheckoutSessionCompleted, map[string]any{
		"id": "cs_1", "object": "checkout.session", "mode": "subscription",
		"client_reference_id": "1", "customer": "cus_1", "subscription": "sub_1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	got := env.tenants.byID[1]
	if got.StripeCustomerID != "cus_1" || got.StripeSubscriptionID != "sub_1" || got.Plan != tenants.PlanFree {
		t.Fatalf("after checkout tenant = %+v, want cus_1/sub_1 on free", got)
	}

	end := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	err = env.svc.HandleEvent(ctx, newEvent(t, "evt_2", stripe.EventTypeCustomerSubscriptionCreated,
		subscriptionObject("sub_1", "cus_1", stripe.SubscriptionStatusActive, end.Unix())))
	if err != nil {
		t.Fatal(err)
	}
	got = env.tenants.byID[1]
	if got.Plan != tenants.PlanPro || got.SubscriptionStatus != "active" || got.CurrentPeriodEnd == nil || !got.CurrentPeriodEnd.Equal(end) {
		t.Fatalf("after subscription tenant = %+v, want pro/active ending %v", got, end)
	}
	if env.tx.Commits != 2 || env.tx.Rollbacks != 0 {
		t.Fatalf("commits/rollbacks = %d/%d, want 2/0", env.tx.Commits, env.tx.Rollbacks)
	}
}

func TestHandleEventCheckoutAfterSubscriptionKeepsStatus(t *testing.T) {
	end := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_1",
		SubscriptionStatus: "trialing", CurrentPeriodEnd: &end, Plan: tenants.PlanPro})

	err := env.svc.HandleEvent(context.Background(), newEvent(t, "evt_1", stripe.EventTypeCheckoutSessionCompleted, map[string]any{
		"id": "cs_1", "object": "checkout.session", "mode": "subscription",
		"client_reference_id": "1", "customer": "cus_1", "subscription": "sub_1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := env.tenants.byID[1]; got.Plan != tenants.PlanPro || got.SubscriptionStatus != "trialing" || got.CurrentPeriodEnd == nil {
		t.Fatalf("tenant = %+v, want pro/trialing kept", got)
	}
}

func TestHandleEventCheckoutOfNewSubscriptionClearsStatus(t *testing.T) {
	end := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_old",
		SubscriptionStatus: "canceled", CurrentPeriodEnd: &end})

	err := env.svc.HandleEvent(context.Background(), newEvent(t, "evt_1", stripe.EventTypeCheckoutSessionCompleted, map[string]any{
		"id": "cs_1", "object": "checkout.session", "mode": "subscription",
		"client_reference_id": "1", "customer": "cus_1", "subscription": "sub_new",
	}))
	if err != nil {
		t.Fatal(err)
	}
	got := env.tenants.byID[1]
	if got.StripeSubscriptionID != "sub_new" || got.SubscriptionStatus != "" || got.CurrentPeriodEnd != nil || got.Plan != tenants.PlanFree {
		t.Fatalf("tenant = %+v, want sub_new with unknown status on free", got)
	}
}

func TestHandleEventCheckoutCustomerConflictIsAcknowledged(t *testing.T) {
	env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_mine"})
	env.tenants.setErr = apperrors.NewConflict("stripe customer already linked to another workspace")

	err := env.svc.HandleEvent(context.Background(), newEvent(t, "evt_1", stripe.EventTypeCheckoutSessionCompleted, map[string]any{
		"id": "cs_1", "object": "checkout.session", "mode": "subscription",
		"client_reference_id": "1", "customer": "cus_other", "subscription": "sub_1",
	}))
	if err != nil {
		t.Fatalf("err = %v, want the event acknowledged", err)
	}
	if got := env.tenants.byID[1]; got.StripeCustomerID != "cus_mine" || got.StripeSubscriptionID != "" {
		t.Fatalf("tenant = %+v, want untouched", got)
	}
	if env.tx.Commits != 1 {
		t.Fatalf("commits = %d, want 1", env.tx.Commits)
	}
}

func TestHandleEventCheckoutFallsBackToCustomer(t *testing.T) {
	env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1"})

	err := env.svc.HandleEvent(context.Background(), newEvent(t, "evt_1", stripe.EventTypeCheckoutSessionCompleted, map[string]any{
		"id": "cs_1", "object": "checkout.session", "mode": "subscription", "customer": "cus_1", "subscription": "sub_1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := env.tenants.byID[1].StripeSubscriptionID; got != "sub_1" {
		t.Fatalf("subscription = %q, want sub_1", got)
	}
}

func TestHandleEventSubscriptionTransitions(t *testing.T) {
	tests := []struct {
		name       string
		typ        stripe.EventType
		status     stripe.SubscriptionStatus
		plan       tenants.Plan
		wantPlan   tenants.Plan
		wantStatus string
	}{
		{"created active", stripe.EventTypeCustomerSubscriptionCreated, stripe.SubscriptionStatusActive, tenants.PlanFree, tenants.PlanPro, "active"},
		{"created trialing", stripe.EventTypeCustomerSubscriptionCreated, stripe.SubscriptionStatusTrialing, tenants.PlanFree, tenants.PlanPro, "trialing"},
		{"updated past_due keeps pro", stripe.EventTypeCustomerSubscriptionUpdated, stripe.SubscriptionStatusPastDue, tenants.PlanPro, tenants.PlanPro, "past_due"},
		{"updated unpaid", stripe.EventTypeCustomerSubscriptionUpdated, stripe.SubscriptionStatusUnpaid, tenants.PlanPro, tenants.PlanFree, "unpaid"},
		{"updated canceled", stripe.EventTypeCustomerSubscriptionUpdated, stripe.SubscriptionStatusCanceled, tenants.PlanPro, tenants.PlanFree, "canceled"},
		{"deleted is canceled", stripe.EventTypeCustomerSubscriptionDeleted, stripe.SubscriptionStatusActive, tenants.PlanPro, tenants.PlanFree, "canceled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_1", Plan: tt.plan})

			err := env.svc.HandleEvent(context.Background(), newEvent(t, "evt_1", tt.typ,
				subscriptionObject("sub_1", "cus_1", tt.status, 1_800_000_000)))
			if err != nil {
				t.Fatal(err)
			}
			got := env.tenants.byID[1]
			if got.Plan != tt.wantPlan || got.SubscriptionStatus != tt.wantStatus {
				t.Fatalf("plan/status = %s/%s, want %s/%s", got.Plan, got.SubscriptionStatus, tt.wantPlan, tt.wantStatus)
			}
		})
	}
}

func TestHandleEventIgnoresStaleSubscription(t *testing.T) {
	env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_new",
		SubscriptionStatus: "active", Plan: tenants.PlanPro})

	err := env.svc.HandleEvent(context.Background(), newEvent(t, "evt_1", stripe.EventTypeCustomerSubscriptionDeleted,
		subscriptionObject("sub_old", "cus_1", stripe.SubscriptionStatusCanceled, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if got := env.tenants.byID[1]; got.Plan != tenants.PlanPro || got.StripeSubscriptionID != "sub_new" {
		t.Fatalf("tenant = %+v, want sub_new on pro untouched", got)
	}
}

func TestHandleEventIgnoresOutOfOrderSubscriptionEvents(t *testing.T) {
	tests := []struct {
		name       string
		first      stripe.EventType
		status     stripe.SubscriptionStatus
		wantStatus string
		wantPlan   tenants.Plan
	}{
		{"retry after cancel", stripe.EventTypeCustomerSubscriptionDeleted, stripe.SubscriptionStatusCanceled, "canceled", tenants.PlanFree},
		{"older update", stripe.EventTypeCustomerSubscriptionUpdated, stripe.SubscriptionStatusPastDue, "past_due", tenants.PlanPro},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_1",
				SubscriptionStatus: "active", Plan: tenants.PlanPro})
			ctx := context.Background()

			if err := env.svc.HandleEvent(ctx, newEventAt(t, "evt_2", tt.first, 200,
				subscriptionObject("sub_1", "cus_1", tt.status, 0))); err != nil {
				t.Fatal(err)
			}
			// An update created earlier, delivered late by a retry.
			if err := env.svc.HandleEvent(ctx, newEventAt(t, "evt_1", stripe.EventTypeCustomerSubscriptionUpdated, 100,
				subscriptionObject("sub_1", "cus_1", stripe.SubscriptionStatusActive, 0))); err != nil {
				t.Fatal(err)
			}
			got := env.tenants.byID[1]
			if got.SubscriptionStatus != tt.wantStatus || got.Plan != tt.wantPlan {
				t.Fatalf("plan/status = %s/%s, want %s/%s", got.Plan, got.SubscriptionStatus, tt.wantPlan, tt.wantStatus)
			}
			if env.tx.Commits != 2 || env.tx.Rollbacks != 0 {
				t.Fatalf("commits/rollbacks = %d/%d, want 2/0", env.tx.Commits, env.tx.Rollbacks)
			}
		})
	}
}

func TestHandleEventAppliesNewerSubscriptionEvent(t *testing.T) {
	env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_1",
		SubscriptionStatus: "active", Plan: tenants.PlanPro})
	ctx := context.Background()

	for i, status := range []stripe.SubscriptionStatus{stripe.SubscriptionStatusPastDue, stripe.SubscriptionStatusActive} {
		if err := env.svc.HandleEvent(ctx, newEventAt(t, "evt_"+string(status), stripe.EventTypeCustomerSubscriptionUpdated, int64(100+i),
			subscriptionObject("sub_1", "cus_1", status, 0))); err != nil {
			t.Fatal(err)
		}
	}
	if got := env.tenants.byID[1]; got.SubscriptionStatus != "active" || got.Plan != tenants.PlanPro {
		t.Fatalf("tenant = %+v, want active on pro", got)
	}
}

func TestHandleEventPaymentFailedIgnoresEndedSubscription(t *testing.T) {
	ownerID := int32(42)
	env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_1",
		SubscriptionStatus: "canceled", Plan: tenants.PlanFree, OwnerUserID: &ownerID})
	env.users.byID[ownerID] = auth.User{ID: ownerID, Email: "owner@example.com"}

	err := env.svc.HandleEvent(context.Background(), newEvent(t, "evt_1", stripe.EventTypeInvoicePaymentFailed,
		invoiceObject("cus_1", "sub_1", 0)))
	if err != nil {
		t.Fatal(err)
	}
	if got := env.tenants.byID[1]; got.SubscriptionStatus != "canceled" || env.tenants.updates != 0 {
		t.Fatalf("tenant = %+v, updates = %d; want canceled untouched", got, env.tenants.updates)
	}
	if len(env.notifier.paymentFailed) != 0 {
		t.Fatalf("paymentFailed = %+v, want none: the update was skipped", env.notifier.paymentFailed)
	}
}

func TestHandleEventPaymentFailed(t *testing.T) {
	tests := []struct {
		name        string
		nextAttempt int64
		wantStatus  string
		wantPlan    tenants.Plan
	}{
		{"retry scheduled", 1_800_000_000, "past_due", tenants.PlanPro},
		{"no more retries stays past_due", 0, "past_due", tenants.PlanPro},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ownerID := int32(42)
			env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_1",
				SubscriptionStatus: "active", Plan: tenants.PlanPro, OwnerUserID: &ownerID})
			env.users.byID[ownerID] = auth.User{ID: ownerID, Email: "owner@example.com"}

			err := env.svc.HandleEvent(context.Background(), newEvent(t, "evt_1", stripe.EventTypeInvoicePaymentFailed,
				invoiceObject("cus_1", "sub_1", tt.nextAttempt)))
			if err != nil {
				t.Fatal(err)
			}
			got := env.tenants.byID[1]
			if got.SubscriptionStatus != tt.wantStatus || got.Plan != tt.wantPlan || got.StripeSubscriptionID != "sub_1" {
				t.Fatalf("tenant = %+v, want %s on %s", got, tt.wantStatus, tt.wantPlan)
			}
			if len(env.notifier.paymentFailed) != 1 || env.notifier.paymentFailed[0].Email != "owner@example.com" {
				t.Fatalf("paymentFailed = %+v, want one notification to owner@example.com", env.notifier.paymentFailed)
			}
		})
	}
}

func TestHandleEventPaymentFailedNoNotificationWithoutOwner(t *testing.T) {
	env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_1",
		SubscriptionStatus: "active", Plan: tenants.PlanPro})

	err := env.svc.HandleEvent(context.Background(), newEvent(t, "evt_1", stripe.EventTypeInvoicePaymentFailed,
		invoiceObject("cus_1", "sub_1", 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.notifier.paymentFailed) != 0 {
		t.Fatalf("paymentFailed = %+v, want none: tenant has no owner", env.notifier.paymentFailed)
	}
}

func TestHandleEventPaymentFailedNoNotificationWithoutOwnerEmail(t *testing.T) {
	ownerID := int32(42)
	env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_1",
		SubscriptionStatus: "active", Plan: tenants.PlanPro, OwnerUserID: &ownerID})
	env.users.byID[ownerID] = auth.User{ID: ownerID}

	err := env.svc.HandleEvent(context.Background(), newEvent(t, "evt_1", stripe.EventTypeInvoicePaymentFailed,
		invoiceObject("cus_1", "sub_1", 0)))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.notifier.paymentFailed) != 0 {
		t.Fatalf("paymentFailed = %+v, want none: owner has no email", env.notifier.paymentFailed)
	}
}

func TestHandleEventPaymentFailedNotifiesOnceOnRetry(t *testing.T) {
	ownerID := int32(42)
	env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_1",
		SubscriptionStatus: "active", Plan: tenants.PlanPro, OwnerUserID: &ownerID})
	env.users.byID[ownerID] = auth.User{ID: ownerID, Email: "owner@example.com"}
	ctx := context.Background()
	ev := newEvent(t, "evt_1", stripe.EventTypeInvoicePaymentFailed, invoiceObject("cus_1", "sub_1", 0))

	for range 2 {
		if err := env.svc.HandleEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if len(env.notifier.paymentFailed) != 1 {
		t.Fatalf("paymentFailed = %+v, want exactly one: the event is deduped on retry", env.notifier.paymentFailed)
	}
}

func TestHandleEventPaymentFailedNotifierErrorDoesNotFailEvent(t *testing.T) {
	ownerID := int32(42)
	env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1", StripeSubscriptionID: "sub_1",
		SubscriptionStatus: "active", Plan: tenants.PlanPro, OwnerUserID: &ownerID})
	env.users.byID[ownerID] = auth.User{ID: ownerID, Email: "owner@example.com"}
	env.notifier.err = stderrors.New("smtp down")

	err := env.svc.HandleEvent(context.Background(), newEvent(t, "evt_1", stripe.EventTypeInvoicePaymentFailed,
		invoiceObject("cus_1", "sub_1", 0)))
	if err != nil {
		t.Fatalf("HandleEvent() error = %v, want nil: a notification failure must not fail the webhook", err)
	}
	if got := env.tenants.byID[1]; got.SubscriptionStatus != "past_due" {
		t.Fatalf("tenant = %+v, want past_due despite the notification failure", got)
	}
}

func TestHandleEventIsIdempotent(t *testing.T) {
	env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1"})
	ctx := context.Background()
	ev := newEvent(t, "evt_1", stripe.EventTypeCustomerSubscriptionCreated,
		subscriptionObject("sub_1", "cus_1", stripe.SubscriptionStatusActive, 1_800_000_000))

	for range 2 {
		if err := env.svc.HandleEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if env.tenants.updates != 1 {
		t.Fatalf("updates = %d, want 1", env.tenants.updates)
	}
}

func TestHandleEventRecordsAndIgnoresUnknown(t *testing.T) {
	env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1"})

	err := env.svc.HandleEvent(context.Background(), newEvent(t, "evt_1", "customer.created", map[string]any{"id": "cus_1"}))
	if err != nil {
		t.Fatal(err)
	}
	if env.events.seen["evt_1"] != "customer.created" || env.tenants.updates != 0 {
		t.Fatalf("seen = %v, updates = %d; want recorded and no update", env.events.seen, env.tenants.updates)
	}
}

func TestHandleEventIgnoresUnknownCustomer(t *testing.T) {
	env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1"})

	err := env.svc.HandleEvent(context.Background(), newEvent(t, "evt_1", stripe.EventTypeCustomerSubscriptionUpdated,
		subscriptionObject("sub_9", "cus_other", stripe.SubscriptionStatusActive, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if env.tenants.updates != 0 || env.tx.Commits != 1 {
		t.Fatalf("updates/commits = %d/%d, want 0/1", env.tenants.updates, env.tx.Commits)
	}
}

func TestHandleEventErrors(t *testing.T) {
	t.Run("missing id", func(t *testing.T) {
		env := newTestEnv(t)
		assertCode(t, env.svc.HandleEvent(context.Background(), stripe.Event{Type: "customer.created"}), apperrors.CodeInvalidArgument)
	})
	t.Run("malformed payload rolls back", func(t *testing.T) {
		env := newTestEnv(t)
		ev := stripe.Event{ID: "evt_1", Type: stripe.EventTypeCustomerSubscriptionUpdated, Data: &stripe.EventData{Raw: json.RawMessage(`[1]`)}}
		assertCode(t, env.svc.HandleEvent(context.Background(), ev), apperrors.CodeInvalidArgument)
		if env.tx.Rollbacks != 1 {
			t.Fatalf("rollbacks = %d, want 1", env.tx.Rollbacks)
		}
	})
	t.Run("update failure rolls back", func(t *testing.T) {
		env := newTestEnv(t, tenants.Tenant{ID: 1, StripeCustomerID: "cus_1"})
		env.tenants.err = apperrors.NewInternal(stderrors.New("db down"))
		err := env.svc.HandleEvent(context.Background(), newEvent(t, "evt_1", stripe.EventTypeCustomerSubscriptionUpdated,
			subscriptionObject("sub_1", "cus_1", stripe.SubscriptionStatusActive, 0)))
		assertCode(t, err, apperrors.CodeInternal)
		if env.tx.Rollbacks != 1 {
			t.Fatalf("rollbacks = %d, want 1", env.tx.Rollbacks)
		}
	})
	t.Run("event log failure", func(t *testing.T) {
		env := newTestEnv(t)
		env.events.err = apperrors.NewInternal(stderrors.New("db down"))
		assertCode(t, env.svc.HandleEvent(context.Background(), stripe.Event{ID: "evt_1", Type: "customer.created"}), apperrors.CodeInternal)
	})
}
