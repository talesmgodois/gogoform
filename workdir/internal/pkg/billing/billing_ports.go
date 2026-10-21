// Package billing manages tenant subscriptions through Stripe.
package billing

import (
	"context"

	"app/internal/pkg/auth"
)

// Gateway talks to the payment provider.
//
// Errors are AppErrors from internal/errors with CodeInternal; the provider's
// error is wrapped for logging and never shown to clients.
type Gateway interface {
	// CreateCustomer registers a customer for the tenant tenantID and returns
	// its provider ID.
	CreateCustomer(ctx context.Context, email, name string, tenantID int32) (string, error)
	// CreateCheckoutSession starts a subscription checkout for customerID on
	// priceID and returns the URL to redirect the user to. The provider sends
	// the user back to successURL or cancelURL.
	CreateCheckoutSession(ctx context.Context, customerID, priceID, successURL, cancelURL string, tenantID int32) (string, error)
	// CreatePortalSession opens a self-service portal session for customerID
	// and returns its URL. The portal links back to returnURL.
	CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, error)
	// CancelSubscription cancels subscriptionID immediately, without
	// proration. A subscription the provider no longer knows is not an error.
	CancelSubscription(ctx context.Context, subscriptionID string) error
}

// EventLog records processed webhook events so a redelivered event is
// handled once.
//
// Errors are AppErrors from internal/errors with CodeInternal.
type EventLog interface {
	// MarkProcessed records the event id of type eventType and reports
	// whether this is the first time it was seen; false means the event was
	// already processed and must be skipped.
	MarkProcessed(ctx context.Context, id, eventType string) (firstTime bool, err error)
}

// Users looks up the user owning a tenant, to notify them of billing events.
//
// Errors are AppErrors from internal/errors with CodeNotFound when no such
// user exists.
type Users interface {
	// GetByID returns the active user with the given ID.
	GetByID(ctx context.Context, id int32) (auth.User, error)
}

// Notifier sends the transactional emails issued by the billing webhooks.
// Sending is expected to be asynchronous (see internal/pkg/mailer.Async), so
// a slow or failing delivery never blocks event processing.
type Notifier interface {
	// SendPaymentFailed notifies owner that invoice.payment_failed left
	// their subscription past_due.
	SendPaymentFailed(ctx context.Context, owner auth.User) error
}
