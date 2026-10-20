package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	apperrors "app/internal/errors"
	authpkg "app/internal/pkg/auth"
	"app/internal/pkg/files"
	"app/internal/pkg/forms"
	"app/internal/pkg/mailer"
	"app/internal/pkg/plans"
	"app/internal/pkg/submissions"
	"app/internal/pkg/tenants"
)

// Messages of the submission validation errors.
const (
	msgInvalidAnswers = "some answers are invalid"
	msgFileNotFound   = "file not found; upload it again"
	// msgNotAccepting is shown to respondents instead of the plan limit
	// reached by the form's tenant, which they need not know about.
	msgNotAccepting = "This form is not accepting responses right now."
)

// CreateSubmissionRequest is the body of POST /public/forms/{slug}/submissions.
type CreateSubmissionRequest struct {
	// Payload holds the answers: a JSON object keyed by the names of the
	// form's input fields. File fields take the IDs returned by
	// POST /public/forms/{slug}/files.
	Payload json.RawMessage `json:"payload" swaggertype:"object"`
	// CompletionTimeSeconds is how long the respondent took to fill the form in.
	CompletionTimeSeconds *int32 `json:"completion_time_seconds" example:"95" minimum:"0"`
	// Website is the honeypot field of the form page: hidden from people, so
	// any value marks the submission as automated. Leave it empty.
	Website string `json:"website" example:""`
	// TurnstileToken is the Cloudflare Turnstile response token, required
	// when Turnstile is enabled on the server.
	TurnstileToken string `json:"turnstile_token" example:""`
}

// SubmissionResponse is a submission as returned by the API.
type SubmissionResponse struct {
	ID          int32           `json:"id" example:"1"`
	FormID      int32           `json:"form_id" example:"1"`
	Payload     json.RawMessage `json:"payload" swaggertype:"object"`
	SubmittedAt time.Time       `json:"submitted_at" example:"2026-01-01T00:00:00Z"`
	// UserID is the signed-in submitter; null for anonymous submissions.
	UserID   *int32            `json:"user_id" example:"3"`
	Metadata *MetadataResponse `json:"metadata,omitempty"`
}

// MetadataResponse describes the client that sent a submission.
type MetadataResponse struct {
	IPAddress             *string `json:"ip_address" example:"203.0.113.7"`
	UserAgent             *string `json:"user_agent" example:"Mozilla/5.0"`
	CompletionTimeSeconds *int32  `json:"completion_time_seconds" example:"95"`
	Referer               *string `json:"referer" example:"https://example.com/contact"`
}

// SubmissionListResponse is a page of submissions.
type SubmissionListResponse struct {
	Items []SubmissionResponse `json:"items"`
	PageResponse
}

// submissionsController serves the submission endpoints.
type submissionsController struct {
	forms       forms.Repository
	submissions submissions.Repository
	files       files.Repository
	plans       *plans.Enforcer
	// tenants, auth, mail and baseURL support the new-submission
	// notification email; see notifyNewSubmission.
	tenants tenants.Repository
	auth    *authpkg.Service
	mail    mailer.Mailer
	baseURL string
	// ip resolves the respondent's IP for submission metadata, trusting
	// X-Forwarded-For only from a configured reverse proxy.
	ip clientIPResolver
	// spam runs the honeypot, minimum completion time and Turnstile checks.
	spam spamGuard
}

// Create stores a submission of a public form.
//
//	@Summary		Submit a form
//	@Description	Stores the answers to a form that can currently be filled in, along with the client's IP address, user agent and referer. Forms that are not public_available require a signed-in user (bearer token or Basic credentials). The submitter's user ID is recorded only by forms that do not accept_anonymous. The payload is validated against the form definition: unknown keys are rejected, required fields must be answered and every answer must match its field's type and options; file fields take IDs of files uploaded to the form. Spam checks run first: a non-empty website honeypot, a completion_time_seconds missing or below the configured minimum, or (when Turnstile is enabled) a missing or rejected turnstile_token give a 400. Invalid answers get a 400 whose "fields" object maps each offending field name to its error. A form whose tenant reached its plan's monthly submission limit fails with plan_limit_reached and a generic message. Cross-origin requests are allowed from any origin (CORS, without cookies).
//	@Tags			submissions,public
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			slug	path		string						true	"Form slug"
//	@Param			body	body		CreateSubmissionRequest		true	"Answers"
//	@Success		201		{object}	SubmissionResponse			"Submission stored"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid request, failed spam check, or invalid answers listed in fields"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Sign-in required, or invalid credentials"
//	@Failure		402		{object}	errors.HTTPErrorResponse	"Form not accepting responses: monthly submission limit reached"
//	@Failure		403		{object}	errors.HTTPErrorResponse	"Form inactive, not open yet or closed"
//	@Failure		404		{object}	errors.HTTPErrorResponse	"Form not found"
//	@Failure		429		{object}	errors.HTTPErrorResponse	"Too many requests from this IP; retry after the Retry-After header seconds"
//	@Failure		500		{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/public/forms/{slug}/submissions [post]
func (c *submissionsController) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateSubmissionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	var payload map[string]json.RawMessage
	switch {
	case len(req.Payload) == 0 || bytes.Equal(req.Payload, []byte("null")):
		apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("payload is required"))
		return
	case req.Payload[0] != '{' || json.Unmarshal(req.Payload, &payload) != nil:
		apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("payload must be a JSON object"))
		return
	case req.CompletionTimeSeconds != nil && *req.CompletionTimeSeconds < 0:
		apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("completion_time_seconds must be >= 0"))
		return
	}
	if err := c.spam.check(r, req); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}

	f, err := availableForm(r, c.forms)
	if err != nil {
		unauthorized(w, r, err)
		return
	}
	in := submissions.CreateSubmissionInput{FormID: f.ID, Payload: req.Payload}
	if !f.AcceptAnonymous {
		u := userFrom(r.Context())
		if u == nil {
			unauthorized(w, r, apperrors.NewUnauthorized("sign in to submit this form"))
			return
		}
		in.UserID = &u.ID
	}
	if err := checkSubmissionLimit(r, c.plans, f); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	if err := c.validatePayload(r, f.Form, payload); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	sub, err := c.submissions.Create(r.Context(), in)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}

	// The submission is already stored: failing to record its metadata must
	// not make the client resend it, so only log the error.
	meta, err := c.submissions.AddMetadata(r.Context(), sub.ID, submissions.Metadata{
		IPAddress:             c.ip.resolvePtr(r),
		UserAgent:             headerPtr(r, "User-Agent"),
		CompletionTimeSeconds: req.CompletionTimeSeconds,
		Referer:               headerPtr(r, "Referer"),
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "record submission metadata", "submission_id", sub.ID, "error", err)
	} else {
		sub.Metadata = &meta
	}
	if f.NotifyOnSubmission && plans.For(f.TenantPlan).EmailNotifications {
		c.notifyOwner(r.Context(), f.Form)
	}
	writeJSON(w, r, http.StatusCreated, toSubmissionResponse(sub))
}

// notifyOwner emails f's tenant owner that f received a new submission, when
// the tenant has an owner with an email address. Failures are logged, never
// returned: the caller's response must not depend on email delivery, and the
// mailer (normally mailer.Async) already sends without blocking.
func (c *submissionsController) notifyOwner(ctx context.Context, f forms.Form) {
	tenant, err := c.tenants.GetByID(ctx, f.TenantID)
	if err != nil || tenant.OwnerUserID == nil {
		return
	}
	owner, err := c.auth.GetUser(ctx, *tenant.OwnerUserID)
	if err != nil || owner.Email == "" {
		return
	}
	r, err := mailer.RenderNewSubmissionNotification(c.baseURL, strconv.Itoa(int(f.ID)), f.Title, time.Now())
	if err != nil {
		slog.ErrorContext(ctx, "render new submission notification", "form_id", f.ID, "error", err)
		return
	}
	if err := c.mail.Send(ctx, mailer.Message{To: owner.Email, Subject: r.Subject, Text: r.Text, HTML: r.HTML}); err != nil {
		slog.ErrorContext(ctx, "send new submission notification", "form_id", f.ID, "error", err)
	}
}

// checkSubmissionLimit checks that f's tenant may receive another submission
// this month. Over the limit it returns a plan_limit_reached error with the
// generic msgNotAccepting, keeping the tenant's plan from respondents.
func checkSubmissionLimit(r *http.Request, enforcer *plans.Enforcer, f forms.FormBySlug) error {
	return hidePlanLimit(enforcer.CheckSubmission(r.Context(), f.TenantID, f.TenantPlan, time.Now()))
}

// hidePlanLimit replaces the message of a plan_limit_reached err with the
// generic msgNotAccepting, for public endpoints whose callers need not know
// the tenant's plan. Other errors are returned unchanged.
func hidePlanLimit(err error) error {
	if e, ok := apperrors.As(err); ok && e.Code == apperrors.CodePlanLimitReached {
		return apperrors.NewPlanLimitReached(msgNotAccepting)
	}
	return err
}

// validatePayload checks payload against the definition of f and that the
// files it references were uploaded to f's tenant, returning a validation
// error listing the offending fields.
func (c *submissionsController) validatePayload(r *http.Request, f forms.Form, payload map[string]json.RawMessage) error {
	content, err := forms.ParseContent(f.Content)
	if err != nil {
		// The form cannot be filled in: there is no way to know its fields.
		return apperrors.Wrap(err, apperrors.CodeInternal, "form "+f.Slug+" has an invalid definition")
	}
	if errs := forms.ValidatePayload(content, payload); errs != nil {
		return apperrors.NewValidation(msgInvalidAnswers, errs)
	}
	errs := map[string]string{}
	for name, ids := range forms.FileIDs(content, payload) {
		for _, id := range ids {
			file, err := c.files.GetInfoByID(r.Context(), id)
			switch {
			case apperrors.HTTPStatus(err) == http.StatusNotFound || err == nil && file.TenantID != f.TenantID:
				// Other tenants' files are reported as missing so their IDs
				// cannot be probed.
				errs[name] = msgFileNotFound
			case err != nil:
				return err
			}
		}
	}
	if len(errs) > 0 {
		return apperrors.NewValidation(msgInvalidAnswers, errs)
	}
	return nil
}

// List lists the submissions of one of the authenticated tenant's forms.
//
//	@Summary		List form submissions
//	@Description	Lists the submissions of one of the authenticated tenant's forms, newest first, with their metadata.
//	@Tags			submissions
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			id		path		int							true	"Form ID"
//	@Param			offset	query		int							false	"Number of submissions to skip"				minimum(0)	default(0)
//	@Param			limit	query		int							false	"Maximum number of submissions to return"	minimum(1)	maximum(100)	default(20)
//	@Success		200		{object}	SubmissionListResponse		"Page of submissions"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid request"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Missing or invalid API key or user credentials"
//	@Failure		403		{object}	errors.HTTPErrorResponse	"Signed-in user has no workspace"
//	@Failure		404		{object}	errors.HTTPErrorResponse	"Form not found"
//	@Failure		500		{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/forms/{id}/submissions [get]
func (c *submissionsController) List(w http.ResponseWriter, r *http.Request) {
	formID, err := pathID(r, "id")
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	offset, limit, err := parsePage(r)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	tenantID := tenantFrom(r.Context()).ID
	// ListByForm returns an empty list for other tenants' forms; check
	// ownership first so they get a 404 instead.
	if _, err := c.forms.GetByID(r.Context(), tenantID, formID); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	subs, err := c.submissions.ListByForm(r.Context(),
		submissions.ListSubmissionsFilter{TenantID: tenantID, FormID: formID},
		submissions.Page{Offset: offset, Limit: limit})
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	resp := SubmissionListResponse{
		Items:        make([]SubmissionResponse, len(subs)),
		PageResponse: PageResponse{Offset: offset, Limit: limit},
	}
	for i, s := range subs {
		resp.Items[i] = toSubmissionResponse(s)
	}
	writeJSON(w, r, http.StatusOK, resp)
}

// headerPtr returns the value of the named request header, or nil when empty.
func headerPtr(r *http.Request, name string) *string {
	if v := r.Header.Get(name); v != "" {
		return &v
	}
	return nil
}

func toSubmissionResponse(s submissions.Submission) SubmissionResponse {
	resp := SubmissionResponse{ID: s.ID, FormID: s.FormID, Payload: s.Payload, SubmittedAt: s.SubmittedAt, UserID: s.UserID}
	if s.Metadata != nil {
		resp.Metadata = &MetadataResponse{
			IPAddress:             s.Metadata.IPAddress,
			UserAgent:             s.Metadata.UserAgent,
			CompletionTimeSeconds: s.Metadata.CompletionTimeSeconds,
			Referer:               s.Metadata.Referer,
		}
	}
	return resp
}
