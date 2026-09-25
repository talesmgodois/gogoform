// Package forms holds the form domain: its data types, the Repository port
// used to persist forms and a Service implementing it on top of sqlc.
package forms

import (
	"encoding/json"
	"time"
)

// Form is a form owned by a tenant.
type Form struct {
	ID          int32
	TenantID    int32
	Title       string
	Slug        string
	Description *string
	IsActive    bool
	// StartDate and EndDate bound the availability window; nil means unbounded.
	StartDate *time.Time
	EndDate   *time.Time
	// Content is the form definition (fields, layout) as raw JSON.
	Content   json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
}

// FormDetails is a Form together with the name of its tenant.
type FormDetails struct {
	Form
	TenantName string
}

// FormSummary is a Form as shown in listings, with its submission count.
type FormSummary struct {
	Form
	SubmissionCount int64
}

// FormOverview is a FormSummary together with the name of its tenant, as
// shown in cross-tenant listings.
type FormOverview struct {
	FormSummary
	TenantName string
}

// CreateFormInput holds the data needed to create a form.
type CreateFormInput struct {
	TenantID    int32
	Title       string
	Slug        string
	Description *string
	// IsActive nil creates an active form.
	IsActive  *bool
	StartDate *time.Time
	EndDate   *time.Time
	Content   json.RawMessage
}

// UpdateFormInput holds the full new state of an existing form. ID and
// TenantID identify the form; every other field replaces the stored value.
type UpdateFormInput struct {
	ID          int32
	TenantID    int32
	Title       string
	Slug        string
	Description *string
	IsActive    bool
	StartDate   *time.Time
	EndDate     *time.Time
	Content     json.RawMessage
}

// ListFormsFilter selects the forms of a tenant.
type ListFormsFilter struct {
	TenantID int32
	// IsActive nil matches forms in any state.
	IsActive *bool
	// Search nil matches any title; otherwise a case-insensitive substring match.
	Search *string
}

// Page selects a window of a listing.
type Page struct {
	Offset int32
	Limit  int32
}
