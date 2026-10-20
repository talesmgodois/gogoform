package handlers

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	apperrors "app/internal/errors"
	"app/internal/pkg/files"
	"app/internal/pkg/forms"
	"app/internal/pkg/plans"
)

const (
	// maxUploadBytes caps the size of an uploaded file. Files are stored as
	// database blobs and loaded in memory, so keep this small.
	maxUploadBytes = 10 << 20
	// multipartOverheadBytes is the room left in the request body for the
	// multipart boundaries, part headers and other form fields.
	multipartOverheadBytes = 64 << 10
	// uploadField is the multipart form field carrying the file.
	uploadField = "file"
	// formFieldValue is the multipart form field naming, in public uploads,
	// the file field of the form the file is for.
	formFieldValue = "field"
	// maxFileTextLen matches the size of the files.name and
	// files.content_type columns.
	maxFileTextLen = 255
	// defaultContentType is used when the type can be neither parsed nor sniffed.
	defaultContentType = "application/octet-stream"
)

// FileResponse is an uploaded file's metadata as returned by the API.
type FileResponse struct {
	ID          string `json:"id" example:"0b6a3b2e-8f1c-4c4e-9d5e-2f1a7c3b9e10"`
	Name        string `json:"name" example:"logo.png"`
	ContentType string `json:"content_type" example:"image/png"`
	Size        int64  `json:"size" example:"2048"`
	// Checksum is the hex-encoded SHA-256 of the content.
	Checksum  string    `json:"checksum_sha256" example:"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"`
	URL       string    `json:"url" example:"/files/0b6a3b2e-8f1c-4c4e-9d5e-2f1a7c3b9e10"`
	CreatedAt time.Time `json:"created_at" example:"2026-01-01T00:00:00Z"`
}

// filesController serves the file endpoints.
type filesController struct {
	files files.Repository
	forms forms.Repository
	plans *plans.Enforcer
}

// Create uploads a file for the authenticated tenant.
//
//	@Summary		Upload a file
//	@Description	Stores the file sent in the "file" field of a multipart form. Files are limited to 10 MiB and can then be read publicly at the returned url. Fails with plan_limit_reached when the file would take the tenant over its plan's storage limit.
//	@Tags			files
//	@Accept			multipart/form-data
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			file	formData	file						true	"File to upload"
//	@Success		201		{object}	FileResponse				"File uploaded"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid, missing, empty or too large file"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Missing or invalid API key or user credentials"
//	@Failure		402		{object}	errors.HTTPErrorResponse	"Plan storage limit reached"
//	@Failure		403		{object}	errors.HTTPErrorResponse	"Signed-in user has no workspace, or cross-origin request authenticated by the session cookie"
//	@Failure		500		{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/files [post]
func (c *filesController) Create(w http.ResponseWriter, r *http.Request) {
	in, _, err := readUpload(w, r)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	tenant := tenantFrom(r.Context())
	if err := c.plans.CheckUpload(r.Context(), tenant.ID, tenant.Plan, int64(len(in.Data))); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	in.TenantID = tenant.ID
	f, err := c.files.Create(r.Context(), in)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	resp := toFileResponse(f)
	w.Header().Set("Location", resp.URL)
	writeJSON(w, r, http.StatusCreated, resp)
}

// PublicCreate uploads a file for one of the file fields of a public form,
// storing it under the form's tenant.
//
//	@Summary		Upload a file to a form
//	@Description	Stores the file sent in the "file" field of a multipart form, for the file field of the form named by the "field" value, which must be sent before the file. No API key is needed; the form must be open to the request's user as for submissions (private forms need a signed-in user, forms that do not accept_anonymous too). The file must match the field's accept list and be at most its max_size_mb (10 MiB at most). Send the returned id as the field's value in the submission. A form whose tenant would go over its plan's storage limit fails with plan_limit_reached and a generic message.
//	@Tags			files,public
//	@Accept			multipart/form-data
//	@Produce		json
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			slug	path		string						true	"Form slug"
//	@Param			field	formData	string						true	"Name of the form's file field"
//	@Param			file	formData	file						true	"File to upload"
//	@Success		201		{object}	FileResponse				"File uploaded"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid, missing, empty, too large or not accepted file, or unknown or non-file field"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Sign-in required, or invalid credentials"
//	@Failure		402		{object}	errors.HTTPErrorResponse	"Form not accepting responses: storage limit reached"
//	@Failure		403		{object}	errors.HTTPErrorResponse	"Form inactive, not open yet or closed"
//	@Failure		404		{object}	errors.HTTPErrorResponse	"Form not found"
//	@Failure		429		{object}	errors.HTTPErrorResponse	"Too many requests from this IP; retry after the Retry-After header seconds"
//	@Failure		500		{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/public/forms/{slug}/files [post]
func (c *filesController) PublicCreate(w http.ResponseWriter, r *http.Request) {
	f, err := availableForm(r, c.forms)
	if err != nil {
		unauthorized(w, r, err)
		return
	}
	if !f.AcceptAnonymous && userFrom(r.Context()) == nil {
		unauthorized(w, r, apperrors.NewUnauthorized("sign in to submit this form"))
		return
	}
	in, values, err := readUpload(w, r)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	field, err := uploadFormField(f.Form, values.Get(formFieldValue))
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	switch {
	case int64(len(in.Data)) > field.MaxSizeBytes():
		apperrors.WriteHTTP(w, r, apperrors.NewBadRequest(
			"file must be at most "+strconv.FormatFloat(float64(field.MaxSizeBytes())/(1<<20), 'f', -1, 64)+" MiB"))
		return
	case !field.Accepts(in.Name, in.ContentType):
		apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("file type is not accepted, expected "+field.Accept))
		return
	}
	if err := hidePlanLimit(c.plans.CheckUpload(r.Context(), f.TenantID, f.TenantPlan, int64(len(in.Data)))); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	in.TenantID = f.TenantID
	file, err := c.files.Create(r.Context(), in)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	resp := toFileResponse(file)
	w.Header().Set("Location", resp.URL)
	writeJSON(w, r, http.StatusCreated, resp)
}

// uploadFormField returns the file field called name of the form f.
func uploadFormField(f forms.Form, name string) (forms.Field, error) {
	if name == "" {
		return forms.Field{}, apperrors.NewBadRequest(`multipart field "` + formFieldValue + `" naming a file field is required before the file`)
	}
	content, err := forms.ParseContent(f.Content)
	if err != nil {
		return forms.Field{}, apperrors.NewBadRequest("form has no file field " + strconv.Quote(name))
	}
	field, ok := content.Field(name)
	switch {
	case !ok:
		return forms.Field{}, apperrors.NewBadRequest("form has no field " + strconv.Quote(name))
	case field.Type != forms.FieldFile:
		return forms.Field{}, apperrors.NewBadRequest("field " + strconv.Quote(name) + " is not a file field")
	}
	return field, nil
}

// Get serves a file's content.
//
//	@Summary		Download a file
//	@Description	Returns the raw content of a file, with its stored content type. Supports HEAD, Range and conditional (ETag) requests. No API key is needed: the file ID is unguessable. Set download=1 to have browsers save the file instead of displaying it.
//	@Tags			files
//	@Produce		octet-stream
//	@Param			id			path		string						true	"File ID (UUID)"
//	@Param			download	query		bool						false	"Serve the file as an attachment"
//	@Success		200	{file}		file						"File content"
//	@Failure		404	{object}	errors.HTTPErrorResponse	"File not found"
//	@Failure		500	{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/files/{id} [get]
func (c *filesController) Get(w http.ResponseWriter, r *http.Request) {
	f, err := c.files.GetByID(r.Context(), r.PathValue("id"))
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	dispositionType := "inline"
	if download, _ := strconv.ParseBool(r.URL.Query().Get("download")); download {
		dispositionType = "attachment"
	}
	disposition := mime.FormatMediaType(dispositionType, map[string]string{"filename": f.Name})
	if disposition == "" {
		disposition = dispositionType
	}
	h := w.Header()
	h.Set("Content-Type", f.ContentType)
	h.Set("Content-Disposition", disposition)
	h.Set("ETag", `"`+f.Checksum+`"`)
	h.Set("Cache-Control", "no-cache")
	// Uploads are untrusted and served from the API's origin: never let the
	// browser sniff them into another type or run scripts they contain.
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(w, r, "", f.CreatedAt, bytes.NewReader(f.Data))
}

// Delete removes one of the authenticated tenant's files.
//
//	@Summary		Delete a file
//	@Description	Deletes one of the authenticated tenant's files.
//	@Tags			files
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			id	path	string	true	"File ID (UUID)"
//	@Success		204	"File deleted"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Missing or invalid API key or user credentials"
//	@Failure		403	{object}	errors.HTTPErrorResponse	"Signed-in user has no workspace, or cross-origin request authenticated by the session cookie"
//	@Failure		404	{object}	errors.HTTPErrorResponse	"File not found"
//	@Failure		500	{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/files/{id} [delete]
func (c *filesController) Delete(w http.ResponseWriter, r *http.Request) {
	if err := c.files.Delete(r.Context(), tenantFrom(r.Context()).ID, r.PathValue("id")); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readUpload streams the multipart body and returns the first part of the
// uploadField field, rejecting bodies without it, empty files and files
// larger than maxUploadBytes. It also returns the values of the text fields
// sent before the file, truncated to maxFileTextLen bytes; later fields are
// ignored.
func readUpload(w http.ResponseWriter, r *http.Request) (files.CreateFileInput, url.Values, error) {
	tooLarge := apperrors.NewBadRequest("file must be at most 10 MiB")
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+multipartOverheadBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		return files.CreateFileInput{}, nil, apperrors.NewBadRequest("request must be a multipart/form-data body")
	}
	values := url.Values{}
	for {
		part, err := mr.NextPart()
		var maxErr *http.MaxBytesError
		switch {
		case errors.Is(err, io.EOF):
			return files.CreateFileInput{}, nil, apperrors.NewBadRequest(`multipart field "` + uploadField + `" with a file is required`)
		case errors.As(err, &maxErr):
			return files.CreateFileInput{}, nil, tooLarge
		case err != nil:
			return files.CreateFileInput{}, nil, apperrors.NewBadRequest("invalid multipart body: " + err.Error())
		}
		if part.FormName() != uploadField || part.FileName() == "" {
			if part.FileName() == "" && part.FormName() != "" {
				v, err := io.ReadAll(io.LimitReader(part, maxFileTextLen))
				if err != nil {
					part.Close()
					return files.CreateFileInput{}, nil, apperrors.NewBadRequest("invalid multipart body: " + err.Error())
				}
				values.Add(part.FormName(), string(v))
			}
			part.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, maxUploadBytes+1))
		part.Close()
		switch {
		case errors.As(err, &maxErr), len(data) > maxUploadBytes:
			return files.CreateFileInput{}, nil, tooLarge
		case err != nil:
			return files.CreateFileInput{}, nil, apperrors.NewBadRequest("invalid multipart body: " + err.Error())
		case len(data) == 0:
			return files.CreateFileInput{}, nil, apperrors.NewBadRequest("file must not be empty")
		}
		return files.CreateFileInput{
			Name:        sanitizeFileName(part.FileName()),
			ContentType: detectContentType(part.Header.Get("Content-Type"), data),
			Data:        data,
		}, values, nil
	}
}

// sanitizeFileName drops control characters and surrounding spaces from a
// client-provided file name and truncates it to maxFileTextLen characters.
// (multipart.Part.FileName already strips any directory.)
func sanitizeFileName(name string) string {
	name = strings.ToValidUTF8(name, "")
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name))
	if utf8.RuneCountInString(name) > maxFileTextLen {
		name = string([]rune(name)[:maxFileTextLen])
	}
	if name == "" || name == "." || name == ".." {
		return uploadField
	}
	return name
}

// detectContentType returns the normalized content type declared by the
// client, or one sniffed from data when the declared one is missing, generic
// or invalid.
func detectContentType(declared string, data []byte) string {
	if mediaType, params, err := mime.ParseMediaType(declared); err == nil && mediaType != defaultContentType {
		if ct := mime.FormatMediaType(mediaType, params); ct != "" && len(ct) <= maxFileTextLen {
			return ct
		}
	}
	return http.DetectContentType(data)
}

func toFileResponse(f files.File) FileResponse {
	return FileResponse{
		ID:          f.ID,
		Name:        f.Name,
		ContentType: f.ContentType,
		Size:        f.Size,
		Checksum:    f.Checksum,
		URL:         "/files/" + f.ID,
		CreatedAt:   f.CreatedAt,
	}
}
