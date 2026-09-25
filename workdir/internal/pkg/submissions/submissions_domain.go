// Package submissions holds the form submission domain: its data types, the
// Repository port used to persist submissions and their metadata, and a
// Service implementing it on top of sqlc.
package submissions

import (
	"encoding/json"
	"time"
)

// Submission is one filled-in form.
type Submission struct {
	ID     int32
	FormID int32
	// Payload holds the submitted answers as raw JSON.
	Payload     json.RawMessage
	SubmittedAt time.Time
	// UserID is the signed-in submitter; nil for anonymous submissions.
	UserID *int32
	// Metadata is nil when no metadata was recorded for the submission.
	Metadata *Metadata
}

// Metadata describes the client that sent a submission. Every field is
// optional.
type Metadata struct {
	IPAddress             *string
	UserAgent             *string
	CompletionTimeSeconds *int32
	Referer               *string
}

// CreateSubmissionInput holds the data needed to store a submission.
type CreateSubmissionInput struct {
	FormID  int32
	Payload json.RawMessage
	// UserID is the signed-in submitter; nil stores an anonymous submission.
	UserID *int32
}

// ListSubmissionsFilter selects the submissions of one of a tenant's forms.
type ListSubmissionsFilter struct {
	TenantID int32
	FormID   int32
}

// Page selects a window of a listing.
type Page struct {
	Offset int32
	Limit  int32
}
