package auth

import (
	"context"

	"app/internal/database"
	"app/internal/db"
	apperrors "app/internal/errors"
)

// Client-facing messages of the errors returned by UserStore.
const (
	msgUserNotFound  = "user not found"
	msgUsernameTaken = "username already taken"
	msgInvalidPage   = "page offset must be >= 0 and limit must be > 0"
)

var _ Repository = (*UserStore)(nil)

// UserStore implements Repository on top of the sqlc queries.
type UserStore struct {
	q db.Querier
}

// NewUserStore returns a UserStore that runs its queries through q.
func NewUserStore(q db.Querier) *UserStore {
	return &UserStore{q: q}
}

// Create stores a new, active user and returns it.
func (s *UserStore) Create(ctx context.Context, in CreateUserInput) (User, error) {
	row, err := s.q.CreateUser(ctx, db.CreateUserParams{
		Username:     in.Username,
		PasswordHash: &in.PasswordHash,
		Role:         db.UserRole(in.Role),
	})
	switch {
	case database.IsUniqueViolation(err):
		return User{}, apperrors.NewConflict(msgUsernameTaken)
	case err != nil:
		return User{}, apperrors.NewInternal(err)
	}
	return toUser(row), nil
}

// CreateIfNotExists stores the user unless the username is taken.
func (s *UserStore) CreateIfNotExists(ctx context.Context, in CreateUserInput) (bool, error) {
	n, err := s.q.CreateUserIfNotExists(ctx, db.CreateUserIfNotExistsParams{
		Username:     in.Username,
		PasswordHash: &in.PasswordHash,
		Role:         db.UserRole(in.Role),
	})
	if err != nil {
		return false, apperrors.NewInternal(err)
	}
	return n == 1, nil
}

// GetByUsername returns the active user with the given username and its
// password hash.
func (s *UserStore) GetByUsername(ctx context.Context, username string) (StoredUser, error) {
	row, err := s.q.GetUserByUsername(ctx, username)
	if err != nil {
		return StoredUser{}, mapGetError(err)
	}
	su := StoredUser{User: toUser(row)}
	if row.PasswordHash != nil {
		su.PasswordHash = *row.PasswordHash
	}
	return su, nil
}

// GetByID returns the active user with the given ID.
func (s *UserStore) GetByID(ctx context.Context, id int32) (User, error) {
	row, err := s.q.GetUserByID(ctx, id)
	if err != nil {
		return User{}, mapGetError(err)
	}
	return toUser(row), nil
}

// List returns every user by ID.
func (s *UserStore) List(ctx context.Context, page Page) ([]User, error) {
	if page.Offset < 0 || page.Limit < 1 {
		return nil, apperrors.NewBadRequest(msgInvalidPage)
	}
	rows, err := s.q.ListUsers(ctx, db.ListUsersParams{PageOffset: page.Offset, PageLimit: page.Limit})
	if err != nil {
		return nil, apperrors.NewInternal(err)
	}
	users := make([]User, len(rows))
	for i, row := range rows {
		users[i] = toUser(row)
	}
	return users, nil
}

// UpdateRole changes the role of a user and returns it.
func (s *UserStore) UpdateRole(ctx context.Context, id int32, role Role) (User, error) {
	row, err := s.q.UpdateUserRole(ctx, db.UpdateUserRoleParams{ID: id, Role: db.UserRole(role)})
	if err != nil {
		return User{}, mapGetError(err)
	}
	return toUser(row), nil
}

// GetByIdentity returns the user linked to the external identity, active or
// not.
func (s *UserStore) GetByIdentity(ctx context.Context, issuer, subject string) (User, error) {
	row, err := s.q.GetUserByIdentity(ctx, db.GetUserByIdentityParams{Issuer: issuer, Subject: subject})
	if err != nil {
		return User{}, mapGetError(err)
	}
	return toUser(row), nil
}

// CreateWithIdentity atomically stores a user without a password and links
// the external identity to it.
func (s *UserStore) CreateWithIdentity(ctx context.Context, in CreateExternalUserInput) (User, error) {
	row, err := s.q.CreateUserWithIdentity(ctx, db.CreateUserWithIdentityParams{
		Username: in.Username,
		Role:     db.UserRole(in.Role),
		Issuer:   in.Issuer,
		Subject:  in.Subject,
		Email:    optionalString(in.Email),
	})
	switch {
	case database.IsUniqueViolation(err):
		return User{}, apperrors.NewConflict(msgUsernameTaken)
	case err != nil:
		return User{}, apperrors.NewInternal(err)
	}
	return toUser(db.User(row)), nil
}

// TouchIdentity records a sign-in through the identity.
func (s *UserStore) TouchIdentity(ctx context.Context, issuer, subject, email string) error {
	err := s.q.TouchUserIdentity(ctx, db.TouchUserIdentityParams{Issuer: issuer, Subject: subject, Email: optionalString(email)})
	if err != nil {
		return apperrors.NewInternal(err)
	}
	return nil
}

// CountIdentities returns how many external identities are linked to the
// user.
func (s *UserStore) CountIdentities(ctx context.Context, userID int32) (int, error) {
	n, err := s.q.CountUserIdentities(ctx, userID)
	if err != nil {
		return 0, apperrors.NewInternal(err)
	}
	return int(n), nil
}

// optionalString maps "" to NULL.
func optionalString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// mapGetError translates the error of a single-row user query.
func mapGetError(err error) error {
	if database.IsNoRows(err) {
		return apperrors.NewNotFound(msgUserNotFound)
	}
	return apperrors.NewInternal(err)
}

func toUser(row db.User) User {
	return User{
		ID:        row.ID,
		Username:  row.Username,
		Role:      Role(row.Role),
		IsActive:  row.IsActive,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}
