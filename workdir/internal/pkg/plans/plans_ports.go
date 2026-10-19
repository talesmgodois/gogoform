package plans

import (
	"context"
	"time"

	"app/internal/pkg/forms"
)

// FormCounter counts a tenant's forms; forms.Repository satisfies it.
type FormCounter interface {
	Count(ctx context.Context, filter forms.ListFormsFilter) (int64, error)
}

// SubmissionCounter counts a tenant's submissions; submissions.Repository
// satisfies it.
type SubmissionCounter interface {
	CountByTenantSince(ctx context.Context, tenantID int32, since time.Time) (int64, error)
}

// StorageMeter measures a tenant's file storage; files.Repository satisfies it.
type StorageMeter interface {
	StorageUsedByTenant(ctx context.Context, tenantID int32) (int64, error)
}
