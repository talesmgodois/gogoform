package tenants

import "context"

// Repository persists tenants.
//
// Errors are AppErrors from internal/errors: CodeNotFound when no matching
// tenant exists, CodeConflict when an API key is taken and CodeInternal for
// anything else.
type Repository interface {
	// Create stores a new, active tenant and returns it.
	Create(ctx context.Context, in CreateTenantInput) (Tenant, error)
	// GetByAPIKey returns the active tenant owning apiKey. Inactive tenants
	// are reported as not found.
	GetByAPIKey(ctx context.Context, apiKey string) (Tenant, error)
}
