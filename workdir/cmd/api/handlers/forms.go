package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	apperrors "app/internal/errors"
	"app/internal/pkg/forms"
)

// maxFormTextLen matches the size of the forms.title and forms.slug columns.
const maxFormTextLen = 255

// slugPattern is the accepted shape of a form slug: lowercase words joined by
// single hyphens.
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// FormRequest is the body of POST /forms and PUT /forms/{id}.
type FormRequest struct {
	Title       string  `json:"title" example:"Contact us"`
	Slug        string  `json:"slug" example:"contact-us"`
	Description *string `json:"description" example:"Get in touch with our team"`
	// IsActive defaults to true when omitted.
	IsActive  *bool      `json:"is_active" example:"true"`
	StartDate *time.Time `json:"start_date" example:"2026-01-01T00:00:00Z"`
	EndDate   *time.Time `json:"end_date" example:"2026-12-31T23:59:59Z"`
	// Content is the form definition (fields, layout); any JSON value but null.
	Content json.RawMessage `json:"content" swaggertype:"object"`
	// PublicAvailable forms can be read and filled in without signing in;
	// defaults to true.
	PublicAvailable *bool `json:"public_available" example:"true"`
	// AcceptAnonymous forms store submissions without the submitter's
	// identity; defaults to true and must be true for public forms.
	AcceptAnonymous *bool `json:"accept_anonymous" example:"true"`
	// IsDraft saves the form without publishing it: drafts cannot be read or
	// filled in publicly, and their content may be omitted. Defaults to false.
	IsDraft bool `json:"is_draft" example:"false"`
}

// FormResponse is a form as returned by the API.
type FormResponse struct {
	ID              int32           `json:"id" example:"1"`
	TenantID        int32           `json:"tenant_id" example:"1"`
	Title           string          `json:"title" example:"Contact us"`
	Slug            string          `json:"slug" example:"contact-us"`
	Description     *string         `json:"description" example:"Get in touch with our team"`
	IsActive        bool            `json:"is_active" example:"true"`
	StartDate       *time.Time      `json:"start_date" example:"2026-01-01T00:00:00Z"`
	EndDate         *time.Time      `json:"end_date" example:"2026-12-31T23:59:59Z"`
	Content         json.RawMessage `json:"content" swaggertype:"object"`
	PublicAvailable bool            `json:"public_available" example:"true"`
	AcceptAnonymous bool            `json:"accept_anonymous" example:"true"`
	IsDraft         bool            `json:"is_draft" example:"false"`
	CreatedAt       time.Time       `json:"created_at" example:"2026-01-01T00:00:00Z"`
	UpdatedAt       time.Time       `json:"updated_at" example:"2026-01-01T00:00:00Z"`
}

// FormDetailsResponse is a form together with the name of its tenant.
type FormDetailsResponse struct {
	FormResponse
	TenantName string `json:"tenant_name" example:"Acme Inc."`
}

// FormSummaryResponse is a form as shown in listings.
type FormSummaryResponse struct {
	FormResponse
	SubmissionCount int64 `json:"submission_count" example:"42"`
}

// PublicFormResponse is the subset of a form exposed to the people filling it in.
type PublicFormResponse struct {
	Title       string          `json:"title" example:"Contact us"`
	Slug        string          `json:"slug" example:"contact-us"`
	Description *string         `json:"description" example:"Get in touch with our team"`
	EndDate     *time.Time      `json:"end_date" example:"2026-12-31T23:59:59Z"`
	Content     json.RawMessage `json:"content" swaggertype:"object"`
	// PublicAvailable is false for forms that require signing in.
	PublicAvailable bool `json:"public_available" example:"true"`
	// AcceptAnonymous is false for forms recording who submitted them.
	AcceptAnonymous bool `json:"accept_anonymous" example:"true"`
}

// FormListResponse is a page of forms.
type FormListResponse struct {
	Items []FormSummaryResponse `json:"items"`
	Total int64                 `json:"total" example:"1"`
	PageResponse
}

// formsController serves the form endpoints.
type formsController struct {
	forms forms.Repository
}

// Create creates a form for the authenticated tenant.
//
//	@Summary		Create a form
//	@Description	Creates a form owned by the authenticated tenant. The slug must be unique across all forms. Forms are public and accept anonymous submissions unless public_available or accept_anonymous is false; a public form must accept anonymous submissions. With is_draft the form is saved as a draft: it is not available publicly until it is published by an update with is_draft false, and its content may be omitted. end_date is the deadline after which no submissions are accepted.
//	@Tags			forms
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			body	body		FormRequest					true	"Form to create"
//	@Success		201		{object}	FormResponse				"Form created"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid request"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Missing or invalid API key"
//	@Failure		409		{object}	errors.HTTPErrorResponse	"Slug already exists"
//	@Failure		500		{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/forms [post]
func (c *formsController) Create(w http.ResponseWriter, r *http.Request) {
	req, err := decodeFormRequest(w, r)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	f, err := c.forms.Create(r.Context(), forms.CreateFormInput{
		TenantID:        tenantFrom(r.Context()).ID,
		Title:           req.Title,
		Slug:            req.Slug,
		Description:     req.Description,
		IsActive:        req.IsActive,
		StartDate:       req.StartDate,
		EndDate:         req.EndDate,
		Content:         req.Content,
		PublicAvailable: req.PublicAvailable,
		AcceptAnonymous: req.AcceptAnonymous,
		IsDraft:         req.IsDraft,
	})
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, toFormResponse(f))
}

// List lists the forms of the authenticated tenant.
//
//	@Summary		List forms
//	@Description	Lists the authenticated tenant's forms, newest first, with their submission counts.
//	@Tags			forms
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			is_active	query		bool						false	"Only forms in this state"
//	@Param			is_draft	query		bool						false	"Only drafts (true) or published forms (false)"
//	@Param			search		query		string						false	"Case-insensitive title substring"
//	@Param			offset		query		int							false	"Number of forms to skip"			minimum(0)	default(0)
//	@Param			limit		query		int							false	"Maximum number of forms to return"	minimum(1)	maximum(100)	default(20)
//	@Success		200			{object}	FormListResponse			"Page of forms"
//	@Failure		400			{object}	errors.HTTPErrorResponse	"Invalid query parameters"
//	@Failure		401			{object}	errors.HTTPErrorResponse	"Missing or invalid API key"
//	@Failure		500			{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/forms [get]
func (c *formsController) List(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := parsePage(r)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	filter := forms.ListFormsFilter{TenantID: tenantFrom(r.Context()).ID}
	q := r.URL.Query()
	if v := q.Get("is_active"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("is_active must be a boolean"))
			return
		}
		filter.IsActive = &b
	}
	if v := q.Get("is_draft"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("is_draft must be a boolean"))
			return
		}
		filter.IsDraft = &b
	}
	if v := strings.TrimSpace(q.Get("search")); v != "" {
		filter.Search = &v
	}

	items, err := c.forms.List(r.Context(), filter, forms.Page{Offset: offset, Limit: limit})
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	total, err := c.forms.Count(r.Context(), filter)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	resp := FormListResponse{
		Items:        make([]FormSummaryResponse, len(items)),
		Total:        total,
		PageResponse: PageResponse{Offset: offset, Limit: limit},
	}
	for i, f := range items {
		resp.Items[i] = FormSummaryResponse{FormResponse: toFormResponse(f.Form), SubmissionCount: f.SubmissionCount}
	}
	writeJSON(w, r, http.StatusOK, resp)
}

// Get returns one of the authenticated tenant's forms.
//
//	@Summary		Get a form
//	@Description	Returns one of the authenticated tenant's forms.
//	@Tags			forms
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			id	path		int							true	"Form ID"
//	@Success		200	{object}	FormDetailsResponse			"Form"
//	@Failure		400	{object}	errors.HTTPErrorResponse	"Invalid form ID"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Missing or invalid API key"
//	@Failure		404	{object}	errors.HTTPErrorResponse	"Form not found"
//	@Failure		500	{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/forms/{id} [get]
func (c *formsController) Get(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	f, err := c.forms.GetByID(r.Context(), tenantFrom(r.Context()).ID, id)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, FormDetailsResponse{FormResponse: toFormResponse(f.Form), TenantName: f.TenantName})
}

// Update replaces one of the authenticated tenant's forms.
//
//	@Summary		Update a form
//	@Description	Replaces every field of one of the authenticated tenant's forms. Omitted optional fields are cleared; an omitted is_active, public_available or accept_anonymous is true and an omitted is_draft is false, so saving a draft again needs is_draft true while omitting it publishes the form.
//	@Tags			forms
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			id		path		int							true	"Form ID"
//	@Param			body	body		FormRequest					true	"New state of the form"
//	@Success		200		{object}	FormResponse				"Form updated"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid request"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Missing or invalid API key"
//	@Failure		404		{object}	errors.HTTPErrorResponse	"Form not found"
//	@Failure		409		{object}	errors.HTTPErrorResponse	"Slug already exists"
//	@Failure		500		{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/forms/{id} [put]
func (c *formsController) Update(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	req, err := decodeFormRequest(w, r)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	isActive := req.IsActive == nil || *req.IsActive
	f, err := c.forms.Update(r.Context(), forms.UpdateFormInput{
		ID:              id,
		TenantID:        tenantFrom(r.Context()).ID,
		Title:           req.Title,
		Slug:            req.Slug,
		Description:     req.Description,
		IsActive:        isActive,
		StartDate:       req.StartDate,
		EndDate:         req.EndDate,
		Content:         req.Content,
		PublicAvailable: req.PublicAvailable,
		AcceptAnonymous: req.AcceptAnonymous,
		IsDraft:         req.IsDraft,
	})
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, toFormResponse(f))
}

// Delete deletes one of the authenticated tenant's forms.
//
//	@Summary		Delete a form
//	@Description	Deletes one of the authenticated tenant's forms. Forms that have submissions or webhooks cannot be deleted.
//	@Tags			forms
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			id	path	int	true	"Form ID"
//	@Success		204	"Form deleted"
//	@Failure		400	{object}	errors.HTTPErrorResponse	"Invalid form ID"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Missing or invalid API key"
//	@Failure		404	{object}	errors.HTTPErrorResponse	"Form not found"
//	@Failure		409	{object}	errors.HTTPErrorResponse	"Form has submissions or webhooks"
//	@Failure		500	{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/forms/{id} [delete]
func (c *formsController) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	if err := c.forms.Delete(r.Context(), tenantFrom(r.Context()).ID, id); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetPublic returns a form that can currently be filled in.
//
//	@Summary		Get a form to fill in
//	@Description	Returns the form with the given slug if it is published, active, its tenant is active and now is inside its availability window. Forms past their end_date (deadline) yield 410. Forms that are not public_available require a signed-in user (bearer token or Basic credentials); credentials are optional otherwise.
//	@Tags			public
//	@Produce		json
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			slug	path		string						true	"Form slug"
//	@Success		200		{object}	PublicFormResponse			"Form"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Private form and not signed in, or invalid credentials"
//	@Failure		404		{object}	errors.HTTPErrorResponse	"Form not found or not available"
//	@Failure		410		{object}	errors.HTTPErrorResponse	"Form closed: its end_date has passed"
//	@Failure		500		{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/public/forms/{slug} [get]
func (c *formsController) GetPublic(w http.ResponseWriter, r *http.Request) {
	f, err := availableForm(r, c.forms)
	if err != nil {
		unauthorized(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, PublicFormResponse{
		Title:           f.Title,
		Slug:            f.Slug,
		Description:     f.Description,
		EndDate:         f.EndDate,
		Content:         f.Content,
		PublicAvailable: f.PublicAvailable,
		AcceptAnonymous: f.AcceptAnonymous,
	})
}

// availableForm returns the form of the slug path value if it can be filled
// in by the user of the request, which Guard(auth.Public, ...) resolved.
func availableForm(r *http.Request, repo forms.Repository) (forms.Form, error) {
	f, err := repo.GetPublicBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		return forms.Form{}, err
	}
	if !f.PublicAvailable && userFrom(r.Context()) == nil {
		return forms.Form{}, apperrors.NewUnauthorized("sign in to access this form")
	}
	return f, nil
}

// emptyContent is the content of drafts saved without one.
var emptyContent = json.RawMessage(`{}`)

// decodeFormRequest decodes and validates a FormRequest. Dates are
// normalized to UTC because the database stores them without a time zone.
// Drafts may omit their content, which is then stored as an empty object.
func decodeFormRequest(w http.ResponseWriter, r *http.Request) (FormRequest, error) {
	var req FormRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return FormRequest{}, err
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Slug = strings.TrimSpace(req.Slug)
	if req.IsDraft && (len(req.Content) == 0 || bytes.Equal(req.Content, []byte("null"))) {
		req.Content = emptyContent
	}
	switch {
	case req.Title == "" || utf8.RuneCountInString(req.Title) > maxFormTextLen:
		return FormRequest{}, apperrors.NewBadRequest("title is required and must be at most 255 characters")
	case len(req.Slug) > maxFormTextLen || !slugPattern.MatchString(req.Slug):
		return FormRequest{}, apperrors.NewBadRequest("slug is required, must be at most 255 characters and contain only lowercase letters, digits and single hyphens")
	case len(req.Content) == 0 || bytes.Equal(req.Content, []byte("null")):
		return FormRequest{}, apperrors.NewBadRequest("content is required")
	case req.StartDate != nil && req.EndDate != nil && !req.StartDate.Before(*req.EndDate):
		return FormRequest{}, apperrors.NewBadRequest("start_date must be before end_date")
	}
	req.StartDate = utc(req.StartDate)
	req.EndDate = utc(req.EndDate)
	return req, nil
}

// utc returns t converted to UTC, or nil when t is nil.
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func toFormResponse(f forms.Form) FormResponse {
	return FormResponse{
		ID:              f.ID,
		TenantID:        f.TenantID,
		Title:           f.Title,
		Slug:            f.Slug,
		Description:     f.Description,
		IsActive:        f.IsActive,
		StartDate:       f.StartDate,
		EndDate:         f.EndDate,
		Content:         f.Content,
		PublicAvailable: f.PublicAvailable,
		AcceptAnonymous: f.AcceptAnonymous,
		IsDraft:         f.IsDraft,
		CreatedAt:       f.CreatedAt,
		UpdatedAt:       f.UpdatedAt,
	}
}
