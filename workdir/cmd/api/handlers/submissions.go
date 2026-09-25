package handlers

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/forms"
	"app/internal/pkg/submissions"
)

// CreateSubmissionRequest is the body of POST /public/forms/{slug}/submissions.
type CreateSubmissionRequest struct {
	// Payload holds the answers; any JSON value but null.
	Payload json.RawMessage `json:"payload" swaggertype:"object"`
	// CompletionTimeSeconds is how long the respondent took to fill the form in.
	CompletionTimeSeconds *int32 `json:"completion_time_seconds" example:"95" minimum:"0"`
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
}

// Create stores a submission of a public form.
//
//	@Summary		Submit a form
//	@Description	Stores the answers to a form that can currently be filled in, along with the client's IP address, user agent and referer. Forms that are not public_available require a signed-in user (bearer token or Basic credentials). The submitter's user ID is recorded only by forms that do not accept_anonymous.
//	@Tags			submissions,public
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			slug	path		string						true	"Form slug"
//	@Param			body	body		CreateSubmissionRequest		true	"Answers"
//	@Success		201		{object}	SubmissionResponse			"Submission stored"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid request"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Sign-in required, or invalid credentials"
//	@Failure		404		{object}	errors.HTTPErrorResponse	"Form not found or not available"
//	@Failure		500		{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/public/forms/{slug}/submissions [post]
func (c *submissionsController) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateSubmissionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	switch {
	case len(req.Payload) == 0 || bytes.Equal(req.Payload, []byte("null")):
		apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("payload is required"))
		return
	case req.CompletionTimeSeconds != nil && *req.CompletionTimeSeconds < 0:
		apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("completion_time_seconds must be >= 0"))
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
	sub, err := c.submissions.Create(r.Context(), in)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}

	// The submission is already stored: failing to record its metadata must
	// not make the client resend it, so only log the error.
	meta, err := c.submissions.AddMetadata(r.Context(), sub.ID, submissions.Metadata{
		IPAddress:             clientIP(r),
		UserAgent:             headerPtr(r, "User-Agent"),
		CompletionTimeSeconds: req.CompletionTimeSeconds,
		Referer:               headerPtr(r, "Referer"),
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "record submission metadata", "submission_id", sub.ID, "error", err)
	} else {
		sub.Metadata = &meta
	}
	writeJSON(w, r, http.StatusCreated, toSubmissionResponse(sub))
}

// List lists the submissions of one of the authenticated tenant's forms.
//
//	@Summary		List form submissions
//	@Description	Lists the submissions of one of the authenticated tenant's forms, newest first, with their metadata.
//	@Tags			submissions
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			id		path		int							true	"Form ID"
//	@Param			offset	query		int							false	"Number of submissions to skip"				minimum(0)	default(0)
//	@Param			limit	query		int							false	"Maximum number of submissions to return"	minimum(1)	maximum(100)	default(20)
//	@Success		200		{object}	SubmissionListResponse		"Page of submissions"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid request"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Missing or invalid API key"
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

// clientIP returns the IP address of the peer. Forwarding headers are
// ignored since they can be forged by the client.
func clientIP(r *http.Request) *string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		return nil
	}
	return &host
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
