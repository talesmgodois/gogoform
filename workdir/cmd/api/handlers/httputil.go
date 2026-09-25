package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	apperrors "app/internal/errors"
)

const (
	// maxBodyBytes caps the size of every JSON request body.
	maxBodyBytes = 1 << 20
	// defaultPageLimit and maxPageLimit bound the size of listing pages.
	defaultPageLimit = 20
	maxPageLimit     = 100
)

// PageResponse describes the window returned by a listing endpoint.
type PageResponse struct {
	Offset int32 `json:"offset" example:"0"`
	Limit  int32 `json:"limit" example:"20"`
}

// writeJSON writes v as a JSON body with the given status code.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.ErrorContext(r.Context(), "encode response", "error", err)
	}
}

// decodeJSON decodes the request body into dst, rejecting unknown fields,
// trailing data and bodies larger than maxBodyBytes.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return apperrors.NewBadRequest("request body too large")
		}
		return apperrors.NewBadRequest("invalid JSON body: " + err.Error())
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return apperrors.NewBadRequest("request body must contain a single JSON object")
	}
	return nil
}

// pathID parses the positive int32 path value called name.
func pathID(r *http.Request, name string) (int32, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 32)
	if err != nil || id < 1 {
		return 0, apperrors.NewBadRequest(name + " must be a positive integer")
	}
	return int32(id), nil
}

// parsePage reads the offset and limit query parameters. A missing limit
// defaults to defaultPageLimit; limits above maxPageLimit are rejected.
func parsePage(r *http.Request) (offset, limit int32, err error) {
	q := r.URL.Query()
	offset, limit = 0, defaultPageLimit
	if v := q.Get("offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 0 {
			return 0, 0, apperrors.NewBadRequest("offset must be an integer >= 0")
		}
		offset = int32(n)
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 1 || n > maxPageLimit {
			return 0, 0, apperrors.NewBadRequest("limit must be an integer between 1 and " + strconv.Itoa(maxPageLimit))
		}
		limit = int32(n)
	}
	return offset, limit, nil
}
