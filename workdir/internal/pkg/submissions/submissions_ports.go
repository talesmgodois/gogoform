package submissions

import "context"

// Repository persists form submissions and their metadata.
//
// Errors are AppErrors from internal/errors: CodeNotFound when the referenced
// form or submission does not exist, CodeConflict when a submission already
// has metadata, CodeInvalidArgument for a bad Page and CodeInternal for
// anything else.
type Repository interface {
	// Create stores a submission and returns it, without metadata.
	Create(ctx context.Context, in CreateSubmissionInput) (Submission, error)
	// AddMetadata records the client metadata of a submission. A submission
	// has at most one set of metadata.
	AddMetadata(ctx context.Context, submissionID int32, m Metadata) (Metadata, error)
	// ListByForm returns the submissions of the tenant's form, newest first,
	// with their metadata. A form that does not belong to the tenant yields an
	// empty list.
	ListByForm(ctx context.Context, filter ListSubmissionsFilter, page Page) ([]Submission, error)
}
