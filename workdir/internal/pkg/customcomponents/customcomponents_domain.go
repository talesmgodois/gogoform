// Package customcomponents holds the reusable field component domain: its
// data types, the Repository port used to persist them, and a Service
// implementing it on top of sqlc.
package customcomponents

import (
	"encoding/json"
	"time"
)

// CustomComponent is a field configuration saved for reuse across forms.
type CustomComponent struct {
	ID   int32
	Name string
	// Field holds the saved field definition as raw JSON.
	Field     json.RawMessage
	CreatedAt time.Time
	// UserID is the user who created it; nil when unknown.
	UserID *int32
}

// CreateCustomComponentInput holds the data needed to save a custom
// component.
type CreateCustomComponentInput struct {
	Name  string
	Field json.RawMessage
	// UserID is the signed-in creator; nil when unknown.
	UserID *int32
}
