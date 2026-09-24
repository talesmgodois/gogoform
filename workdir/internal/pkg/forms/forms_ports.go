package forms

import "context"

// Repository persists forms. Every tenant-facing method is scoped by tenant,
// so a tenant can never read or change another tenant's forms.
//
// Errors are AppErrors from internal/errors: CodeNotFound when the form does
// not exist, CodeConflict when a slug is taken, CodeInvalidArgument for a bad
// Page and CodeInternal for anything else.
type Repository interface {
	// Create stores a new form and returns it.
	Create(ctx context.Context, in CreateFormInput) (Form, error)
	// GetByID returns the tenant's form with the given ID.
	GetByID(ctx context.Context, tenantID, id int32) (FormDetails, error)
	// GetPublicBySlug returns the form with the given slug if it can be filled
	// in publicly: the form and its tenant are active and now is inside the
	// availability window.
	GetPublicBySlug(ctx context.Context, slug string) (Form, error)
	// List returns the forms matching filter, newest first.
	List(ctx context.Context, filter ListFormsFilter, page Page) ([]FormSummary, error)
	// Count returns how many forms match filter, for pagination totals.
	Count(ctx context.Context, filter ListFormsFilter) (int64, error)
	// Update replaces the stored state of a form and returns it.
	Update(ctx context.Context, in UpdateFormInput) (Form, error)
	// Delete removes the tenant's form with the given ID.
	Delete(ctx context.Context, tenantID, id int32) error
}
