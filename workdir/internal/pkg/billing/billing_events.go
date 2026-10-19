package billing

import (
	"context"

	"app/internal/db"
	apperrors "app/internal/errors"
)

var _ EventLog = (*EventStore)(nil)

// EventStore implements EventLog on top of the sqlc queries.
type EventStore struct {
	q db.Querier
}

// NewEventStore returns an EventStore that runs its queries through q.
func NewEventStore(q db.Querier) *EventStore {
	return &EventStore{q: q}
}

// MarkProcessed records the event and reports whether it was new.
func (s *EventStore) MarkProcessed(ctx context.Context, id, eventType string) (bool, error) {
	n, err := s.q.InsertStripeEvent(ctx, db.InsertStripeEventParams{ID: id, Type: eventType})
	if err != nil {
		return false, apperrors.NewInternal(err)
	}
	return n > 0, nil
}
