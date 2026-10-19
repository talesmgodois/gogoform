// Package plans is the single source of truth for what each billing plan
// allows: quantitative limits (forms, monthly submissions, storage) and
// pro-only features.
package plans

import (
	"time"

	"app/internal/pkg/tenants"
)

// Unlimited marks a quantitative limit that does not apply.
const Unlimited int64 = -1

const (
	megabyte int64 = 1 << 20
	gigabyte int64 = 1 << 30
)

// Limits is what a plan allows.
type Limits struct {
	// MaxForms is the number of forms a tenant may have, or Unlimited.
	MaxForms int64
	// MaxSubmissionsPerMonth is the number of submissions accepted across all
	// of a tenant's forms per calendar month (UTC), or Unlimited.
	MaxSubmissionsPerMonth int64
	// MaxStorageBytes is the total size of a tenant's files, or Unlimited.
	MaxStorageBytes int64
	// ShowBadge reports whether public pages show the "Powered by gogoform" badge.
	ShowBadge bool
	// Webhooks reports whether the tenant may manage webhooks.
	Webhooks bool
	// Exports reports whether the tenant may export submissions as CSV/JSON.
	Exports bool
	// EmailNotifications reports whether the tenant may get an email per submission.
	EmailNotifications bool
}

var (
	freeLimits = Limits{
		MaxForms:               3,
		MaxSubmissionsPerMonth: 100,
		MaxStorageBytes:        100 * megabyte,
		ShowBadge:              true,
	}
	proLimits = Limits{
		MaxForms:               Unlimited,
		MaxSubmissionsPerMonth: 10_000,
		MaxStorageBytes:        10 * gigabyte,
		Webhooks:               true,
		Exports:                true,
		EmailNotifications:     true,
	}
)

// For returns the limits of plan. Unknown plans get the free limits.
func For(plan tenants.Plan) Limits {
	if plan == tenants.PlanPro {
		return proLimits
	}
	return freeLimits
}

// CanCreateForm reports whether a tenant that already has formCount forms may
// create another one.
func CanCreateForm(limits Limits, formCount int64) bool {
	return limits.MaxForms == Unlimited || formCount < limits.MaxForms
}

// CanSubmit reports whether a tenant that already received monthCount
// submissions this month may receive another one.
func CanSubmit(limits Limits, monthCount int64) bool {
	return limits.MaxSubmissionsPerMonth == Unlimited || monthCount < limits.MaxSubmissionsPerMonth
}

// CanStore reports whether a tenant using usedBytes may store newBytes more.
// An upload that lands exactly on the limit is allowed.
func CanStore(limits Limits, usedBytes, newBytes int64) bool {
	return limits.MaxStorageBytes == Unlimited || usedBytes+newBytes <= limits.MaxStorageBytes
}

// MonthStart returns midnight UTC on the first day of now's calendar month
// (in UTC), the start of the monthly submission window.
func MonthStart(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}
