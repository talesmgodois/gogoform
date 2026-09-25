package forms

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"app/internal/database"
	"app/internal/db"
	apperrors "app/internal/errors"
)

// Client-facing messages of the errors returned by Service.
const (
	msgNotFound       = "form not found"
	msgTenantNotFound = "tenant not found"
	msgSlugTaken      = "form slug already exists"
	msgInUse          = "form has submissions or webhooks"
	msgInvalidPage    = "page offset must be >= 0 and limit must be > 0"
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

// Create stores a new form and returns it.
func (s *Service) Create(ctx context.Context, in CreateFormInput) (Form, error) {
	// A nil pointer would insert an explicit NULL rather than the column
	// default, so default to active here.
	isActive := true
	if in.IsActive != nil {
		isActive = *in.IsActive
	}
	row, err := s.q.CreateForm(ctx, db.CreateFormParams{
		TenantID:    in.TenantID,
		Title:       in.Title,
		Slug:        in.Slug,
		Description: in.Description,
		IsActive:    &isActive,
		StartDate:   in.StartDate,
		EndDate:     in.EndDate,
		FormContent: in.Content,
	})
	switch {
	case database.IsUniqueViolation(err):
		return Form{}, apperrors.NewConflict(msgSlugTaken)
	case database.IsForeignKeyViolation(err):
		return Form{}, apperrors.NewNotFound(msgTenantNotFound)
	case err != nil:
		return Form{}, apperrors.NewInternal(err)
	}
	return toForm(row), nil
}

// GetByID returns the tenant's form with the given ID.
func (s *Service) GetByID(ctx context.Context, tenantID, id int32) (FormDetails, error) {
	row, err := s.q.GetFormByID(ctx, db.GetFormByIDParams{ID: id, TenantID: tenantID})
	if err != nil {
		return FormDetails{}, mapGetError(err)
	}
	return FormDetails{
		Form: toForm(db.Form{
			ID:          row.ID,
			TenantID:    row.TenantID,
			Title:       row.Title,
			Slug:        row.Slug,
			Description: row.Description,
			IsActive:    row.IsActive,
			StartDate:   row.StartDate,
			EndDate:     row.EndDate,
			FormContent: row.FormContent,
			CreatedAt:   row.CreatedAt,
			UpdatedAt:   row.UpdatedAt,
		}),
		TenantName: row.TenantName,
	}, nil
}

// GetPublicBySlug returns the form with the given slug if it can be filled in
// publicly. Forms that exist but are not available are reported as not found.
func (s *Service) GetPublicBySlug(ctx context.Context, slug string) (Form, error) {
	row, err := s.q.GetPublicFormBySlug(ctx, slug)
	if err != nil {
		return Form{}, mapGetError(err)
	}
	return toForm(row), nil
}

// List returns the forms matching filter, newest first.
func (s *Service) List(ctx context.Context, filter ListFormsFilter, page Page) ([]FormSummary, error) {
	if page.Offset < 0 || page.Limit < 1 {
		return nil, apperrors.NewBadRequest(msgInvalidPage)
	}
	rows, err := s.q.ListFormsByTenant(ctx, db.ListFormsByTenantParams{
		TenantID:   filter.TenantID,
		IsActive:   filter.IsActive,
		Search:     filter.Search,
		PageOffset: page.Offset,
		PageLimit:  page.Limit,
	})
	if err != nil {
		return nil, apperrors.NewInternal(err)
	}
	forms := make([]FormSummary, len(rows))
	for i, row := range rows {
		forms[i] = FormSummary{
			Form: toForm(db.Form{
				ID:          row.ID,
				TenantID:    row.TenantID,
				Title:       row.Title,
				Slug:        row.Slug,
				Description: row.Description,
				IsActive:    row.IsActive,
				StartDate:   row.StartDate,
				EndDate:     row.EndDate,
				FormContent: row.FormContent,
				CreatedAt:   row.CreatedAt,
				UpdatedAt:   row.UpdatedAt,
			}),
			SubmissionCount: row.SubmissionCount,
		}
	}
	return forms, nil
}

// Count returns how many forms match filter.
func (s *Service) Count(ctx context.Context, filter ListFormsFilter) (int64, error) {
	n, err := s.q.CountFormsByTenant(ctx, db.CountFormsByTenantParams{
		TenantID: filter.TenantID,
		IsActive: filter.IsActive,
		Search:   filter.Search,
	})
	if err != nil {
		return 0, apperrors.NewInternal(err)
	}
	return n, nil
}

// ListAll returns the forms of every tenant, newest first.
func (s *Service) ListAll(ctx context.Context, page Page) ([]FormOverview, error) {
	if page.Offset < 0 || page.Limit < 1 {
		return nil, apperrors.NewBadRequest(msgInvalidPage)
	}
	rows, err := s.q.ListAllForms(ctx, db.ListAllFormsParams{PageOffset: page.Offset, PageLimit: page.Limit})
	if err != nil {
		return nil, apperrors.NewInternal(err)
	}
	forms := make([]FormOverview, len(rows))
	for i, row := range rows {
		forms[i] = FormOverview{
			FormSummary: FormSummary{
				Form: toForm(db.Form{
					ID:          row.ID,
					TenantID:    row.TenantID,
					Title:       row.Title,
					Slug:        row.Slug,
					Description: row.Description,
					IsActive:    row.IsActive,
					StartDate:   row.StartDate,
					EndDate:     row.EndDate,
					FormContent: row.FormContent,
					CreatedAt:   row.CreatedAt,
					UpdatedAt:   row.UpdatedAt,
				}),
				SubmissionCount: row.SubmissionCount,
			},
			TenantName: row.TenantName,
		}
	}
	return forms, nil
}

// CountAll returns how many forms exist across every tenant.
func (s *Service) CountAll(ctx context.Context) (int64, error) {
	n, err := s.q.CountAllForms(ctx)
	if err != nil {
		return 0, apperrors.NewInternal(err)
	}
	return n, nil
}

// Update replaces the stored state of a form and returns it.
func (s *Service) Update(ctx context.Context, in UpdateFormInput) (Form, error) {
	row, err := s.q.UpdateForm(ctx, db.UpdateFormParams{
		ID:          in.ID,
		TenantID:    in.TenantID,
		Title:       in.Title,
		Slug:        in.Slug,
		Description: in.Description,
		IsActive:    &in.IsActive,
		StartDate:   in.StartDate,
		EndDate:     in.EndDate,
		FormContent: in.Content,
	})
	switch {
	case database.IsUniqueViolation(err):
		return Form{}, apperrors.NewConflict(msgSlugTaken)
	case err != nil:
		return Form{}, mapGetError(err)
	}
	return toForm(row), nil
}

// Delete removes the tenant's form with the given ID. Forms that still have
// submissions or webhooks cannot be deleted and yield a conflict.
func (s *Service) Delete(ctx context.Context, tenantID, id int32) error {
	n, err := s.q.DeleteForm(ctx, db.DeleteFormParams{ID: id, TenantID: tenantID})
	switch {
	case database.IsForeignKeyViolation(err):
		return apperrors.NewConflict(msgInUse)
	case err != nil:
		return apperrors.NewInternal(err)
	case n == 0:
		return apperrors.NewNotFound(msgNotFound)
	}
	return nil
}

// mapGetError translates the error of a single-row form query.
func mapGetError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return apperrors.NewNotFound(msgNotFound)
	}
	return apperrors.NewInternal(err)
}

func toForm(row db.Form) Form {
	return Form{
		ID:          row.ID,
		TenantID:    row.TenantID,
		Title:       row.Title,
		Slug:        row.Slug,
		Description: row.Description,
		IsActive:    deref(row.IsActive),
		StartDate:   row.StartDate,
		EndDate:     row.EndDate,
		Content:     row.FormContent,
		CreatedAt:   deref(row.CreatedAt),
		UpdatedAt:   deref(row.UpdatedAt),
	}
}

// deref returns *p, or the zero value when p is nil.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}
