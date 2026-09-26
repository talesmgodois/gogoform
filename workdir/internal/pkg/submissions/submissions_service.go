package submissions

import (
	"context"

	"app/internal/database"
	"app/internal/db"
	apperrors "app/internal/errors"
)

// Client-facing messages of the errors returned by Service.
const (
	msgFormNotFound       = "form not found"
	msgSubmissionNotFound = "submission not found"
	msgMetadataExists     = "submission already has metadata"
	msgInvalidPage        = "page offset must be >= 0 and limit must be > 0"
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

// Create stores a submission and returns it, without metadata.
func (s *Service) Create(ctx context.Context, in CreateSubmissionInput) (Submission, error) {
	row, err := s.q.CreateFormSubmission(ctx, db.CreateFormSubmissionParams{
		FormID:  in.FormID,
		Payload: in.Payload,
		UserID:  in.UserID,
	})
	switch {
	case database.IsForeignKeyViolation(err):
		return Submission{}, apperrors.NewNotFound(msgFormNotFound)
	case err != nil:
		return Submission{}, apperrors.NewInternal(err)
	}
	return Submission{
		ID:          row.ID,
		FormID:      row.FormID,
		Payload:     row.Payload,
		SubmittedAt: deref(row.SubmittedAt),
		UserID:      row.UserID,
	}, nil
}

// AddMetadata records the client metadata of a submission.
func (s *Service) AddMetadata(ctx context.Context, submissionID int32, m Metadata) (Metadata, error) {
	row, err := s.q.CreateSubmissionMetadata(ctx, db.CreateSubmissionMetadataParams{
		SubmissionID:          submissionID,
		IpAddress:             m.IPAddress,
		UserAgent:             m.UserAgent,
		CompletionTimeSeconds: m.CompletionTimeSeconds,
		Referer:               m.Referer,
	})
	switch {
	case database.IsUniqueViolation(err):
		return Metadata{}, apperrors.NewConflict(msgMetadataExists)
	case database.IsForeignKeyViolation(err):
		return Metadata{}, apperrors.NewNotFound(msgSubmissionNotFound)
	case err != nil:
		return Metadata{}, apperrors.NewInternal(err)
	}
	return Metadata{
		IPAddress:             row.IpAddress,
		UserAgent:             row.UserAgent,
		CompletionTimeSeconds: row.CompletionTimeSeconds,
		Referer:               row.Referer,
	}, nil
}

// ListByForm returns the submissions of the tenant's form, newest first.
func (s *Service) ListByForm(ctx context.Context, filter ListSubmissionsFilter, page Page) ([]Submission, error) {
	if page.Offset < 0 || page.Limit < 1 {
		return nil, apperrors.NewBadRequest(msgInvalidPage)
	}
	rows, err := s.q.ListSubmissionsByForm(ctx, db.ListSubmissionsByFormParams{
		FormID:     filter.FormID,
		TenantID:   filter.TenantID,
		PageOffset: page.Offset,
		PageLimit:  page.Limit,
	})
	if err != nil {
		return nil, apperrors.NewInternal(err)
	}
	subs := make([]Submission, len(rows))
	for i, row := range rows {
		subs[i] = toSubmission(row)
	}
	return subs, nil
}

// ListAllByForm returns every submission of the tenant's form, newest first.
func (s *Service) ListAllByForm(ctx context.Context, filter ListSubmissionsFilter) ([]Submission, error) {
	rows, err := s.q.ListAllSubmissionsByForm(ctx, db.ListAllSubmissionsByFormParams{
		FormID:   filter.FormID,
		TenantID: filter.TenantID,
	})
	if err != nil {
		return nil, apperrors.NewInternal(err)
	}
	subs := make([]Submission, len(rows))
	for i, row := range rows {
		subs[i] = toSubmission(db.ListSubmissionsByFormRow(row))
	}
	return subs, nil
}

// CountAll returns how many submissions exist across every tenant.
func (s *Service) CountAll(ctx context.Context) (int64, error) {
	n, err := s.q.CountAllSubmissions(ctx)
	if err != nil {
		return 0, apperrors.NewInternal(err)
	}
	return n, nil
}

// CountAllToday returns how many submissions were made today, across every
// tenant.
func (s *Service) CountAllToday(ctx context.Context) (int64, error) {
	n, err := s.q.CountAllSubmissionsToday(ctx)
	if err != nil {
		return 0, apperrors.NewInternal(err)
	}
	return n, nil
}

// toSubmission maps a submission row LEFT JOINed with its metadata.
// Submissions without metadata have a nil Metadata.
func toSubmission(row db.ListSubmissionsByFormRow) Submission {
	sub := Submission{
		ID:          row.ID,
		FormID:      row.FormID,
		Payload:     row.Payload,
		SubmittedAt: deref(row.SubmittedAt),
		UserID:      row.UserID,
	}
	// All metadata columns NULL means there is none (or none worth reporting).
	if row.IpAddress != nil || row.UserAgent != nil || row.CompletionTimeSeconds != nil || row.Referer != nil {
		sub.Metadata = &Metadata{
			IPAddress:             row.IpAddress,
			UserAgent:             row.UserAgent,
			CompletionTimeSeconds: row.CompletionTimeSeconds,
			Referer:               row.Referer,
		}
	}
	return sub
}

// deref returns *p, or the zero value when p is nil.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
