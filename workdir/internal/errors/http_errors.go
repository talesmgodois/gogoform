package errors

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// internalMessage replaces the message of internal errors so causes never leak to clients.
const internalMessage = "internal server error"

// HTTPErrorResponse is the JSON body written for failed requests.
type HTTPErrorResponse struct {
	Code    Code   `json:"code" example:"not_found"`
	Message string `json:"message" example:"form not found"`
}

// NewBadRequest returns an AppError that maps to 400 Bad Request.
func NewBadRequest(message string) *AppError {
	return New(CodeInvalidArgument, message)
}

// NewUnauthorized returns an AppError that maps to 401 Unauthorized.
func NewUnauthorized(message string) *AppError {
	return New(CodeUnauthorized, message)
}

// NewForbidden returns an AppError that maps to 403 Forbidden.
func NewForbidden(message string) *AppError {
	return New(CodeForbidden, message)
}

// NewNotFound returns an AppError that maps to 404 Not Found.
func NewNotFound(message string) *AppError {
	return New(CodeNotFound, message)
}

// NewConflict returns an AppError that maps to 409 Conflict.
func NewConflict(message string) *AppError {
	return New(CodeConflict, message)
}

// NewGone returns an AppError that maps to 410 Gone.
func NewGone(message string) *AppError {
	return New(CodeGone, message)
}

// NewInternal returns an AppError that wraps err and maps to 500 Internal Server Error.
func NewInternal(err error) *AppError {
	return Wrap(err, CodeInternal, internalMessage)
}

// HTTPStatus returns the HTTP status code for err. Errors that are not an
// AppError, or have an unknown code, map to 500.
func HTTPStatus(err error) int {
	appErr, ok := As(err)
	if !ok {
		return http.StatusInternalServerError
	}
	switch appErr.Code {
	case CodeInvalidArgument:
		return http.StatusBadRequest
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict:
		return http.StatusConflict
	case CodeGone:
		return http.StatusGone
	default:
		return http.StatusInternalServerError
	}
}

// WriteHTTP writes err as a JSON HTTPErrorResponse with the matching status
// code. 5xx errors are logged and their message is replaced with a generic one.
func WriteHTTP(w http.ResponseWriter, r *http.Request, err error) {
	status := HTTPStatus(err)

	resp := HTTPErrorResponse{Code: CodeInternal, Message: internalMessage}
	if appErr, ok := As(err); ok && status < http.StatusInternalServerError {
		resp = HTTPErrorResponse{Code: appErr.Code, Message: appErr.Message}
	}
	if status >= http.StatusInternalServerError {
		slog.ErrorContext(r.Context(), "request failed", "status", status, "error", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if encErr := json.NewEncoder(w).Encode(resp); encErr != nil {
		slog.ErrorContext(r.Context(), "encode error response", "error", encErr)
	}
}
