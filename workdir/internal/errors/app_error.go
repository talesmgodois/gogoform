// Package errors defines AppError, the application's typed error, and helpers
// to translate it into HTTP responses.
package errors

import (
	stderrors "errors"
)

// Code is a machine-readable error category, independent of transport.
type Code string

const (
	CodeInvalidArgument Code = "invalid_argument"
	CodeUnauthorized    Code = "unauthorized"
	CodeForbidden       Code = "forbidden"
	CodeNotFound        Code = "not_found"
	CodeConflict        Code = "conflict"
	CodeInternal        Code = "internal"
	// CodePlanLimitReached means the tenant's plan does not allow more of a
	// resource (forms, submissions, storage).
	CodePlanLimitReached Code = "plan_limit_reached"
	// CodePlanFeatureUnavailable means the tenant's plan does not include the
	// requested feature.
	CodePlanFeatureUnavailable Code = "plan_feature_unavailable"
)

// AppError is an error carrying a Code, a message safe to show to clients and
// an optional underlying cause.
type AppError struct {
	Code    Code
	Message string
	// Fields maps input field names to what is wrong with them, for
	// validation errors that concern specific fields.
	Fields map[string]string
	Err    error
}

// New returns an AppError with the given code and message.
func New(code Code, message string) *AppError {
	return &AppError{Code: code, Message: message}
}

// Wrap returns an AppError with the given code and message that wraps err.
func Wrap(err error, code Code, message string) *AppError {
	return &AppError{Code: code, Message: message, Err: err}
}

// Error returns the message, followed by the cause when there is one.
func (e *AppError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

// Unwrap returns the underlying cause, so errors.Is and errors.As see through it.
func (e *AppError) Unwrap() error {
	return e.Err
}

// As returns the first AppError in err's chain, if any.
func As(err error) (*AppError, bool) {
	var appErr *AppError
	if stderrors.As(err, &appErr) {
		return appErr, true
	}
	return nil, false
}
