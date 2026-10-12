package customcomponents

import (
	"context"

	"app/internal/db"
	apperrors "app/internal/errors"
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

// Create saves a new reusable component.
func (s *Service) Create(ctx context.Context, in CreateCustomComponentInput) (CustomComponent, error) {
	row, err := s.q.CreateCustomComponent(ctx, db.CreateCustomComponentParams{
		UserID:    in.UserID,
		Name:      in.Name,
		FieldJson: in.FieldJSON,
	})
	if err != nil {
		return CustomComponent{}, apperrors.NewInternal(err)
	}
	return toCustomComponent(row), nil
}

// List returns every saved component, across every user, newest first.
func (s *Service) List(ctx context.Context) ([]CustomComponent, error) {
	rows, err := s.q.ListCustomComponents(ctx)
	if err != nil {
		return nil, apperrors.NewInternal(err)
	}
	components := make([]CustomComponent, len(rows))
	for i, row := range rows {
		components[i] = toCustomComponent(row)
	}
	return components, nil
}

func toCustomComponent(row db.CustomComponent) CustomComponent {
	return CustomComponent{
		ID:        row.ID,
		UserID:    row.UserID,
		Name:      row.Name,
		FieldJSON: row.FieldJson,
		CreatedAt: row.CreatedAt,
	}
}
