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
)

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
	row, err := s.q.CreateTenant(ctx, db.CreateTenantParams{Name: in.Name, ApiKey: in.APIKey})
	switch {
	case database.IsUniqueViolation(err):
		return Tenant{}, apperrors.NewConflict(msgAPIKeyUsed)
	case err != nil:
		return Tenant{}, apperrors.NewInternal(err)
	}
	return toTenant(row), nil
}

// GetByAPIKey returns the active tenant owning apiKey.
func (s *Service) GetByAPIKey(ctx context.Context, apiKey string) (Tenant, error) {
	row, err := s.q.GetTenantByAPIKey(ctx, apiKey)
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
		ID:        row.ID,
		Name:      row.Name,
		APIKey:    row.ApiKey,
		IsActive:  deref(row.IsActive),
		CreatedAt: deref(row.CreatedAt),
	}
}

// deref returns *p, or the zero value when p is nil.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
