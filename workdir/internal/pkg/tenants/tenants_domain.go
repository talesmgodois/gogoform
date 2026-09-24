// Package tenants holds the tenant domain: its data types, the Repository
// port used to persist tenants and a Service implementing it on top of sqlc.
package tenants

import "time"

// Tenant is an account that owns forms and authenticates with an API key.
type Tenant struct {
	ID   int32
	Name string
	// APIKey is a credential; never expose it outside authentication flows.
	APIKey    string
	IsActive  bool
	CreatedAt time.Time
}

// CreateTenantInput holds the data needed to create a tenant.
type CreateTenantInput struct {
	Name   string
	APIKey string
}
