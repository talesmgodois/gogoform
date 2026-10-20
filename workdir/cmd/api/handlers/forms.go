package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	apperrors "app/internal/errors"
	authpkg "app/internal/pkg/auth"
	"app/internal/pkg/forms"
	"app/internal/pkg/plans"
	"app/internal/pkg/tenants"
)

// msgEmailUnverified is shown when a tenant whose owner has an unverified
// email tries to publish (activate) a form.
const msgEmailUnverified = "verify your email address before publishing a form"

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
	// NotifyOnSubmission emails the tenant owner on each new submission;
	// defaults to false. Enabling it requires a plan with email
	// notifications and fails with plan_feature_unavailable otherwise.
	NotifyOnSubmission bool `json:"notify_on_submission" example:"false"`
}

// FormResponse is a form as returned by the API.
type FormResponse struct {
	ID                 int32           `json:"id" example:"1"`
	TenantID           int32           `json:"tenant_id" example:"1"`
	Title              string          `json:"title" example:"Contact us"`
	Slug               string          `json:"slug" example:"contact-us"`
	Description        *string         `json:"description" example:"Get in touch with our team"`
	IsActive           bool            `json:"is_active" example:"true"`
	StartDate          *time.Time      `json:"start_date" example:"2026-01-01T00:00:00Z"`
	EndDate            *time.Time      `json:"end_date" example:"2026-12-31T23:59:59Z"`
	Content            json.RawMessage `json:"content" swaggertype:"object"`
	PublicAvailable    bool            `json:"public_available" example:"true"`
	AcceptAnonymous    bool            `json:"accept_anonymous" example:"true"`
	NotifyOnSubmission bool            `json:"notify_on_submission" example:"false"`
	CreatedAt          time.Time       `json:"created_at" example:"2026-01-01T00:00:00Z"`
	UpdatedAt          time.Time       `json:"updated_at" example:"2026-01-01T00:00:00Z"`
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
	plans *plans.Enforcer
	// auth looks up a tenant's owner to enforce checkEmailVerifiedForPublish.
	auth *authpkg.Service
}

// Create creates a form for the authenticated tenant.
//
//	@Summary		Create a form
//	@Description	Creates a form owned by the authenticated tenant. The slug must be unique across all forms. Forms are public and accept anonymous submissions unless public_available or accept_anonymous is false; a public form must accept anonymous submissions. Fails with plan_limit_reached when the tenant's plan allows no more forms, and plan_feature_unavailable when notify_on_submission is true on a plan without email notifications.
//	@Tags			forms
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			body	body		FormRequest					true	"Form to create"
//	@Success		201		{object}	FormResponse				"Form created"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid request"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Missing or invalid API key or user credentials"
//	@Failure		402		{object}	errors.HTTPErrorResponse	"Plan form limit reached"
//	@Failure		403		{object}	errors.HTTPErrorResponse	"Signed-in user has no workspace, cross-origin request authenticated by the session cookie, the tenant owner's email is not verified and is_active is true, or notify_on_submission is true on a plan without email notifications"
//	@Failure		409		{object}	errors.HTTPErrorResponse	"Slug already exists"
//	@Failure		500		{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/forms [post]
func (c *formsController) Create(w http.ResponseWriter, r *http.Request) {
	req, err := decodeFormRequest(w, r)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	tenant := tenantFrom(r.Context())
	if err := c.plans.CheckFormCreate(r.Context(), tenant.ID, tenant.Plan); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	if err := checkEmailVerifiedForPublish(r.Context(), c.auth, tenant, req.IsActive == nil || *req.IsActive); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	if err := checkNotifyOnSubmissionAllowed(tenant, req.NotifyOnSubmission); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	f, err := c.forms.Create(r.Context(), forms.CreateFormInput{
		TenantID:           tenant.ID,
		Title:              req.Title,
		Slug:               req.Slug,
		Description:        req.Description,
		IsActive:           req.IsActive,
		StartDate:          req.StartDate,
		EndDate:            req.EndDate,
		Content:            req.Content,
		PublicAvailable:    req.PublicAvailable,
		AcceptAnonymous:    req.AcceptAnonymous,
		NotifyOnSubmission: req.NotifyOnSubmission,
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
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			is_active	query		bool						false	"Only forms in this state"
//	@Param			search		query		string						false	"Case-insensitive title substring"
//	@Param			offset		query		int							false	"Number of forms to skip"			minimum(0)	default(0)
//	@Param			limit		query		int							false	"Maximum number of forms to return"	minimum(1)	maximum(100)	default(20)
//	@Success		200			{object}	FormListResponse			"Page of forms"
//	@Failure		400			{object}	errors.HTTPErrorResponse	"Invalid query parameters"
//	@Failure		401			{object}	errors.HTTPErrorResponse	"Missing or invalid API key or user credentials"
//	@Failure		403			{object}	errors.HTTPErrorResponse	"Signed-in user has no workspace"
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
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			id	path		int							true	"Form ID"
//	@Success		200	{object}	FormDetailsResponse			"Form"
//	@Failure		400	{object}	errors.HTTPErrorResponse	"Invalid form ID"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Missing or invalid API key or user credentials"
//	@Failure		403	{object}	errors.HTTPErrorResponse	"Signed-in user has no workspace"
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
//	@Description	Replaces every field of one of the authenticated tenant's forms. Omitted optional fields are cleared; an omitted is_active, public_available or accept_anonymous is true. Fails with plan_feature_unavailable when notify_on_submission is true on a plan without email notifications.
//	@Tags			forms
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			id		path		int							true	"Form ID"
//	@Param			body	body		FormRequest					true	"New state of the form"
//	@Success		200		{object}	FormResponse				"Form updated"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid request"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Missing or invalid API key or user credentials"
//	@Failure		403		{object}	errors.HTTPErrorResponse	"Signed-in user has no workspace, cross-origin request authenticated by the session cookie, the tenant owner's email is not verified and is_active is true, or notify_on_submission is true on a plan without email notifications"
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
	tenant := tenantFrom(r.Context())
	if err := checkEmailVerifiedForPublish(r.Context(), c.auth, tenant, isActive); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	if err := checkNotifyOnSubmissionAllowed(tenant, req.NotifyOnSubmission); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	f, err := c.forms.Update(r.Context(), forms.UpdateFormInput{
		ID:                 id,
		TenantID:           tenant.ID,
		Title:              req.Title,
		Slug:               req.Slug,
		Description:        req.Description,
		IsActive:           isActive,
		StartDate:          req.StartDate,
		EndDate:            req.EndDate,
		Content:            req.Content,
		PublicAvailable:    req.PublicAvailable,
		AcceptAnonymous:    req.AcceptAnonymous,
		NotifyOnSubmission: req.NotifyOnSubmission,
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
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			id	path	int	true	"Form ID"
//	@Success		204	"Form deleted"
//	@Failure		400	{object}	errors.HTTPErrorResponse	"Invalid form ID"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Missing or invalid API key or user credentials"
//	@Failure		403	{object}	errors.HTTPErrorResponse	"Signed-in user has no workspace, or cross-origin request authenticated by the session cookie"
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
//	@Description	Returns the form with the given slug if it is active, its tenant is active and now is inside its availability window; otherwise a 403 tells which. Forms that are not public_available require a signed-in user (bearer token or Basic credentials); credentials are optional otherwise. Cross-origin requests are allowed from any origin (CORS, without cookies).
//	@Tags			public
//	@Produce		json
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			slug	path		string						true	"Form slug"
//	@Success		200		{object}	PublicFormResponse			"Form"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Private form and not signed in, or invalid credentials"
//	@Failure		403		{object}	errors.HTTPErrorResponse	"Form inactive, not open yet or closed"
//	@Failure		404		{object}	errors.HTTPErrorResponse	"Form not found"
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

// availableForm returns the form of the slug path value, along with its
// tenant's state, if it can be filled in by the user of the request, which Guard(auth.Public, ...) resolved.
// Forms that exist but accept no responses get a 403 telling why, as the /f
// page does; inactive forms and forms of inactive tenants share a message.
func availableForm(r *http.Request, repo forms.Repository) (forms.FormBySlug, error) {
	f, err := repo.GetBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		return forms.FormBySlug{}, err
	}
	switch forms.Availability(f.Form, f.TenantActive, time.Now()) {
	case forms.Available:
	case forms.NotStarted:
		return forms.FormBySlug{}, apperrors.NewForbidden("form opens on " + f.StartDate.UTC().Format(time.RFC3339))
	case forms.Ended:
		return forms.FormBySlug{}, apperrors.NewForbidden("form closed on " + f.EndDate.UTC().Format(time.RFC3339))
	default:
		return forms.FormBySlug{}, apperrors.NewForbidden("form is not accepting responses")
	}
	if !f.PublicAvailable && userFrom(r.Context()) == nil {
		return forms.FormBySlug{}, apperrors.NewUnauthorized("sign in to access this form")
	}
	return f, nil
}

// decodeFormRequest decodes and validates a FormRequest. Dates are
// normalized to UTC because the database stores them without a time zone.
func decodeFormRequest(w http.ResponseWriter, r *http.Request) (FormRequest, error) {
	var req FormRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return FormRequest{}, err
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Slug = strings.TrimSpace(req.Slug)
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

// checkEmailVerifiedForPublish rejects saving a form with is_active true for
// a tenant whose owner has an email address that is still unverified.
// API-key tenants without an owner, and owners without an email (such as the
// bootstrap admin), are unaffected: only accounts that signed up with an
// email must verify it before publishing.
func checkEmailVerifiedForPublish(ctx context.Context, auth *authpkg.Service, tenant tenants.Tenant, isActive bool) error {
	if !isActive || tenant.OwnerUserID == nil {
		return nil
	}
	owner, err := auth.GetUser(ctx, *tenant.OwnerUserID)
	if err != nil {
		return err
	}
	if owner.Email != "" && !owner.EmailVerified() {
		return apperrors.NewEmailUnverified(msgEmailUnverified)
	}
	return nil
}

// checkNotifyOnSubmissionAllowed returns a plan_feature_unavailable error
// when notify is true and the tenant's plan does not include email
// notifications, mirroring checkWebhooksAllowed in webhooks.go.
func checkNotifyOnSubmissionAllowed(t tenants.Tenant, notify bool) error {
	if notify && !plans.For(t.Plan).EmailNotifications {
		return apperrors.NewPlanFeatureUnavailable("Email notifications on new submissions are available on the Pro plan. Upgrade to Pro to enable them.")
	}
	return nil
}

func toFormResponse(f forms.Form) FormResponse {
	return FormResponse{
		ID:                 f.ID,
		TenantID:           f.TenantID,
		Title:              f.Title,
		Slug:               f.Slug,
		Description:        f.Description,
		IsActive:           f.IsActive,
		StartDate:          f.StartDate,
		EndDate:            f.EndDate,
		Content:            f.Content,
		PublicAvailable:    f.PublicAvailable,
		AcceptAnonymous:    f.AcceptAnonymous,
		NotifyOnSubmission: f.NotifyOnSubmission,
		CreatedAt:          f.CreatedAt,
		UpdatedAt:          f.UpdatedAt,
	}
}
