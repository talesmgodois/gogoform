package tenants

import "context"

// Repository persists tenants.
//
// Errors are AppErrors from internal/errors: CodeNotFound when no matching
// tenant exists, CodeConflict when an API key or Stripe customer is taken or
// the owner already has a tenant, and CodeInternal for anything else.
type Repository interface {
	// Create stores a new, active tenant and returns it.
	Create(ctx context.Context, in CreateTenantInput) (Tenant, error)
	// GetByAPIKey returns the active tenant owning apiKey. Inactive tenants
	// are reported as not found.
	GetByAPIKey(ctx context.Context, apiKey string) (Tenant, error)
	// GetByID returns the active tenant with the given ID. Inactive tenants
	// are reported as not found.
	GetByID(ctx context.Context, id int32) (Tenant, error)
	// GetByOwner returns the active tenant owned by userID. Inactive tenants
	// are reported as not found.
	GetByOwner(ctx context.Context, userID int32) (Tenant, error)
	// RotateAPIKey replaces the API key of the active tenant tenantID with
	// newKey and returns the updated tenant; the old key stops working at once.
	RotateAPIKey(ctx context.Context, tenantID int32, newKey string) (Tenant, error)
	// GetByStripeCustomerID returns the active tenant linked to the Stripe
	// customer customerID. Inactive tenants are reported as not found.
	GetByStripeCustomerID(ctx context.Context, customerID string) (Tenant, error)
	// SetStripeCustomer links the active tenant tenantID to the Stripe
	// customer customerID and returns the updated tenant.
	SetStripeCustomer(ctx context.Context, tenantID int32, customerID string) (Tenant, error)
	// LinkSubscription stores subscriptionID as the subscription of the
	// active tenant linked to customerID and returns the updated tenant. The
	// stored status and period end are kept when subscriptionID is already
	// the tenant's subscription and cleared otherwise; the plan is unchanged.
	LinkSubscription(ctx context.Context, customerID, subscriptionID string) (Tenant, error)
	// UpdateSubscription stores the subscription state and plan on the active
	// tenant linked to in.CustomerID and returns the updated tenant. It
	// returns NotFound as well when in is about the stored subscription and
	// older than the last update applied to it.
	UpdateSubscription(ctx context.Context, in SubscriptionUpdate) (Tenant, error)
}
