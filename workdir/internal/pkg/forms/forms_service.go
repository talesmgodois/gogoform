package forms

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

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
	msgPublicNotAnon  = "a public form must accept anonymous submissions"
	msgClosed         = "form is closed: its end_date has passed"
	msgLocked         = "form has submissions: its content cannot change and it cannot become a draft again; duplicate it instead"
	msgInvalidContent = "form content must be valid JSON"
)

var _ Repository = (*Service)(nil)

// Service implements Repository on top of the sqlc queries.
type Service struct {
	q db.Querier
	// now tells the time the availability window is checked against.
	now func() time.Time
}

// NewService returns a Service that runs its queries through q.
func NewService(q db.Querier) *Service {
	return &Service{q: q, now: time.Now}
}

// Create stores a new form and returns it.
func (s *Service) Create(ctx context.Context, in CreateFormInput) (Form, error) {
	// A nil pointer would insert an explicit NULL rather than the column
	// default, so default to active here.
	isActive := true
	if in.IsActive != nil {
		isActive = *in.IsActive
	}
	public, anonymous, err := resolveAccess(in.PublicAvailable, in.AcceptAnonymous)
	if err != nil {
		return Form{}, err
	}
	content, err := canonicalizeJSON(in.Content)
	if err != nil {
		return Form{}, err
	}
	row, err := s.q.CreateForm(ctx, db.CreateFormParams{
		TenantID:        in.TenantID,
		Title:           in.Title,
		Slug:            in.Slug,
		Description:     in.Description,
		IsActive:        &isActive,
		StartDate:       in.StartDate,
		EndDate:         in.EndDate,
		FormContent:     content,
		PublicAvailable: public,
		AcceptAnonymous: anonymous,
		IsDraft:         in.IsDraft,
	})
	switch {
	case database.IsUniqueViolation(err):
		return Form{}, apperrors.NewConflict(msgSlugTaken)
	case database.IsCheckViolation(err):
		return Form{}, apperrors.NewBadRequest(msgPublicNotAnon)
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
			ID:              row.ID,
			TenantID:        row.TenantID,
			Title:           row.Title,
			Slug:            row.Slug,
			Description:     row.Description,
			IsActive:        row.IsActive,
			StartDate:       row.StartDate,
			EndDate:         row.EndDate,
			FormContent:     row.FormContent,
			CreatedAt:       row.CreatedAt,
			UpdatedAt:       row.UpdatedAt,
			PublicAvailable: row.PublicAvailable,
			AcceptAnonymous: row.AcceptAnonymous,
			IsDraft:         row.IsDraft,
		}),
		TenantName: row.TenantName,
	}, nil
}

// GetPublicBySlug returns the form with the given slug if it can be filled in
// publicly. Forms past their end_date are reported as gone; any other form
// that exists but is not available (inactive, draft, not open yet) as not
// found.
func (s *Service) GetPublicBySlug(ctx context.Context, slug string) (Form, error) {
	row, err := s.q.GetPublicFormBySlug(ctx, slug)
	if err != nil {
		return Form{}, mapGetError(err)
	}
	f := toForm(row)
	now := s.now()
	switch {
	case f.StartDate != nil && now.Before(*f.StartDate):
		return Form{}, apperrors.NewNotFound(msgNotFound)
	case f.EndDate != nil && !now.Before(*f.EndDate):
		return Form{}, apperrors.NewGone(msgClosed)
	}
	return f, nil
}

// List returns the forms matching filter, newest first.
func (s *Service) List(ctx context.Context, filter ListFormsFilter, page Page) ([]FormSummary, error) {
	if page.Offset < 0 || page.Limit < 1 {
		return nil, apperrors.NewBadRequest(msgInvalidPage)
	}
	rows, err := s.q.ListFormsByTenant(ctx, db.ListFormsByTenantParams{
		TenantID:   filter.TenantID,
		IsActive:   filter.IsActive,
		IsDraft:    filter.IsDraft,
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
				ID:              row.ID,
				TenantID:        row.TenantID,
				Title:           row.Title,
				Slug:            row.Slug,
				Description:     row.Description,
				IsActive:        row.IsActive,
				StartDate:       row.StartDate,
				EndDate:         row.EndDate,
				FormContent:     row.FormContent,
				CreatedAt:       row.CreatedAt,
				UpdatedAt:       row.UpdatedAt,
				PublicAvailable: row.PublicAvailable,
				AcceptAnonymous: row.AcceptAnonymous,
				IsDraft:         row.IsDraft,
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
		IsDraft:  filter.IsDraft,
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
					ID:              row.ID,
					TenantID:        row.TenantID,
					Title:           row.Title,
					Slug:            row.Slug,
					Description:     row.Description,
					IsActive:        row.IsActive,
					StartDate:       row.StartDate,
					EndDate:         row.EndDate,
					FormContent:     row.FormContent,
					CreatedAt:       row.CreatedAt,
					UpdatedAt:       row.UpdatedAt,
					PublicAvailable: row.PublicAvailable,
					AcceptAnonymous: row.AcceptAnonymous,
					IsDraft:         row.IsDraft,
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

// GetAnyByID returns the form with the given ID whatever its tenant.
func (s *Service) GetAnyByID(ctx context.Context, id int32) (FormOverview, error) {
	row, err := s.q.GetAnyFormByID(ctx, id)
	if err != nil {
		return FormOverview{}, mapGetError(err)
	}
	return FormOverview{
		FormSummary: FormSummary{
			Form: toForm(db.Form{
				ID:              row.ID,
				TenantID:        row.TenantID,
				Title:           row.Title,
				Slug:            row.Slug,
				Description:     row.Description,
				IsActive:        row.IsActive,
				StartDate:       row.StartDate,
				EndDate:         row.EndDate,
				FormContent:     row.FormContent,
				CreatedAt:       row.CreatedAt,
				UpdatedAt:       row.UpdatedAt,
				PublicAvailable: row.PublicAvailable,
				AcceptAnonymous: row.AcceptAnonymous,
				IsDraft:         row.IsDraft,
			}),
			SubmissionCount: row.SubmissionCount,
		},
		TenantName: row.TenantName,
	}, nil
}

// Update replaces the stored state of a form and returns it. Once a form has
// submissions, changing its content or turning it back into a draft yields a
// conflict.
func (s *Service) Update(ctx context.Context, in UpdateFormInput) (Form, error) {
	public, anonymous, err := resolveAccess(in.PublicAvailable, in.AcceptAnonymous)
	if err != nil {
		return Form{}, err
	}
	content, err := canonicalizeJSON(in.Content)
	if err != nil {
		return Form{}, err
	}
	row, err := s.q.UpdateForm(ctx, db.UpdateFormParams{
		ID:              in.ID,
		TenantID:        in.TenantID,
		Title:           in.Title,
		Slug:            in.Slug,
		Description:     in.Description,
		IsActive:        &in.IsActive,
		StartDate:       in.StartDate,
		EndDate:         in.EndDate,
		FormContent:     content,
		PublicAvailable: public,
		AcceptAnonymous: anonymous,
		IsDraft:         in.IsDraft,
	})
	switch {
	case database.IsUniqueViolation(err):
		return Form{}, apperrors.NewConflict(msgSlugTaken)
	case database.IsCheckViolation(err):
		return Form{}, apperrors.NewBadRequest(msgPublicNotAnon)
	case database.IsNoRows(err):
		// No row is updated both when the form does not exist and when its
		// submissions lock the change: tell them apart.
		if _, getErr := s.GetByID(ctx, in.TenantID, in.ID); getErr != nil {
			return Form{}, getErr
		}
		return Form{}, apperrors.NewConflict(msgLocked)
	case err != nil:
		return Form{}, apperrors.NewInternal(err)
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

// resolveAccess applies the defaults of the access flags: forms are public
// and accept anonymous submissions unless told otherwise. A public form must
// accept anonymous submissions, which the forms_public_accepts_anonymous
// constraint enforces as well.
func resolveAccess(publicAvailable, acceptAnonymous *bool) (public, anonymous bool, err error) {
	public, anonymous = true, true
	if publicAvailable != nil {
		public = *publicAvailable
	}
	if acceptAnonymous != nil {
		anonymous = *acceptAnonymous
	}
	if public && !anonymous {
		return false, false, apperrors.NewBadRequest(msgPublicNotAnon)
	}
	return public, anonymous, nil
}

// canonicalizeJSON re-marshals raw into a compact form with object keys
// sorted and numbers preserved, so semantically identical content compares
// byte for byte regardless of key order or whitespace across engines.
func canonicalizeJSON(raw json.RawMessage) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, apperrors.NewBadRequest(msgInvalidContent)
	}
	canon, err := json.Marshal(v)
	if err != nil {
		return nil, apperrors.NewInternal(err)
	}
	return canon, nil
}

// mapGetError translates the error of a single-row form query.
func mapGetError(err error) error {
	if database.IsNoRows(err) {
		return apperrors.NewNotFound(msgNotFound)
	}
	return apperrors.NewInternal(err)
}

func toForm(row db.Form) Form {
	return Form{
		ID:              row.ID,
		TenantID:        row.TenantID,
		Title:           row.Title,
		Slug:            row.Slug,
		Description:     row.Description,
		IsActive:        deref(row.IsActive),
		StartDate:       row.StartDate,
		EndDate:         row.EndDate,
		Content:         row.FormContent,
		CreatedAt:       deref(row.CreatedAt),
		UpdatedAt:       deref(row.UpdatedAt),
		PublicAvailable: row.PublicAvailable,
		AcceptAnonymous: row.AcceptAnonymous,
		IsDraft:         row.IsDraft,
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
