// Package tenants holds the tenant domain: its data types, the Repository
// port used to persist tenants and a Service implementing it on top of sqlc.
package tenants

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Tenant is an account that owns forms and authenticates with an API key.
type Tenant struct {
	ID   int32
	Name string
	// APIKey is a credential; never expose it outside authentication flows.
	APIKey    string
	IsActive  bool
	CreatedAt time.Time
	// OwnerUserID is the user owning this workspace; nil for API-key-only
	// tenants created before accounts existed.
	OwnerUserID *int32
	// Plan is the billing plan the tenant is on.
	Plan Plan
	// StripeCustomerID is the tenant's Stripe customer; empty until the
	// first checkout.
	StripeCustomerID string
	// StripeSubscriptionID is the tenant's current Stripe subscription;
	// empty when it never subscribed.
	StripeSubscriptionID string
	// SubscriptionStatus is the Stripe subscription status (active,
	// past_due, canceled, ...); empty when it never subscribed.
	SubscriptionStatus string
	// CurrentPeriodEnd is when the current billing period ends; nil when
	// unknown.
	CurrentPeriodEnd *time.Time
}

// Plan is a billing plan.
type Plan string

// Billing plans.
const (
	PlanFree Plan = "free"
	PlanPro  Plan = "pro"
)

// SubscriptionUpdate holds the Stripe subscription state to store on the
// tenant owning CustomerID. Empty strings and a nil CurrentPeriodEnd clear
// the matching columns. EventAt is when the provider created the event
// carrying this state; an update about the stored subscription older than
// the last one applied is dropped.
type SubscriptionUpdate struct {
	CustomerID       string
	SubscriptionID   string
	Status           string
	CurrentPeriodEnd *time.Time
	Plan             Plan
	EventAt          time.Time
}

// CreateTenantInput holds the data needed to create a tenant.
type CreateTenantInput struct {
	Name   string
	APIKey string
	// OwnerUserID is the user owning the new tenant; nil for none.
	OwnerUserID *int32
}

// MaxNameLen is the maximum length of a tenant name, in characters; it
// matches the tenants.name column size.
const MaxNameLen = 100

// GenerateAPIKey returns a random 256-bit key, hex encoded.
func GenerateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
