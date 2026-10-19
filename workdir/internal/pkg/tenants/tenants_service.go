package tenants

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"app/internal/database"
	"app/internal/db"
	apperrors "app/internal/errors"
)

// Client-facing messages of the errors returned by Service.
const (
	msgNotFound   = "tenant not found"
	msgAPIKeyUsed = "api key already in use"
	msgOwnerTaken = "user already has a workspace"
	msgCustomer   = "stripe customer already linked to another workspace"
)

// tenantsOwnerIndex is the unique index allowing one tenant per owner.
const tenantsOwnerIndex = "tenants_owner_user_id_key"

var _ Repository = (*Service)(nil)

// Service implements Repository on top of the sqlc queries.
type Service struct {
	q db.Querier
}

// NewService returns a Service that runs its queries through q.
func NewService(q db.Querier) *Service {
	return &Service{q: q}
}

// Create stores a new, active tenant and returns it.
func (s *Service) Create(ctx context.Context, in CreateTenantInput) (Tenant, error) {
	row, err := s.q.CreateTenant(ctx, db.CreateTenantParams{
		Name:        in.Name,
		ApiKey:      in.APIKey,
		OwnerUserID: in.OwnerUserID,
	})
	switch {
	case database.IsUniqueViolationOf(err, tenantsOwnerIndex):
		return Tenant{}, apperrors.NewConflict(msgOwnerTaken)
	case database.IsUniqueViolation(err):
		return Tenant{}, apperrors.NewConflict(msgAPIKeyUsed)
	case err != nil:
		return Tenant{}, apperrors.NewInternal(err)
	}
	return toTenant(row), nil
}

// GetByAPIKey returns the active tenant owning apiKey.
func (s *Service) GetByAPIKey(ctx context.Context, apiKey string) (Tenant, error) {
	return toTenantResult(s.q.GetTenantByAPIKey(ctx, apiKey))
}

// GetByID returns the active tenant with the given ID.
func (s *Service) GetByID(ctx context.Context, id int32) (Tenant, error) {
	return toTenantResult(s.q.GetTenantByID(ctx, id))
}

// GetByOwner returns the active tenant owned by userID.
func (s *Service) GetByOwner(ctx context.Context, userID int32) (Tenant, error) {
	return toTenantResult(s.q.GetTenantByOwner(ctx, userID))
}

// RotateAPIKey replaces the API key of the active tenant tenantID.
func (s *Service) RotateAPIKey(ctx context.Context, tenantID int32, newKey string) (Tenant, error) {
	row, err := s.q.RotateTenantAPIKey(ctx, db.RotateTenantAPIKeyParams{ID: tenantID, NewKey: newKey})
	if database.IsUniqueViolation(err) {
		return Tenant{}, apperrors.NewConflict(msgAPIKeyUsed)
	}
	return toTenantResult(row, err)
}

// GetByStripeCustomerID returns the active tenant linked to customerID.
func (s *Service) GetByStripeCustomerID(ctx context.Context, customerID string) (Tenant, error) {
	return toTenantResult(s.q.GetTenantByStripeCustomerID(ctx, customerID))
}

// SetStripeCustomer links the active tenant tenantID to customerID.
func (s *Service) SetStripeCustomer(ctx context.Context, tenantID int32, customerID string) (Tenant, error) {
	row, err := s.q.SetTenantStripeCustomer(ctx, db.SetTenantStripeCustomerParams{ID: tenantID, StripeCustomerID: customerID})
	if database.IsUniqueViolation(err) {
		return Tenant{}, apperrors.NewConflict(msgCustomer)
	}
	return toTenantResult(row, err)
}

// LinkSubscription stores subscriptionID on the tenant linked to customerID.
func (s *Service) LinkSubscription(ctx context.Context, customerID, subscriptionID string) (Tenant, error) {
	return toTenantResult(s.q.LinkTenantSubscription(ctx, db.LinkTenantSubscriptionParams{
		StripeCustomerID:     customerID,
		StripeSubscriptionID: subscriptionID,
	}))
}

// UpdateSubscription stores the subscription state on the tenant linked to
// in.CustomerID, unless it is older than the last update applied.
func (s *Service) UpdateSubscription(ctx context.Context, in SubscriptionUpdate) (Tenant, error) {
	return toTenantResult(s.q.UpdateTenantSubscriptionByCustomer(ctx, db.UpdateTenantSubscriptionByCustomerParams{
		StripeCustomerID:     in.CustomerID,
		StripeSubscriptionID: nilIfEmpty(in.SubscriptionID),
		SubscriptionStatus:   nilIfEmpty(in.Status),
		CurrentPeriodEnd:     in.CurrentPeriodEnd,
		EventAt:              in.EventAt,
		Plan:                 db.TenantPlan(in.Plan),
	}))
}

// toTenantResult maps a single-row query result, turning pgx.ErrNoRows into
// NotFound and any other error into Internal.
func toTenantResult(row db.Tenant, err error) (Tenant, error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return Tenant{}, apperrors.NewNotFound(msgNotFound)
	case err != nil:
		return Tenant{}, apperrors.NewInternal(err)
	}
	return toTenant(row), nil
}

func toTenant(row db.Tenant) Tenant {
	return Tenant{
		ID:                   row.ID,
		Name:                 row.Name,
		APIKey:               row.ApiKey,
		IsActive:             deref(row.IsActive),
		CreatedAt:            deref(row.CreatedAt),
		OwnerUserID:          row.OwnerUserID,
		Plan:                 Plan(row.Plan),
		StripeCustomerID:     deref(row.StripeCustomerID),
		StripeSubscriptionID: deref(row.StripeSubscriptionID),
		SubscriptionStatus:   deref(row.SubscriptionStatus),
		CurrentPeriodEnd:     row.CurrentPeriodEnd,
	}
}

// nilIfEmpty returns nil for an empty s, or a pointer to it.
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// deref returns *p, or the zero value when p is nil.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
