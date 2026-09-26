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
	// in publicly: the form is published (not a draft), it and its tenant are
	// active and now is inside the availability window. A form past its
	// end_date yields CodeGone; any other unavailable form CodeNotFound.
	GetPublicBySlug(ctx context.Context, slug string) (Form, error)
	// List returns the forms matching filter, newest first.
	List(ctx context.Context, filter ListFormsFilter, page Page) ([]FormSummary, error)
	// Count returns how many forms match filter, for pagination totals.
	Count(ctx context.Context, filter ListFormsFilter) (int64, error)
	// ListAll returns the forms of every tenant, newest first. It is not
	// scoped by tenant: only use it behind operator-level access.
	ListAll(ctx context.Context, page Page) ([]FormOverview, error)
	// CountAll returns how many forms exist across every tenant.
	CountAll(ctx context.Context) (int64, error)
	// GetAnyByID returns the form with the given ID whatever its tenant. It
	// is not scoped by tenant: only use it behind operator-level access.
	GetAnyByID(ctx context.Context, id int32) (FormOverview, error)
	// Update replaces the stored state of a form and returns it. Once the
	// form has submissions its content is locked and it cannot become a draft
	// again: such changes yield CodeConflict.
	Update(ctx context.Context, in UpdateFormInput) (Form, error)
	// Delete removes the tenant's form with the given ID.
	Delete(ctx context.Context, tenantID, id int32) error
}
