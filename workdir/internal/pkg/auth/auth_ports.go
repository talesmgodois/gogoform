package auth

import "context"

// Repository persists users.
//
// Errors are AppErrors from internal/errors: CodeNotFound when no matching
// active user exists, CodeConflict when a username is taken, CodeInvalidArgument
// for a bad Page and CodeInternal for anything else.
type Repository interface {
	// Create stores a new, active user and returns it.
	Create(ctx context.Context, in CreateUserInput) (User, error)
	// CreateIfNotExists stores the user unless the username is taken, in
	// which case the existing user is left untouched and created is false.
	CreateIfNotExists(ctx context.Context, in CreateUserInput) (created bool, err error)
	// GetByUsername returns the active user with the given username, along
	// with its password hash.
	GetByUsername(ctx context.Context, username string) (StoredUser, error)
	// GetByID returns the active user with the given ID.
	GetByID(ctx context.Context, id int32) (User, error)
	// List returns every user, active or not, by ID.
	List(ctx context.Context, page Page) ([]User, error)
	// UpdateRole changes the role of a user and returns it.
	UpdateRole(ctx context.Context, id int32, role Role) (User, error)

	// GetByIdentity returns the user linked to the external identity
	// (issuer, subject), active or not: callers must check IsActive.
	GetByIdentity(ctx context.Context, issuer, subject string) (User, error)
	// CreateWithIdentity atomically stores a new, active user without a
	// password and links the external identity to it. CodeConflict means
	// the username or the identity is taken.
	CreateWithIdentity(ctx context.Context, in CreateExternalUserInput) (User, error)
	// TouchIdentity records a sign-in through the identity and refreshes
	// its email (empty for none).
	TouchIdentity(ctx context.Context, issuer, subject, email string) error
	// CountIdentities returns how many external identities are linked to
	// the user.
	CountIdentities(ctx context.Context, userID int32) (int, error)
}
