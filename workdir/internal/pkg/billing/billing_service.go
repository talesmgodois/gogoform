package billing

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/stripe/stripe-go/v82"

	"app/internal/database"
	"app/internal/db"
	apperrors "app/internal/errors"
	"app/internal/pkg/auth"
	"app/internal/pkg/tenants"
)

// Client-facing messages of the errors returned by Service.
const (
	msgNoCustomer   = "workspace has no billing account yet"
	msgAlreadyPro   = "workspace is already on the pro plan"
	msgHasSub       = "workspace already has a subscription; manage it from the billing portal"
	msgInvalidEvent = "invalid stripe event payload"
)

// Options configures a Service.
type Options struct {
	// PriceID is the Stripe price of the pro plan.
	PriceID string
	// Tx runs HandleEvent in one transaction; required.
	Tx database.TxRunner
	// TenantsFor returns the tenants.Repository to use inside a Tx
	// transaction; tenants.NewService when nil.
	TenantsFor func(q db.Querier) tenants.Repository
	// EventsFor returns the EventLog to use inside a Tx transaction;
	// NewEventStore when nil.
	EventsFor func(q db.Querier) EventLog
}

// CheckoutURLs are the pages Stripe sends the user back to after checkout.
type CheckoutURLs struct {
	SuccessURL string
	CancelURL  string
}

// Service starts checkouts and portal sessions and applies Stripe webhook
// events to tenants.
type Service struct {
	gateway    Gateway
	tenants    tenants.Repository
	priceID    string
	tx         database.TxRunner
	tenantsFor func(q db.Querier) tenants.Repository
	eventsFor  func(q db.Querier) EventLog
}

// NewService returns a Service calling Stripe through gateway and storing
// customers in repo.
func NewService(gateway Gateway, repo tenants.Repository, opts Options) (*Service, error) {
	if opts.PriceID == "" {
		return nil, errors.New("billing: price ID is required")
	}
	if opts.Tx == nil {
		return nil, errors.New("billing: transaction runner is required")
	}
	if opts.TenantsFor == nil {
		opts.TenantsFor = func(q db.Querier) tenants.Repository { return tenants.NewService(q) }
	}
	if opts.EventsFor == nil {
		opts.EventsFor = func(q db.Querier) EventLog { return NewEventStore(q) }
	}
	return &Service{
		gateway:    gateway,
		tenants:    repo,
		priceID:    opts.PriceID,
		tx:         opts.Tx,
		tenantsFor: opts.TenantsFor,
		eventsFor:  opts.EventsFor,
	}, nil
}

// StartCheckout returns the URL of a Stripe Checkout Session subscribing
// tenant to the pro plan. The tenant's Stripe customer is reused, or created
// for user and stored on the tenant first. A tenant already on pro, or whose
// subscription has not ended, gets a Conflict so it is not billed twice.
func (s *Service) StartCheckout(ctx context.Context, tenant tenants.Tenant, user auth.User, urls CheckoutURLs) (string, error) {
	if tenant.Plan == tenants.PlanPro {
		return "", apperrors.NewConflict(msgAlreadyPro)
	}
	if tenant.StripeSubscriptionID != "" && !IsEndedStatus(stripe.SubscriptionStatus(tenant.SubscriptionStatus)) {
		return "", apperrors.NewConflict(msgHasSub)
	}
	customerID := tenant.StripeCustomerID
	if customerID == "" {
		id, err := s.gateway.CreateCustomer(ctx, user.Email, tenant.Name, tenant.ID)
		if err != nil {
			return "", err
		}
		if _, err := s.tenants.SetStripeCustomer(ctx, tenant.ID, id); err != nil {
			return "", err
		}
		customerID = id
	}
	return s.gateway.CreateCheckoutSession(ctx, customerID, s.priceID, urls.SuccessURL, urls.CancelURL, tenant.ID)
}

// OpenPortal returns the URL of a Stripe Customer Portal session for tenant,
// linking back to returnURL. A tenant without a Stripe customer gets a
// NotFound.
func (s *Service) OpenPortal(ctx context.Context, tenant tenants.Tenant, returnURL string) (string, error) {
	if tenant.StripeCustomerID == "" {
		return "", apperrors.NewNotFound(msgNoCustomer)
	}
	return s.gateway.CreatePortalSession(ctx, tenant.StripeCustomerID, returnURL)
}

// HandleEvent applies a verified Stripe webhook event in one transaction.
// An event id already processed is skipped, and so are unhandled event types,
// events about customers that belong to no tenant (or to another tenant than
// the checkout's) and subscription updates older than the last one applied.
// A malformed payload
// gets a BadRequest; any other error should make Stripe retry.
func (s *Service) HandleEvent(ctx context.Context, event stripe.Event) error {
	if event.ID == "" {
		return apperrors.NewBadRequest(msgInvalidEvent)
	}
	return s.tx.InTx(ctx, func(q db.Querier) error {
		first, err := s.eventsFor(q).MarkProcessed(ctx, event.ID, string(event.Type))
		if err != nil || !first {
			return err
		}
		repo := s.tenantsFor(q)
		switch event.Type {
		case stripe.EventTypeCheckoutSessionCompleted:
			return handleCheckoutCompleted(ctx, repo, event)
		case stripe.EventTypeCustomerSubscriptionCreated,
			stripe.EventTypeCustomerSubscriptionUpdated,
			stripe.EventTypeCustomerSubscriptionDeleted:
			return handleSubscription(ctx, repo, event)
		case stripe.EventTypeInvoicePaymentFailed:
			return handlePaymentFailed(ctx, repo, event)
		default:
			return nil
		}
	})
}

// handleCheckoutCompleted links the session's customer and subscription to
// the tenant named by client_reference_id (or, failing that, owning the
// customer). Status and plan are left to the subscription events: the link
// keeps the status a subscription event already stored for this
// subscription, even when that event commits concurrently.
func handleCheckoutCompleted(ctx context.Context, repo tenants.Repository, event stripe.Event) error {
	var cs stripe.CheckoutSession
	if err := decodeEvent(event, &cs); err != nil {
		return err
	}
	customerID := customerIDOf(cs.Customer)
	if cs.Mode != stripe.CheckoutSessionModeSubscription || customerID == "" || cs.Subscription == nil {
		return nil
	}

	tenant, err := checkoutTenant(ctx, repo, cs.ClientReferenceID, customerID)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if tenant.StripeCustomerID != customerID {
		_, err = repo.SetStripeCustomer(ctx, tenant.ID, customerID)
		// The customer belongs to another tenant: retrying cannot succeed.
		if isConflict(err) {
			slog.WarnContext(ctx, "stripe checkout customer linked to another tenant",
				"event_id", event.ID, "tenant_id", tenant.ID, "customer_id", customerID)
			return nil
		}
		if err != nil {
			return err
		}
	}
	_, err = repo.LinkSubscription(ctx, customerID, cs.Subscription.ID)
	return err
}

// checkoutTenant returns the tenant whose ID is clientRef, or the one linked
// to customerID when clientRef is not a tenant ID.
func checkoutTenant(ctx context.Context, repo tenants.Repository, clientRef, customerID string) (tenants.Tenant, error) {
	if id, err := strconv.ParseInt(clientRef, 10, 32); err == nil {
		return repo.GetByID(ctx, int32(id))
	}
	return repo.GetByStripeCustomerID(ctx, customerID)
}

// handleSubscription stores the subscription state and derived plan on the
// tenant owning the subscription's customer. A deleted subscription is
// canceled. Events about another subscription than the tenant's current one
// are ignored unless they grant pro, so a stale subscription ending does not
// downgrade a tenant that resubscribed. Events about a subscription already
// ended, or older than the last one applied, are ignored too, so a late
// retry cannot revive a canceled subscription.
func handleSubscription(ctx context.Context, repo tenants.Repository, event stripe.Event) error {
	var sub stripe.Subscription
	if err := decodeEvent(event, &sub); err != nil {
		return err
	}
	if event.Type == stripe.EventTypeCustomerSubscriptionDeleted {
		sub.Status = stripe.SubscriptionStatusCanceled
	}
	tenant, err := repo.GetByStripeCustomerID(ctx, customerIDOf(sub.Customer))
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	plan := PlanForStatus(sub.Status, tenant.Plan)
	if tenant.StripeSubscriptionID != "" && tenant.StripeSubscriptionID != sub.ID && plan != tenants.PlanPro {
		return nil
	}
	if tenant.StripeSubscriptionID == sub.ID && IsEndedStatus(stripe.SubscriptionStatus(tenant.SubscriptionStatus)) {
		return nil
	}
	_, err = repo.UpdateSubscription(ctx, tenants.SubscriptionUpdate{
		CustomerID:       tenant.StripeCustomerID,
		SubscriptionID:   sub.ID,
		Status:           string(sub.Status),
		CurrentPeriodEnd: currentPeriodEnd(&sub),
		Plan:             plan,
		EventAt:          eventTime(event),
	})
	if isNotFound(err) {
		return nil
	}
	return err
}

// handlePaymentFailed marks the tenant's subscription past_due, keeping its
// plan; terminal statuses are left to customer.subscription.updated, as
// Stripe may keep the subscription past_due after the last retry. Invoices
// of another subscription than the tenant's current one, or of one already
// ended, are ignored.
func handlePaymentFailed(ctx context.Context, repo tenants.Repository, event stripe.Event) error {
	var inv stripe.Invoice
	if err := decodeEvent(event, &inv); err != nil {
		return err
	}
	if inv.Parent == nil || inv.Parent.SubscriptionDetails == nil || inv.Parent.SubscriptionDetails.Subscription == nil {
		return nil
	}
	subID := inv.Parent.SubscriptionDetails.Subscription.ID
	tenant, err := repo.GetByStripeCustomerID(ctx, customerIDOf(inv.Customer))
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if tenant.StripeSubscriptionID != "" && tenant.StripeSubscriptionID != subID {
		return nil
	}
	if IsEndedStatus(stripe.SubscriptionStatus(tenant.SubscriptionStatus)) {
		return nil
	}
	status := stripe.SubscriptionStatusPastDue
	_, err = repo.UpdateSubscription(ctx, tenants.SubscriptionUpdate{
		CustomerID:       tenant.StripeCustomerID,
		SubscriptionID:   subID,
		Status:           string(status),
		CurrentPeriodEnd: tenant.CurrentPeriodEnd,
		Plan:             PlanForStatus(status, tenant.Plan),
		EventAt:          eventTime(event),
	})
	if isNotFound(err) {
		return nil
	}
	return err
}

// eventTime returns when Stripe created event, in UTC.
func eventTime(event stripe.Event) time.Time {
	return time.Unix(event.Created, 0).UTC()
}

// decodeEvent unmarshals the event's object into v.
func decodeEvent(event stripe.Event, v any) error {
	if event.Data == nil || len(event.Data.Raw) == 0 {
		return apperrors.NewBadRequest(msgInvalidEvent)
	}
	if err := json.Unmarshal(event.Data.Raw, v); err != nil {
		return apperrors.Wrap(err, apperrors.CodeInvalidArgument, msgInvalidEvent)
	}
	return nil
}

// customerIDOf returns the ID of c, or "" when it is nil.
func customerIDOf(c *stripe.Customer) string {
	if c == nil {
		return ""
	}
	return c.ID
}

// currentPeriodEnd returns the latest period end of the subscription's
// items, or nil when none is set.
func currentPeriodEnd(sub *stripe.Subscription) *time.Time {
	var end int64
	if sub.Items != nil {
		for _, item := range sub.Items.Data {
			if item != nil && item.CurrentPeriodEnd > end {
				end = item.CurrentPeriodEnd
			}
		}
	}
	if end == 0 {
		return nil
	}
	t := time.Unix(end, 0).UTC()
	return &t
}

// isNotFound reports whether err is a NotFound AppError.
func isNotFound(err error) bool {
	appErr, ok := apperrors.As(err)
	return ok && appErr.Code == apperrors.CodeNotFound
}

// isConflict reports whether err is a Conflict AppError.
func isConflict(err error) bool {
	appErr, ok := apperrors.As(err)
	return ok && appErr.Code == apperrors.CodeConflict
}
