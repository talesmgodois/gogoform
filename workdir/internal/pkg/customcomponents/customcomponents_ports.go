package customcomponents

import "context"

// Repository persists reusable custom field components.
//
// Errors are AppErrors from internal/errors: CodeInternal for anything else.
type Repository interface {
	// Create stores a custom component and returns it.
	Create(ctx context.Context, in CreateCustomComponentInput) (CustomComponent, error)
	// List returns every saved custom component, newest first.
	List(ctx context.Context) ([]CustomComponent, error)
}
