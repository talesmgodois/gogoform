package accounts

import (
	"context"

	"app/internal/db"
	apperrors "app/internal/errors"
)

var _ Store = (*SQLStore)(nil)

// SQLStore implements Store on top of the sqlc queries.
type SQLStore struct {
	q db.Querier
}

// NewStore returns a SQLStore that runs its queries through q.
func NewStore(q db.Querier) *SQLStore {
	return &SQLStore{q: q}
}

// DeleteTenantData deletes the tenant's rows child first: the foreign keys
// of the tenant data have no ON DELETE CASCADE.
func (s *SQLStore) DeleteTenantData(ctx context.Context, tenantID int32) error {
	steps := []func(context.Context, int32) error{
		s.q.DeleteSubmissionMetadataByTenant,
		s.q.DeleteSubmissionsByTenant,
		s.q.DeleteWebhooksByTenant,
		s.q.DeleteFormsByTenant,
		s.q.DeleteFilesByTenant,
		s.q.DeleteTenant,
	}
	for _, step := range steps {
		if err := step(ctx, tenantID); err != nil {
			return apperrors.NewInternal(err)
		}
	}
	return nil
}

// DeleteUser anonymizes the user's submissions to other tenants' forms and
// deletes the user; user_tokens cascade.
func (s *SQLStore) DeleteUser(ctx context.Context, userID int32) error {
	if err := s.q.ClearSubmissionsUser(ctx, &userID); err != nil {
		return apperrors.NewInternal(err)
	}
	if err := s.q.DeleteUser(ctx, userID); err != nil {
		return apperrors.NewInternal(err)
	}
	return nil
}
