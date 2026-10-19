package billing

import (
	"context"
	"strconv"

	"github.com/stripe/stripe-go/v82"

	apperrors "app/internal/errors"
)

// msgProviderError is the client-facing message when a Stripe call fails.
const msgProviderError = "billing provider error"

// metadataTenantID is the Stripe metadata key holding the tenant ID.
const metadataTenantID = "tenant_id"

var _ Gateway = (*StripeGateway)(nil)

// StripeGateway implements Gateway with the Stripe API.
type StripeGateway struct {
	client *stripe.Client
}

// NewStripeGateway returns a StripeGateway authenticated with secretKey.
func NewStripeGateway(secretKey string) *StripeGateway {
	return &StripeGateway{client: stripe.NewClient(secretKey)}
}

// CreateCustomer registers a Stripe customer tagged with the tenant ID.
func (g *StripeGateway) CreateCustomer(ctx context.Context, email, name string, tenantID int32) (string, error) {
	params := &stripe.CustomerCreateParams{
		Email:    stripe.String(email),
		Metadata: tenantMetadata(tenantID),
	}
	if name != "" {
		params.Name = stripe.String(name)
	}
	c, err := g.client.V1Customers.Create(ctx, params)
	if err != nil {
		return "", apperrors.Wrap(err, apperrors.CodeInternal, msgProviderError)
	}
	return c.ID, nil
}

// CreateCheckoutSession creates a subscription Checkout Session for one unit
// of priceID. The tenant ID is sent as client_reference_id and as metadata on
// both the session and the subscription.
func (g *StripeGateway) CreateCheckoutSession(ctx context.Context, customerID, priceID, successURL, cancelURL string, tenantID int32) (string, error) {
	params := &stripe.CheckoutSessionCreateParams{
		Mode:              stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		Customer:          stripe.String(customerID),
		ClientReferenceID: stripe.String(strconv.Itoa(int(tenantID))),
		SuccessURL:        stripe.String(successURL),
		CancelURL:         stripe.String(cancelURL),
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{
			{Price: stripe.String(priceID), Quantity: stripe.Int64(1)},
		},
		Metadata: tenantMetadata(tenantID),
		SubscriptionData: &stripe.CheckoutSessionCreateSubscriptionDataParams{
			Metadata: tenantMetadata(tenantID),
		},
	}
	s, err := g.client.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return "", apperrors.Wrap(err, apperrors.CodeInternal, msgProviderError)
	}
	return s.URL, nil
}

// CreatePortalSession creates a Customer Portal session for customerID.
func (g *StripeGateway) CreatePortalSession(ctx context.Context, customerID, returnURL string) (string, error) {
	s, err := g.client.V1BillingPortalSessions.Create(ctx, &stripe.BillingPortalSessionCreateParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(returnURL),
	})
	if err != nil {
		return "", apperrors.Wrap(err, apperrors.CodeInternal, msgProviderError)
	}
	return s.URL, nil
}

// tenantMetadata returns the Stripe metadata identifying tenantID.
func tenantMetadata(tenantID int32) map[string]string {
	return map[string]string{metadataTenantID: strconv.Itoa(int(tenantID))}
}
