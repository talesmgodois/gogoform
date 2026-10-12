// Package customcomponents holds the reusable field components domain: field
// definitions saved from the form builder so they can be reinserted into
// other forms.
package customcomponents

import (
	"context"
	"encoding/json"
	"time"
)

// CustomComponent is a field definition saved for reuse.
type CustomComponent struct {
	ID int32
	// UserID is the user who saved the component.
	UserID    int32
	Name      string
	FieldJSON json.RawMessage
	CreatedAt time.Time
}

// CreateCustomComponentInput holds the data needed to save a component.
type CreateCustomComponentInput struct {
	UserID    int32
	Name      string
	FieldJSON json.RawMessage
}

// Repository persists reusable field components.
//
// Errors are AppErrors from internal/errors: CodeInternal for anything else.
type Repository interface {
	// Create saves a new reusable component.
	Create(ctx context.Context, in CreateCustomComponentInput) (CustomComponent, error)
	// List returns every saved component, across every user, newest first.
	List(ctx context.Context) ([]CustomComponent, error)
}
