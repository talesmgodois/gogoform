package forms

import "time"

// AvailabilityStatus tells whether a form accepts responses and, if not, why.
type AvailabilityStatus string

const (
	// Available forms accept responses.
	Available AvailabilityStatus = "available"
	// Inactive forms, or forms of an inactive tenant, accept no responses.
	Inactive AvailabilityStatus = "inactive"
	// NotStarted forms open at their StartDate.
	NotStarted AvailabilityStatus = "not_started"
	// Ended forms closed at their EndDate.
	Ended AvailabilityStatus = "ended"
)

// Availability reports whether f accepts responses at now, mirroring the
// rules of GetPublicBySlug: the form and its tenant are active, StartDate is
// not after now and EndDate is after now. Being inactive takes precedence
// over the window. It does not consider whether signing in is required.
func Availability(f Form, tenantActive bool, now time.Time) AvailabilityStatus {
	switch {
	case !f.IsActive || !tenantActive:
		return Inactive
	case f.StartDate != nil && f.StartDate.After(now):
		return NotStarted
	case f.EndDate != nil && !f.EndDate.After(now):
		return Ended
	}
	return Available
}
