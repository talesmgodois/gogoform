package files

import "context"

// Repository stores files as database blobs. Reads are not scoped by tenant
// (files are served publicly by their unguessable ID); removals are.
//
// Errors are AppErrors from internal/errors: CodeNotFound when the file (or
// the tenant, on Create) does not exist and CodeInternal for anything else.
type Repository interface {
	// Create stores a new file and returns it without its Data.
	Create(ctx context.Context, in CreateFileInput) (File, error)
	// GetByID returns the file with the given ID, including its Data.
	GetByID(ctx context.Context, id string) (File, error)
	// Delete removes the tenant's file with the given ID.
	Delete(ctx context.Context, tenantID int32, id string) error
}
