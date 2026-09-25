package main

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	apperrors "app/internal/errors"
	"app/internal/pkg/files"
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
}

// Create uploads a file for the authenticated tenant.
//
//	@Summary		Upload a file
//	@Description	Stores the file sent in the "file" field of a multipart form. Files are limited to 10 MiB and can then be read publicly at the returned url.
//	@Tags			files
//	@Accept			multipart/form-data
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			file	formData	file						true	"File to upload"
//	@Success		201		{object}	FileResponse				"File uploaded"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid, missing, empty or too large file"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Missing or invalid API key"
//	@Failure		500		{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/files [post]
func (c *filesController) Create(w http.ResponseWriter, r *http.Request) {
	in, err := readUpload(w, r)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	in.TenantID = tenantFrom(r.Context()).ID
	f, err := c.files.Create(r.Context(), in)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	resp := toFileResponse(f)
	w.Header().Set("Location", resp.URL)
	writeJSON(w, r, http.StatusCreated, resp)
}

// Get serves a file's content.
//
//	@Summary		Download a file
//	@Description	Returns the raw content of a file, with its stored content type. Supports HEAD, Range and conditional (ETag) requests. No API key is needed: the file ID is unguessable.
//	@Tags			files
//	@Produce		octet-stream
//	@Param			id	path		string						true	"File ID (UUID)"
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
	disposition := mime.FormatMediaType("inline", map[string]string{"filename": f.Name})
	if disposition == "" {
		disposition = "inline"
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
//	@Param			id	path	string	true	"File ID (UUID)"
//	@Success		204	"File deleted"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Missing or invalid API key"
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
// larger than maxUploadBytes. Other fields are ignored.
func readUpload(w http.ResponseWriter, r *http.Request) (files.CreateFileInput, error) {
	tooLarge := apperrors.NewBadRequest("file must be at most 10 MiB")
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+multipartOverheadBytes)
	mr, err := r.MultipartReader()
	if err != nil {
		return files.CreateFileInput{}, apperrors.NewBadRequest("request must be a multipart/form-data body")
	}
	for {
		part, err := mr.NextPart()
		var maxErr *http.MaxBytesError
		switch {
		case errors.Is(err, io.EOF):
			return files.CreateFileInput{}, apperrors.NewBadRequest(`multipart field "` + uploadField + `" with a file is required`)
		case errors.As(err, &maxErr):
			return files.CreateFileInput{}, tooLarge
		case err != nil:
			return files.CreateFileInput{}, apperrors.NewBadRequest("invalid multipart body: " + err.Error())
		}
		if part.FormName() != uploadField || part.FileName() == "" {
			part.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, maxUploadBytes+1))
		part.Close()
		switch {
		case errors.As(err, &maxErr), len(data) > maxUploadBytes:
			return files.CreateFileInput{}, tooLarge
		case err != nil:
			return files.CreateFileInput{}, apperrors.NewBadRequest("invalid multipart body: " + err.Error())
		case len(data) == 0:
			return files.CreateFileInput{}, apperrors.NewBadRequest("file must not be empty")
		}
		return files.CreateFileInput{
			Name:        sanitizeFileName(part.FileName()),
			ContentType: detectContentType(part.Header.Get("Content-Type"), data),
			Data:        data,
		}, nil
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
