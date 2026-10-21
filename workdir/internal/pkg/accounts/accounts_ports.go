// Package accounts closes user accounts: it cancels the workspace's Stripe
// subscription and deletes the user together with the tenant data they own.
package accounts

import (
	"context"

	"app/internal/pkg/tenants"
)

// Store deletes the rows of an account. It runs inside the transaction of
// Service.DeleteAccount.
//
// Errors are AppErrors from internal/errors with CodeInternal.
type Store interface {
	// DeleteTenantData deletes the tenant tenantID with its forms,
	// submissions (and their metadata), webhooks and files.
	DeleteTenantData(ctx context.Context, tenantID int32) error
	// DeleteUser deletes the user userID and their tokens; the submissions
	// they sent to other tenants' forms are kept as anonymous ones.
	DeleteUser(ctx context.Context, userID int32) error
}

// Workspaces finds the workspace a user owns.
type Workspaces interface {
	// GetByOwner returns the active tenant owned by userID; CodeNotFound
	// when there is none.
	GetByOwner(ctx context.Context, userID int32) (tenants.Tenant, error)
}

// Subscriptions cancels the subscription of a workspace with the payment
// provider; implemented by *billing.Service.
type Subscriptions interface {
	// CancelSubscription cancels the tenant's subscription right away; a
	// tenant without an ongoing subscription is not an error.
	CancelSubscription(ctx context.Context, tenant tenants.Tenant) error
}
