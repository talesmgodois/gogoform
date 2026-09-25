package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	apperrors "app/internal/errors"
)

// MinSecretLen is the minimum length of the JWT signing secret: 256 bits,
// the size of the HS256 key.
const MinSecretLen = 32

// DefaultTokenTTL is how long tokens are valid when Options.TokenTTL is unset.
const DefaultTokenTTL = time.Hour

// Password length bounds; bcrypt ignores anything past 72 bytes.
const (
	minPasswordLen = 8
	maxPasswordLen = 72
)

// usernamePattern is the accepted shape of a normalized username. It rules
// out ':' because HTTP Basic credentials are "username:password".
var usernamePattern = regexp.MustCompile(`^[a-z0-9._-]{3,50}$`)

// Client-facing messages of the errors returned by Service.
const (
	msgInvalidCredentials = "invalid username or password"
	msgInvalidToken       = "invalid or expired token"
	msgInvalidUsername    = "username must be 3 to 50 characters: letters, digits, '.', '_' or '-'"
	msgInvalidPassword    = "password must be 8 to 72 bytes long"
	msgInvalidRole        = "role must be one of ADMIN, FORM_CREATOR, BASIC"
	msgOwnRole            = "you cannot change your own role"
)

// Options configures a Service.
type Options struct {
	// Secret signs the tokens; at least MinSecretLen bytes.
	Secret []byte
	// TokenTTL is how long issued tokens are valid; DefaultTokenTTL when 0.
	TokenTTL time.Duration
	// PasswordCost is the bcrypt cost; bcrypt.DefaultCost when 0.
	PasswordCost int
	// Now returns the current time; time.Now when nil.
	Now func() time.Time
}

// Service authenticates users with a password (sign in, HTTP Basic) or a
// token (JWT bearer) and manages their accounts.
type Service struct {
	users  Repository
	tokens tokenSigner
	cost   int
	// dummyHash is compared against when the username is unknown, so a
	// failed sign-in takes as long whether or not the user exists.
	dummyHash []byte
}

// NewService returns a Service storing users in users.
func NewService(users Repository, opts Options) (*Service, error) {
	if len(opts.Secret) < MinSecretLen {
		return nil, fmt.Errorf("auth: secret must be at least %d bytes", MinSecretLen)
	}
	if opts.TokenTTL == 0 {
		opts.TokenTTL = DefaultTokenTTL
	}
	if opts.TokenTTL < 0 {
		return nil, errors.New("auth: token TTL must be positive")
	}
	if opts.PasswordCost == 0 {
		opts.PasswordCost = bcrypt.DefaultCost
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("dummy-password"), opts.PasswordCost)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	return &Service{
		users:     users,
		tokens:    tokenSigner{secret: opts.Secret, ttl: opts.TokenTTL, now: opts.Now},
		cost:      opts.PasswordCost,
		dummyHash: dummy,
	}, nil
}

// NormalizeUsername trims and lowercases a username, since usernames are
// case-insensitive.
func NormalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// SignUp creates a user with DefaultRole.
func (s *Service) SignUp(ctx context.Context, username, password string) (User, error) {
	in, err := s.newUser(username, password, DefaultRole)
	if err != nil {
		return User{}, err
	}
	return s.users.Create(ctx, in)
}

// EnsureUser creates the user with the given role unless the username is
// already taken; an existing user is never changed. It is meant for
// bootstrapping accounts from the configuration.
func (s *Service) EnsureUser(ctx context.Context, username, password string, role Role) (created bool, err error) {
	if !role.Valid() {
		return false, apperrors.NewBadRequest(msgInvalidRole)
	}
	in, err := s.newUser(username, password, role)
	if err != nil {
		return false, err
	}
	return s.users.CreateIfNotExists(ctx, in)
}

// SignIn checks the credentials and issues a token.
func (s *Service) SignIn(ctx context.Context, username, password string) (Token, error) {
	u, err := s.CheckPassword(ctx, username, password)
	if err != nil {
		return Token{}, err
	}
	tok, exp, err := s.tokens.sign(u)
	if err != nil {
		return Token{}, apperrors.NewInternal(err)
	}
	return Token{AccessToken: tok, ExpiresAt: exp, User: u}, nil
}

// CheckPassword returns the active user matching the credentials. Unknown
// users, inactive users and wrong passwords all yield the same unauthorized
// error.
func (s *Service) CheckPassword(ctx context.Context, username, password string) (User, error) {
	su, err := s.users.GetByUsername(ctx, NormalizeUsername(username))
	if err != nil {
		if !isNotFound(err) {
			return User{}, err
		}
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return User{}, apperrors.NewUnauthorized(msgInvalidCredentials)
	}
	if bcrypt.CompareHashAndPassword([]byte(su.PasswordHash), []byte(password)) != nil {
		return User{}, apperrors.NewUnauthorized(msgInvalidCredentials)
	}
	return su.User, nil
}

// CheckToken returns the active user a valid token was issued to, with its
// current role.
func (s *Service) CheckToken(ctx context.Context, token string) (User, error) {
	id, err := s.tokens.verify(token)
	if err != nil {
		return User{}, apperrors.NewUnauthorized(msgInvalidToken)
	}
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		if isNotFound(err) {
			return User{}, apperrors.NewUnauthorized(msgInvalidToken)
		}
		return User{}, err
	}
	return u, nil
}

// ListUsers returns every user by ID.
func (s *Service) ListUsers(ctx context.Context, page Page) ([]User, error) {
	return s.users.List(ctx, page)
}

// SetRole changes the role of the user with the given ID on behalf of actor.
// Actors cannot change their own role, so the last admin cannot demote
// themselves by mistake.
func (s *Service) SetRole(ctx context.Context, actor User, id int32, role Role) (User, error) {
	if !role.Valid() {
		return User{}, apperrors.NewBadRequest(msgInvalidRole)
	}
	if actor.ID == id {
		return User{}, apperrors.NewBadRequest(msgOwnRole)
	}
	return s.users.UpdateRole(ctx, id, role)
}

// newUser validates the credentials and hashes the password.
func (s *Service) newUser(username, password string, role Role) (CreateUserInput, error) {
	username = NormalizeUsername(username)
	if !usernamePattern.MatchString(username) {
		return CreateUserInput{}, apperrors.NewBadRequest(msgInvalidUsername)
	}
	if len(password) < minPasswordLen || len(password) > maxPasswordLen {
		return CreateUserInput{}, apperrors.NewBadRequest(msgInvalidPassword)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.cost)
	if err != nil {
		return CreateUserInput{}, apperrors.NewInternal(err)
	}
	return CreateUserInput{Username: username, PasswordHash: string(hash), Role: role}, nil
}

func isNotFound(err error) bool {
	appErr, ok := apperrors.As(err)
	return ok && appErr.Code == apperrors.CodeNotFound
}
