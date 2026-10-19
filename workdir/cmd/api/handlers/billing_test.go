package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/webhook"

	"app/internal/config"
	"app/internal/database"
	"app/internal/db"
	apperrors "app/internal/errors"
	"app/internal/pkg/auth"
	"app/internal/pkg/billing"
	"app/internal/pkg/mailer"
	"app/internal/pkg/tenants"
)

// billingTestOrigin is the Origin of a same-origin request to httptest.NewRequest's host.
const billingTestOrigin = "http://example.com"

// fakeGateway is a billing.Gateway recording its calls and returning
// canned Stripe URLs.
type fakeGateway struct {
	customers []string // emails passed to CreateCustomer
	checkouts []string // customer IDs passed to CreateCheckoutSession
	portals   []string // customer IDs passed to CreatePortalSession
	urls      billing.CheckoutURLs
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

func (g *fakeGateway) CreateCheckoutSession(_ context.Context, customerID, _, successURL, cancelURL string, _ int32) (string, error) {
	if g.err != nil {
		return "", g.err
	}
	g.checkouts = append(g.checkouts, customerID)
	g.urls = billing.CheckoutURLs{SuccessURL: successURL, CancelURL: cancelURL}
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

// fakeEventLog is a billing.EventLog remembering the processed event ids.
type fakeEventLog struct {
	seen map[string]string
}

func (f *fakeEventLog) MarkProcessed(_ context.Context, id, eventType string) (bool, error) {
	if _, ok := f.seen[id]; ok {
		return false, nil
	}
	f.seen[id] = eventType
	return true, nil
}

// billingTenantID is the workspace of appUser in a billingEnv, distinct from
// testTenant so lookups by ID find it.
const billingTenantID int32 = 8

// billingOwnerEmail is the email of the owner of billingTenantID in
// newBillingEnv, unless the test clears OwnerUserID.
const billingOwnerEmail = "owner@example.com"

// billingEnv is appServices with a billing service over fakes. appUser owns
// tenant billingTenantID, on the free plan unless changed. mail records the
// payment-failed emails the billing service sends.
type billingEnv struct {
	svc     Services
	tenants *fakeTenants
	gateway *fakeGateway
	events  *fakeEventLog
	mail    *mailer.LogMailer
}

// fakeBillingUsers is a billing.Users over a fixed set of users by ID.
type fakeBillingUsers map[int32]auth.User

func (f fakeBillingUsers) GetByID(_ context.Context, id int32) (auth.User, error) {
	if u, ok := f[id]; ok {
		return u, nil
	}
	return auth.User{}, apperrors.NewNotFound("user not found")
}

func newBillingEnv(t *testing.T, change func(*tenants.Tenant)) billingEnv {
	t.Helper()
	svc := appServices()
	env := billingEnv{
		svc:     svc,
		tenants: svc.tenants.(*fakeTenants),
		gateway: &fakeGateway{},
		events:  &fakeEventLog{seen: map[string]string{}},
		mail:    mailer.NewLogMailer(nil),
	}
	tenant := env.tenants.owned[2]
	tenant.ID, tenant.APIKey, tenant.Plan = billingTenantID, "billing-key", tenants.PlanFree
	if change != nil {
		change(&tenant)
	}
	env.tenants.owned[2] = tenant
	users := fakeBillingUsers{}
	if tenant.OwnerUserID != nil {
		users[*tenant.OwnerUserID] = auth.User{ID: *tenant.OwnerUserID, Email: billingOwnerEmail}
	}
	billingSvc, err := billing.NewService(env.gateway, env.tenants, billing.Options{
		PriceID:    billingTestStripe.PriceID,
		Tx:         &database.FakeTxRunner{},
		TenantsFor: func(db.Querier) tenants.Repository { return env.tenants },
		EventsFor:  func(db.Querier) billing.EventLog { return env.events },
		UsersFor:   func(db.Querier) billing.Users { return users },
		Notifier:   billing.NewMailNotifier(env.mail, testApp.BaseURL),
	})
	if err != nil {
		t.Fatal(err)
	}
	env.svc.billing = billingSvc
	return env
}

// serve sends req through the routes with Stripe enabled.
func (e billingEnv) serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	Routes(e.svc, testFeatures, billingTestStripe, testApp).ServeHTTP(rec, req)
	return rec
}

// tenant returns the current state of appUser's workspace.
func (e billingEnv) tenant() tenants.Tenant {
	return e.tenants.owned[2]
}

// withCustomer links appUser's workspace to the Stripe customer cus_1.
func withCustomer(t *tenants.Tenant) { t.StripeCustomerID = "cus_1" }

// stripeEvent returns the JSON of an event of type typ about obj, with the
// API version stripe-go expects.
func stripeEvent(t *testing.T, id string, typ stripe.EventType, obj map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"id": id, "object": "event", "type": typ, "api_version": stripe.APIVersion,
		"data": map[string]any{"object": obj},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// webhookRequest returns a POST /webhooks/stripe of payload signed with secret.
func webhookRequest(payload []byte, secret string) *http.Request {
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: secret})
	req := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader(string(payload)))
	req.Header.Set(stripeSignatureHeader, signed.Header)
	return req
}

// sendEvent posts payload signed with the configured webhook secret.
func (e billingEnv) sendEvent(t *testing.T, payload []byte) *httptest.ResponseRecorder {
	t.Helper()
	return e.serve(webhookRequest(payload, billingTestStripe.WebhookSecret))
}

func checkoutObject(customer, subscription string, tenantID int32) map[string]any {
	return map[string]any{
		"id": "cs_1", "object": "checkout.session", "mode": "subscription",
		"customer": customer, "subscription": subscription, "client_reference_id": strconv.Itoa(int(tenantID)),
	}
}

func subscriptionObject(id, customer string, status stripe.SubscriptionStatus, periodEnd int64) map[string]any {
	return map[string]any{
		"id": id, "object": "subscription", "customer": customer, "status": status,
		"items": map[string]any{"object": "list", "data": []any{
			map[string]any{"id": "si_1", "object": "subscription_item", "current_period_end": periodEnd},
		}},
	}
}

func invoiceObject(customer, subscription string, nextAttempt int64) map[string]any {
	return map[string]any{
		"id": "in_1", "object": "invoice", "customer": customer, "next_payment_attempt": nextAttempt,
		"parent": map[string]any{"type": "subscription_details", "subscription_details": map[string]any{"subscription": subscription}},
	}
}

// assertPlan fails the test when the workspace is not on plan with status.
func assertPlan(t *testing.T, got tenants.Tenant, plan tenants.Plan, status string) {
	t.Helper()
	if got.Plan != plan || got.SubscriptionStatus != status {
		t.Fatalf("tenant plan/status = %q/%q, want %q/%q", got.Plan, got.SubscriptionStatus, plan, status)
	}
}

func TestStripeWebhookRejectsBadSignature(t *testing.T) {
	env := newBillingEnv(t, withCustomer)
	payload := stripeEvent(t, "evt_1", stripe.EventTypeCustomerSubscriptionUpdated,
		subscriptionObject("sub_1", "cus_1", stripe.SubscriptionStatusActive, 1))
	missing := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader(string(payload)))
	// Signed for payload, sent with another body.
	tampered := httptest.NewRequest(http.MethodPost, "/webhooks/stripe", strings.NewReader(strings.Replace(string(payload), "active", "trialing", 1)))
	tampered.Header.Set(stripeSignatureHeader, webhookRequest(payload, billingTestStripe.WebhookSecret).Header.Get(stripeSignatureHeader))
	tests := []struct {
		name string
		req  *http.Request
	}{
		{"missing signature", missing},
		{"wrong secret", webhookRequest(payload, "whsec_other")},
		{"tampered body", tampered},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertStatus(t, env.serve(tt.req), http.StatusBadRequest)
			if len(env.events.seen) != 0 || len(env.tenants.subscriptions) != 0 {
				t.Fatalf("event applied: seen %v, updates %v", env.events.seen, env.tenants.subscriptions)
			}
		})
	}
}

func TestStripeWebhookRejectsOversizedBody(t *testing.T) {
	env := newBillingEnv(t, nil)
	payload := []byte(`{"id":"evt_1","pad":"` + strings.Repeat("x", maxStripeEventBytes) + `"}`)
	assertStatus(t, env.sendEvent(t, payload), http.StatusBadRequest)
}

func TestStripeWebhookUpgradesAfterCheckout(t *testing.T) {
	periodEnd := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	for _, status := range []stripe.SubscriptionStatus{stripe.SubscriptionStatusActive, stripe.SubscriptionStatusTrialing} {
		t.Run(string(status), func(t *testing.T) {
			env := newBillingEnv(t, nil)
			assertStatus(t, env.sendEvent(t, stripeEvent(t, "evt_checkout", stripe.EventTypeCheckoutSessionCompleted,
				checkoutObject("cus_1", "sub_1", billingTenantID))), http.StatusOK)
			if got := env.tenant(); got.StripeCustomerID != "cus_1" || got.StripeSubscriptionID != "sub_1" || got.Plan != tenants.PlanFree {
				t.Fatalf("after checkout: tenant = %+v", got)
			}

			assertStatus(t, env.sendEvent(t, stripeEvent(t, "evt_sub", stripe.EventTypeCustomerSubscriptionCreated,
				subscriptionObject("sub_1", "cus_1", status, periodEnd.Unix()))), http.StatusOK)
			got := env.tenant()
			assertPlan(t, got, tenants.PlanPro, string(status))
			if got.CurrentPeriodEnd == nil || !got.CurrentPeriodEnd.Equal(periodEnd) {
				t.Fatalf("current period end = %v, want %v", got.CurrentPeriodEnd, periodEnd)
			}
		})
	}
}

func TestStripeWebhookDowngrades(t *testing.T) {
	tests := []struct {
		name   string
		typ    stripe.EventType
		status stripe.SubscriptionStatus
		want   string
	}{
		{"deleted", stripe.EventTypeCustomerSubscriptionDeleted, stripe.SubscriptionStatusActive, "canceled"},
		{"unpaid", stripe.EventTypeCustomerSubscriptionUpdated, stripe.SubscriptionStatusUnpaid, "unpaid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newBillingEnv(t, func(tn *tenants.Tenant) {
				tn.Plan, tn.StripeCustomerID, tn.StripeSubscriptionID, tn.SubscriptionStatus = tenants.PlanPro, "cus_1", "sub_1", "active"
			})
			assertStatus(t, env.sendEvent(t, stripeEvent(t, "evt_1", tt.typ,
				subscriptionObject("sub_1", "cus_1", tt.status, 1))), http.StatusOK)
			assertPlan(t, env.tenant(), tenants.PlanFree, tt.want)
		})
	}
}

func TestStripeWebhookPaymentFailed(t *testing.T) {
	tests := []struct {
		name        string
		nextAttempt int64
		plan        tenants.Plan
		status      string
	}{
		{"retried", time.Now().Add(time.Hour).Unix(), tenants.PlanPro, "past_due"},
		{"final attempt stays past_due", 0, tenants.PlanPro, "past_due"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newBillingEnv(t, func(tn *tenants.Tenant) {
				tn.Plan, tn.StripeCustomerID, tn.StripeSubscriptionID, tn.SubscriptionStatus = tenants.PlanPro, "cus_1", "sub_1", "active"
			})
			assertStatus(t, env.sendEvent(t, stripeEvent(t, "evt_1", stripe.EventTypeInvoicePaymentFailed,
				invoiceObject("cus_1", "sub_1", tt.nextAttempt))), http.StatusOK)
			assertPlan(t, env.tenant(), tt.plan, tt.status)
		})
	}
}

func TestStripeWebhookPaymentFailedNotifiesOwner(t *testing.T) {
	env := newBillingEnv(t, func(tn *tenants.Tenant) {
		tn.Plan, tn.StripeCustomerID, tn.StripeSubscriptionID, tn.SubscriptionStatus = tenants.PlanPro, "cus_1", "sub_1", "active"
	})
	assertStatus(t, env.sendEvent(t, stripeEvent(t, "evt_1", stripe.EventTypeInvoicePaymentFailed,
		invoiceObject("cus_1", "sub_1", 0))), http.StatusOK)
	if sent := env.mail.Sent(); len(sent) != 1 || sent[0].To != billingOwnerEmail {
		t.Fatalf("sent = %+v, want one payment-failed email to %s", sent, billingOwnerEmail)
	}
}

func TestStripeWebhookPaymentFailedNoOwnerNoEmail(t *testing.T) {
	env := newBillingEnv(t, func(tn *tenants.Tenant) {
		tn.Plan, tn.StripeCustomerID, tn.StripeSubscriptionID, tn.SubscriptionStatus = tenants.PlanPro, "cus_1", "sub_1", "active"
		tn.OwnerUserID = nil
	})
	assertStatus(t, env.sendEvent(t, stripeEvent(t, "evt_1", stripe.EventTypeInvoicePaymentFailed,
		invoiceObject("cus_1", "sub_1", 0))), http.StatusOK)
	if sent := env.mail.Sent(); len(sent) != 0 {
		t.Fatalf("sent = %+v, want none: tenant has no owner", sent)
	}
}

func TestStripeWebhookAcceptsOtherAPIVersion(t *testing.T) {
	env := newBillingEnv(t, withCustomer)
	raw, err := json.Marshal(map[string]any{
		"id": "evt_1", "object": "event", "type": stripe.EventTypeCustomerSubscriptionUpdated, "api_version": "2099-01-01.clover",
		"data": map[string]any{"object": subscriptionObject("sub_1", "cus_1", stripe.SubscriptionStatusActive, 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertStatus(t, env.sendEvent(t, raw), http.StatusOK)
	assertPlan(t, env.tenant(), tenants.PlanPro, "active")
}

func TestStripeWebhookIsIdempotent(t *testing.T) {
	env := newBillingEnv(t, withCustomer)
	payload := stripeEvent(t, "evt_1", stripe.EventTypeCustomerSubscriptionUpdated,
		subscriptionObject("sub_1", "cus_1", stripe.SubscriptionStatusActive, 1))
	for range 2 {
		assertStatus(t, env.sendEvent(t, payload), http.StatusOK)
	}
	if n := len(env.tenants.subscriptions); n != 1 {
		t.Fatalf("subscription updated %d times, want 1", n)
	}
	assertPlan(t, env.tenant(), tenants.PlanPro, "active")
}

func TestStripeWebhookIgnoresUnknownEvent(t *testing.T) {
	env := newBillingEnv(t, withCustomer)
	assertStatus(t, env.sendEvent(t, stripeEvent(t, "evt_1", "customer.created",
		map[string]any{"id": "cus_1", "object": "customer"})), http.StatusOK)
	if env.events.seen["evt_1"] != "customer.created" || len(env.tenants.subscriptions) != 0 {
		t.Fatalf("seen %v, updates %v", env.events.seen, env.tenants.subscriptions)
	}
}

func TestStripeWebhookMalformedEvent(t *testing.T) {
	env := newBillingEnv(t, withCustomer)
	payload := []byte(`{"id":"evt_1","object":"event","type":"customer.subscription.updated","api_version":"` +
		stripe.APIVersion + `","data":{"object":{"id":"sub_1","status":{}}}}`)
	assertStatus(t, env.sendEvent(t, payload), http.StatusBadRequest)
}

func TestStripeWebhookProcessingError(t *testing.T) {
	env := newBillingEnv(t, withCustomer)
	env.tenants.err = apperrors.NewInternal(errors.New("db down"))
	assertStatus(t, env.sendEvent(t, stripeEvent(t, "evt_1", stripe.EventTypeCustomerSubscriptionUpdated,
		subscriptionObject("sub_1", "cus_1", stripe.SubscriptionStatusActive, 1))), http.StatusInternalServerError)
}

func TestBillingRoutesNotFoundWithoutStripe(t *testing.T) {
	env := newBillingEnv(t, withCustomer)
	disabled := billingTestStripe
	disabled.PriceID = ""
	requests := map[string]func() *http.Request{
		"webhook": func() *http.Request {
			return webhookRequest(stripeEvent(t, "evt_1", "customer.created", map[string]any{"id": "cus_1"}), billingTestStripe.WebhookSecret)
		},
		"checkout": func() *http.Request {
			return cookieRequest(t, env.svc, http.MethodPost, "/billing/checkout", "", appUser, billingTestOrigin)
		},
		"portal": func() *http.Request {
			return cookieRequest(t, env.svc, http.MethodPost, "/billing/portal", "", appUser, billingTestOrigin)
		},
	}
	configs := []struct {
		name    string
		stripe  config.StripeConfig
		billing bool
	}{
		{"unconfigured", testStripe, true},
		{"partial", disabled, true},
		{"no billing service", billingTestStripe, false},
	}
	for _, c := range configs {
		for name, req := range requests {
			svc := env.svc
			if !c.billing {
				svc.billing = nil
			}
			rec := httptest.NewRecorder()
			Routes(svc, testFeatures, c.stripe, testApp).ServeHTTP(rec, req())
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s: %s = %d, want 404", c.name, name, rec.Code)
			}
		}
	}
}

func TestBillingCheckout(t *testing.T) {
	tests := []struct {
		name      string
		change    func(*tenants.Tenant)
		customer  string
		created   int
		storedCus string
	}{
		{"reuses the customer", withCustomer, "cus_1", 0, "cus_1"},
		{"creates the customer", nil, "cus_new", 1, "cus_new"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newBillingEnv(t, tt.change)
			rec := env.serve(cookieRequest(t, env.svc, http.MethodPost, "/billing/checkout", "", appUser, billingTestOrigin))
			assertStatus(t, rec, http.StatusSeeOther)
			if loc := rec.Header().Get("Location"); loc != "https://checkout.stripe.test/"+tt.customer {
				t.Fatalf("Location = %q", loc)
			}
			if len(env.gateway.customers) != tt.created || env.tenant().StripeCustomerID != tt.storedCus {
				t.Fatalf("customers created %v, stored %q", env.gateway.customers, env.tenant().StripeCustomerID)
			}
			want := billing.CheckoutURLs{
				SuccessURL: "https://forms.example.com/app/billing?checkout=success",
				CancelURL:  "https://forms.example.com/app/billing?checkout=cancel",
			}
			if env.gateway.urls != want {
				t.Fatalf("checkout URLs = %+v, want %+v", env.gateway.urls, want)
			}
		})
	}
}

func TestBillingCheckoutAlreadyPro(t *testing.T) {
	env := newBillingEnv(t, func(tn *tenants.Tenant) { tn.Plan, tn.StripeCustomerID = tenants.PlanPro, "cus_1" })
	rec := env.serve(cookieRequest(t, env.svc, http.MethodPost, "/billing/checkout", "", appUser, billingTestOrigin))
	assertStatus(t, rec, http.StatusConflict)
	if len(env.gateway.checkouts) != 0 {
		t.Fatalf("checkout started: %v", env.gateway.checkouts)
	}
}

func TestBillingCheckoutExistingSubscription(t *testing.T) {
	env := newBillingEnv(t, func(tn *tenants.Tenant) {
		tn.StripeCustomerID, tn.StripeSubscriptionID, tn.SubscriptionStatus = "cus_1", "sub_1", "past_due"
	})
	rec := env.serve(cookieRequest(t, env.svc, http.MethodPost, "/billing/checkout", "", appUser, billingTestOrigin))
	assertStatus(t, rec, http.StatusConflict)
	if len(env.gateway.checkouts) != 0 {
		t.Fatalf("checkout started: %v", env.gateway.checkouts)
	}
}

func TestBillingCheckoutGatewayError(t *testing.T) {
	env := newBillingEnv(t, withCustomer)
	env.gateway.err = apperrors.NewInternal(errors.New("stripe: secret detail"))
	rec := env.serve(cookieRequest(t, env.svc, http.MethodPost, "/billing/checkout", "", appUser, billingTestOrigin))
	assertStatus(t, rec, http.StatusInternalServerError)
	if strings.Contains(rec.Body.String(), "secret detail") {
		t.Fatal("error page leaks the gateway error")
	}
}

func TestBillingPortal(t *testing.T) {
	env := newBillingEnv(t, withCustomer)
	rec := env.serve(cookieRequest(t, env.svc, http.MethodPost, "/billing/portal", "", appUser, billingTestOrigin))
	assertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "https://portal.stripe.test/cus_1" {
		t.Fatalf("Location = %q", loc)
	}
	if env.gateway.returnURL != "https://forms.example.com/app/billing" {
		t.Fatalf("return URL = %q", env.gateway.returnURL)
	}
}

func TestBillingPortalWithoutCustomer(t *testing.T) {
	env := newBillingEnv(t, nil)
	rec := env.serve(cookieRequest(t, env.svc, http.MethodPost, "/billing/portal", "", appUser, billingTestOrigin))
	assertStatus(t, rec, http.StatusNotFound)
	if !strings.Contains(rec.Body.String(), "no billing account") || len(env.gateway.portals) != 0 {
		t.Fatalf("portals %v, body %s", env.gateway.portals, rec.Body.String())
	}
}

func TestBillingFormsRejections(t *testing.T) {
	for _, path := range []string{"/billing/checkout", "/billing/portal"} {
		env := newBillingEnv(t, withCustomer)

		rec := env.serve(cookieRequest(t, env.svc, http.MethodPost, path, "", appUser, "https://evil.test"))
		assertStatus(t, rec, http.StatusForbidden)

		cross := cookieRequest(t, env.svc, http.MethodPost, path, "", appUser, "")
		cross.Header.Set("Sec-Fetch-Site", "cross-site")
		assertStatus(t, env.serve(cross), http.StatusForbidden)

		rec = env.serve(httptest.NewRequest(http.MethodPost, path, nil))
		assertStatus(t, rec, http.StatusSeeOther)
		if loc := rec.Header().Get("Location"); loc != "/app/signin?next=%2Fapp%2Fbilling" {
			t.Fatalf("%s: Location = %q", path, loc)
		}

		// "basic" has no workspace.
		assertStatus(t, env.serve(cookieRequest(t, env.svc, http.MethodPost, path, "", "basic", billingTestOrigin)), http.StatusForbidden)

		if len(env.gateway.checkouts)+len(env.gateway.portals) != 0 {
			t.Fatalf("%s: gateway called on a rejected request", path)
		}
	}
}
