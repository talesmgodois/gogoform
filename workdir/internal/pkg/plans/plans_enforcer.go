package plans

import (
	"context"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/forms"
	"app/internal/pkg/tenants"
)

const (
	msgFormLimit       = "Your plan's form limit has been reached. Upgrade to Pro to create more forms."
	msgSubmissionLimit = "Your plan's monthly submission limit has been reached. Upgrade to Pro to accept more submissions."
	msgStorageLimit    = "Your plan's file storage limit has been reached. Upgrade to Pro for more storage."
)

// Usage is what a tenant has used against its plan's limits.
type Usage struct {
	Limits Limits
	// Forms is the number of forms the tenant has.
	Forms int64
	// SubmissionsThisMonth is the number of submissions received since
	// MonthStart.
	SubmissionsThisMonth int64
	// StorageBytes is the total size of the tenant's files.
	StorageBytes int64
}

// Enforcer checks a tenant's usage against its plan's limits.
//
// Check methods return nil when the action is allowed, an AppError with
// CodePlanLimitReached when it is not, and the port's error when counting
// fails.
type Enforcer struct {
	forms       FormCounter
	submissions SubmissionCounter
	storage     StorageMeter
}

// NewEnforcer returns an Enforcer reading usage from the given ports.
func NewEnforcer(f FormCounter, s SubmissionCounter, st StorageMeter) *Enforcer {
	return &Enforcer{forms: f, submissions: s, storage: st}
}

// CheckFormCreate reports whether the tenant may create another form.
func (e *Enforcer) CheckFormCreate(ctx context.Context, tenantID int32, plan tenants.Plan) error {
	limits := For(plan)
	if limits.MaxForms == Unlimited {
		return nil
	}
	n, err := e.formCount(ctx, tenantID)
	if err != nil {
		return err
	}
	if !CanCreateForm(limits, n) {
		return apperrors.NewPlanLimitReached(msgFormLimit)
	}
	return nil
}

// CheckSubmission reports whether the tenant may receive another submission
// in now's calendar month.
func (e *Enforcer) CheckSubmission(ctx context.Context, tenantID int32, plan tenants.Plan, now time.Time) error {
	limits := For(plan)
	if limits.MaxSubmissionsPerMonth == Unlimited {
		return nil
	}
	n, err := e.submissions.CountByTenantSince(ctx, tenantID, MonthStart(now))
	if err != nil {
		return err
	}
	if !CanSubmit(limits, n) {
		return apperrors.NewPlanLimitReached(msgSubmissionLimit)
	}
	return nil
}

// CheckUpload reports whether the tenant may store a file of size bytes. An
// upload that lands exactly on the limit is allowed.
func (e *Enforcer) CheckUpload(ctx context.Context, tenantID int32, plan tenants.Plan, size int64) error {
	limits := For(plan)
	if limits.MaxStorageBytes == Unlimited {
		return nil
	}
	used, err := e.storage.StorageUsedByTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	if !CanStore(limits, used, size) {
		return apperrors.NewPlanLimitReached(msgStorageLimit)
	}
	return nil
}

// Usage returns the tenant's current usage alongside its plan's limits.
func (e *Enforcer) Usage(ctx context.Context, tenantID int32, plan tenants.Plan, now time.Time) (Usage, error) {
	u := Usage{Limits: For(plan)}
	var err error
	if u.Forms, err = e.formCount(ctx, tenantID); err != nil {
		return Usage{}, err
	}
	if u.SubmissionsThisMonth, err = e.submissions.CountByTenantSince(ctx, tenantID, MonthStart(now)); err != nil {
		return Usage{}, err
	}
	if u.StorageBytes, err = e.storage.StorageUsedByTenant(ctx, tenantID); err != nil {
		return Usage{}, err
	}
	return u, nil
}

func (e *Enforcer) formCount(ctx context.Context, tenantID int32) (int64, error) {
	return e.forms.Count(ctx, forms.ListFormsFilter{TenantID: tenantID})
}
