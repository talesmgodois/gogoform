package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
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
	msgPasswordDisabled   = "password sign-in is disabled; sign in through the identity provider"
	msgExternalInvalid    = "the identity provider did not identify the user"
	msgExternalInactive   = "this account is disabled"
	msgExternalUnknown    = "no account is linked to this identity"
	msgNoFreeUsername     = "could not find a free username for the new account"
)

// maxUsernameAttempts bounds how many numeric suffixes SignInExternal tries
// when the username derived from an identity is taken.
const maxUsernameAttempts = 100

// fallbackUsername names accounts whose identity carries no usable name.
const fallbackUsername = "user"

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
	// PasswordLoginDisabled rejects password sign-in, sign-up and HTTP
	// Basic credentials, for SSO-only deployments. EnsureUser still creates
	// accounts, so a bootstrap admin can exist.
	PasswordLoginDisabled bool
	// External configures SignInExternal.
	External ExternalOptions
}

// ExternalOptions configures sign-in through an external identity provider.
type ExternalOptions struct {
	// AutoCreateUsers creates an account on the first sign-in of an
	// identity. When false, only identities already linked may sign in.
	AutoCreateUsers bool
	// DefaultRole is the role of the accounts created that way; DefaultRole
	// when empty.
	DefaultRole Role
}

// Service authenticates users with a password (sign in, HTTP Basic) or a
// token (JWT bearer) and manages their accounts.
type Service struct {
	users  Repository
	tokens tokenSigner
	cost   int
	// passwordLogin is false when Options.PasswordLoginDisabled is set.
	passwordLogin bool
	external      ExternalOptions
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
	if opts.External.DefaultRole == "" {
		opts.External.DefaultRole = DefaultRole
	}
	if !opts.External.DefaultRole.Valid() {
		return nil, fmt.Errorf("auth: invalid default role %q for external users", opts.External.DefaultRole)
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("dummy-password"), opts.PasswordCost)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	return &Service{
		users:         users,
		tokens:        tokenSigner{secret: opts.Secret, ttl: opts.TokenTTL, now: opts.Now},
		cost:          opts.PasswordCost,
		dummyHash:     dummy,
		passwordLogin: !opts.PasswordLoginDisabled,
		external:      opts.External,
	}, nil
}

// NormalizeUsername trims and lowercases a username, since usernames are
// case-insensitive.
func NormalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// PasswordLoginEnabled reports whether users may sign in and sign up with a
// password.
func (s *Service) PasswordLoginEnabled() bool {
	return s.passwordLogin
}

// SignUp creates a user with DefaultRole.
func (s *Service) SignUp(ctx context.Context, username, password string) (User, error) {
	if !s.passwordLogin {
		return User{}, apperrors.NewForbidden(msgPasswordDisabled)
	}
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
	return s.issue(u)
}

// SignInExternal issues a token to the user linked to an identity verified
// by an external provider. An unknown identity gets a new account when
// ExternalOptions.AutoCreateUsers is set. Accounts are only ever found by
// (issuer, subject): never by email or username, which a provider may let
// people choose, so an identity can't take over an existing account.
func (s *Service) SignInExternal(ctx context.Context, id ExternalIdentity) (Token, error) {
	if id.Issuer == "" || id.Subject == "" {
		return Token{}, apperrors.NewUnauthorized(msgExternalInvalid)
	}
	u, err := s.users.GetByIdentity(ctx, id.Issuer, id.Subject)
	switch {
	case err == nil:
		if !u.IsActive {
			return Token{}, apperrors.NewForbidden(msgExternalInactive)
		}
		if err := s.users.TouchIdentity(ctx, id.Issuer, id.Subject, id.Email); err != nil {
			return Token{}, err
		}
	case !isNotFound(err):
		return Token{}, err
	case !s.external.AutoCreateUsers:
		return Token{}, apperrors.NewForbidden(msgExternalUnknown)
	default:
		if u, err = s.createExternalUser(ctx, id); err != nil {
			return Token{}, err
		}
	}
	return s.issue(u)
}

// LinkedIdentities returns how many external identities are linked to the
// user.
func (s *Service) LinkedIdentities(ctx context.Context, userID int32) (int, error) {
	return s.users.CountIdentities(ctx, userID)
}

// createExternalUser stores a user for id under the first free username
// derived from it: the base name, then base2, base3 and so on.
func (s *Service) createExternalUser(ctx context.Context, id ExternalIdentity) (User, error) {
	base := externalUsername(id)
	for attempt := 1; attempt <= maxUsernameAttempts; attempt++ {
		u, err := s.users.CreateWithIdentity(ctx, CreateExternalUserInput{
			Username: usernameWithSuffix(base, attempt),
			Role:     s.external.DefaultRole,
			Issuer:   id.Issuer,
			Subject:  id.Subject,
			Email:    id.Email,
		})
		if err == nil {
			return u, nil
		}
		if !isConflict(err) {
			return User{}, err
		}
		// The identity itself may have just been linked by a concurrent
		// first sign-in: then that account is the one to use.
		if existing, err := s.users.GetByIdentity(ctx, id.Issuer, id.Subject); err == nil {
			if !existing.IsActive {
				return User{}, apperrors.NewForbidden(msgExternalInactive)
			}
			return existing, nil
		}
	}
	return User{}, apperrors.NewConflict(msgNoFreeUsername)
}

// externalUsername derives a valid username from the first usable of the
// preferred username, the local part of the email, or fallbackUsername.
func externalUsername(id ExternalIdentity) string {
	local, _, _ := strings.Cut(id.Email, "@")
	for _, candidate := range []string{id.PreferredUsername, local} {
		if name := sanitizeUsername(candidate); usernamePattern.MatchString(name) {
			return name
		}
	}
	return fallbackUsername
}

// sanitizeUsername lowercases s and maps it onto usernamePattern's
// alphabet: runs of other characters become a single '_', which is also
// trimmed from both ends. The result is capped at 50 bytes.
func sanitizeUsername(s string) string {
	var b strings.Builder
	pendingSep := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			if pendingSep && b.Len() > 0 {
				b.WriteByte('_')
			}
			pendingSep = false
			b.WriteRune(r)
			continue
		}
		pendingSep = true
	}
	name := strings.Trim(b.String(), "_")
	if len(name) > 50 {
		name = strings.TrimRight(name[:50], "_")
	}
	return name
}

// usernameWithSuffix returns base for the first attempt and base followed by
// the attempt number otherwise, shortening base to stay within 50 bytes.
func usernameWithSuffix(base string, attempt int) string {
	if attempt <= 1 {
		return base
	}
	suffix := strconv.Itoa(attempt)
	if len(base)+len(suffix) > 50 {
		base = base[:50-len(suffix)]
	}
	return base + suffix
}

// issue signs a token for u.
func (s *Service) issue(u User) (Token, error) {
	tok, exp, err := s.tokens.sign(u)
	if err != nil {
		return Token{}, apperrors.NewInternal(err)
	}
	return Token{AccessToken: tok, ExpiresAt: exp, User: u}, nil
}

// CheckPassword returns the active user matching the credentials. Unknown
// users, inactive users, users without a password and wrong passwords all
// yield the same unauthorized error, in about the same time.
func (s *Service) CheckPassword(ctx context.Context, username, password string) (User, error) {
	if !s.passwordLogin {
		return User{}, apperrors.NewForbidden(msgPasswordDisabled)
	}
	su, err := s.users.GetByUsername(ctx, NormalizeUsername(username))
	if err != nil {
		if !isNotFound(err) {
			return User{}, err
		}
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return User{}, apperrors.NewUnauthorized(msgInvalidCredentials)
	}
	if su.PasswordHash == "" {
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

func isConflict(err error) bool {
	appErr, ok := apperrors.As(err)
	return ok && appErr.Code == apperrors.CodeConflict
}
