package webhooks

import "context"

// Repository persists form webhooks. It is not scoped by tenant: callers must
// check that the form belongs to the tenant first.
//
// Errors are AppErrors from internal/errors: CodeNotFound when the referenced
// form does not exist and CodeInternal for anything else.
type Repository interface {
	// Create registers a new, active webhook and returns it.
	Create(ctx context.Context, in CreateWebhookInput) (Webhook, error)
	// ListActiveByForm returns the active webhooks of a form, oldest first.
	ListActiveByForm(ctx context.Context, formID int32) ([]Webhook, error)
}
