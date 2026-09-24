package webhooks

import (
	"context"

	"app/internal/database"
	"app/internal/db"
	apperrors "app/internal/errors"
)

// msgFormNotFound is the client-facing message when the webhook's form is missing.
const msgFormNotFound = "form not found"

var _ Repository = (*Service)(nil)

// Service implements Repository on top of the sqlc queries.
type Service struct {
	q db.Querier
}

// NewService returns a Service that runs its queries through q.
func NewService(q db.Querier) *Service {
	return &Service{q: q}
}

// Create registers a new, active webhook and returns it.
func (s *Service) Create(ctx context.Context, in CreateWebhookInput) (Webhook, error) {
	row, err := s.q.CreateFormWebhook(ctx, db.CreateFormWebhookParams{
		FormID:      in.FormID,
		TargetUrl:   in.TargetURL,
		SecretToken: in.SecretToken,
	})
	switch {
	case database.IsForeignKeyViolation(err):
		return Webhook{}, apperrors.NewNotFound(msgFormNotFound)
	case err != nil:
		return Webhook{}, apperrors.NewInternal(err)
	}
	return toWebhook(row), nil
}

// ListActiveByForm returns the active webhooks of a form, oldest first. An
// unknown form yields an empty list.
func (s *Service) ListActiveByForm(ctx context.Context, formID int32) ([]Webhook, error) {
	rows, err := s.q.ListActiveWebhooksByForm(ctx, formID)
	if err != nil {
		return nil, apperrors.NewInternal(err)
	}
	hooks := make([]Webhook, len(rows))
	for i, row := range rows {
		hooks[i] = toWebhook(row)
	}
	return hooks, nil
}

func toWebhook(row db.FormWebhook) Webhook {
	return Webhook{
		ID:          row.ID,
		FormID:      row.FormID,
		TargetURL:   row.TargetUrl,
		SecretToken: row.SecretToken,
		IsActive:    deref(row.IsActive),
		CreatedAt:   deref(row.CreatedAt),
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
