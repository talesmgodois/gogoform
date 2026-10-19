// Package billing manages tenant subscriptions through Stripe.
package billing

import "context"

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
