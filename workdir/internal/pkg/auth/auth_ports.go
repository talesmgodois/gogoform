package auth

import "context"

// Repository persists users.
//
// Errors are AppErrors from internal/errors: CodeNotFound when no matching
// active user exists, CodeConflict when a username or email is taken, CodeInvalidArgument
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
	// GetByEmail returns the active user with the given email, along with
	// its password hash.
	GetByEmail(ctx context.Context, email string) (StoredUser, error)
	// GetByID returns the active user with the given ID.
	GetByID(ctx context.Context, id int32) (User, error)
	// List returns every user, active or not, by ID.
	List(ctx context.Context, page Page) ([]User, error)
	// UpdateRole changes the role of a user and returns it.
	UpdateRole(ctx context.Context, id int32, role Role) (User, error)
	// MarkEmailVerified sets the email_verified_at of a user to now and
	// returns it.
	MarkEmailVerified(ctx context.Context, id int32) (User, error)
	// UpdatePassword replaces the password hash of a user and returns it.
	UpdatePassword(ctx context.Context, id int32, passwordHash string) (User, error)
}

// Notifier sends the transactional emails issued by the auth flows. Sending
// is expected to be asynchronous (see internal/pkg/mailer.Async), so a slow
// or failing delivery never blocks or fails the caller.
type Notifier interface {
	// SendEmailVerification sends the link redeeming token at
	// GET /app/verify?token=.
	SendEmailVerification(ctx context.Context, user User, token string) error
	// SendPasswordReset sends the link redeeming token at
	// GET /app/reset-password?token=.
	SendPasswordReset(ctx context.Context, user User, token string) error
}
