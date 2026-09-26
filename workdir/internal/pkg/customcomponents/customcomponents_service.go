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

// Create stores a custom component and returns it.
func (s *Service) Create(ctx context.Context, in CreateCustomComponentInput) (CustomComponent, error) {
	row, err := s.q.CreateCustomComponent(ctx, db.CreateCustomComponentParams{
		UserID: in.UserID,
		Name:   in.Name,
		Field:  in.Field,
	})
	if err != nil {
		return CustomComponent{}, apperrors.NewInternal(err)
	}
	return toCustomComponent(row), nil
}

// List returns every saved custom component, newest first.
func (s *Service) List(ctx context.Context) ([]CustomComponent, error) {
	rows, err := s.q.ListCustomComponents(ctx)
	if err != nil {
		return nil, apperrors.NewInternal(err)
	}
	items := make([]CustomComponent, len(rows))
	for i, row := range rows {
		items[i] = toCustomComponent(row)
	}
	return items, nil
}

func toCustomComponent(row db.CustomComponent) CustomComponent {
	return CustomComponent{
		ID:        row.ID,
		UserID:    row.UserID,
		Name:      row.Name,
		Field:     row.Field,
		CreatedAt: row.CreatedAt,
	}
}
