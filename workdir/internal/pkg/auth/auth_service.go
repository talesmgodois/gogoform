package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"app/internal/database"
	"app/internal/db"
	apperrors "app/internal/errors"
	"app/internal/pkg/tenants"
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

// usernameInvalidChars matches what usernamePattern rejects, to derive a
// username from an email local part.
var usernameInvalidChars = regexp.MustCompile(`[^a-z0-9._-]+`)

// Username bounds, as enforced by usernamePattern.
const (
	minUsernameLen = 3
	maxUsernameLen = 50
)

// maxEmailLen is the size of the users.email column (RFC 5321 path limit).
const maxEmailLen = 254

// signUpAttempts bounds how many usernames SignUp tries when the one derived
// from the email is taken.
const signUpAttempts = 5

// Client-facing messages of the errors returned by Service.
const (
	msgInvalidCredentials = "invalid email, username or password"
	msgInvalidToken       = "invalid or expired token"
	msgInvalidUsername    = "username must be 3 to 50 characters: letters, digits, '.', '_' or '-'"
	msgInvalidEmail       = "email must be a valid address of at most 254 characters"
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
	// Tx runs the writes of SignUp (user and tenant) in one transaction;
	// SignUp fails when it is nil.
	Tx database.TxRunner
	// UsersFor returns the Repository to use inside a Tx transaction;
	// NewUserStore when nil.
	UsersFor func(q db.Querier) Repository
	// TenantsFor returns the tenants.Repository to use inside a Tx
	// transaction; tenants.NewService when nil.
	TenantsFor func(q db.Querier) tenants.Repository
}

// Service authenticates users with a password (sign in, HTTP Basic) or a
// token (JWT bearer) and manages their accounts.
type Service struct {
	users  Repository
	tokens tokenSigner
	cost   int
	// tx, usersFor and tenantsFor create accounts atomically.
	tx         database.TxRunner
	usersFor   func(q db.Querier) Repository
	tenantsFor func(q db.Querier) tenants.Repository
	// dummyHash is compared against when the user is unknown, so a failed
	// sign-in takes as long whether or not the user exists.
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
	if opts.UsersFor == nil {
		opts.UsersFor = func(q db.Querier) Repository { return NewUserStore(q) }
	}
	if opts.TenantsFor == nil {
		opts.TenantsFor = func(q db.Querier) tenants.Repository { return tenants.NewService(q) }
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("dummy-password"), opts.PasswordCost)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	return &Service{
		users:      users,
		tokens:     tokenSigner{secret: opts.Secret, ttl: opts.TokenTTL, now: opts.Now},
		cost:       opts.PasswordCost,
		dummyHash:  dummy,
		tx:         opts.Tx,
		usersFor:   opts.UsersFor,
		tenantsFor: opts.TenantsFor,
	}, nil
}

// NormalizeUsername trims and lowercases a username, since usernames are
// case-insensitive.
func NormalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// NormalizeEmail trims and lowercases an email, since emails are
// case-insensitive.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// SignUp creates a user with DefaultRole signing in with email, together with
// its workspace: a tenant named after the email, owned by the user and with a
// fresh API key. Both are created in one transaction, so a failure leaves
// neither behind. Without an explicit username, one is derived from the email
// local part, with a random suffix when it is taken.
func (s *Service) SignUp(ctx context.Context, in SignUpInput) (Account, error) {
	email := NormalizeEmail(in.Email)
	if !validEmail(email) {
		return Account{}, apperrors.NewBadRequest(msgInvalidEmail)
	}
	derived := strings.TrimSpace(in.Username) == ""
	username := in.Username
	if derived {
		username = usernameFromEmail(email)
	}
	cu, err := s.newUser(username, in.Password, DefaultRole)
	if err != nil {
		return Account{}, err
	}
	cu.Email = email
	if s.tx == nil {
		return Account{}, apperrors.NewInternal(errors.New("auth: sign-up needs a transaction runner"))
	}

	base := cu.Username
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			if cu.Username, err = withRandomSuffix(base); err != nil {
				return Account{}, apperrors.NewInternal(err)
			}
		}
		acc, err := s.createAccount(ctx, cu)
		if derived && isUsernameTaken(err) && attempt+1 < signUpAttempts {
			continue
		}
		return acc, err
	}
}

// createAccount stores the user and its tenant in one transaction.
func (s *Service) createAccount(ctx context.Context, in CreateUserInput) (Account, error) {
	apiKey, err := tenants.GenerateAPIKey()
	if err != nil {
		return Account{}, apperrors.NewInternal(err)
	}
	var acc Account
	err = s.tx.InTx(ctx, func(q db.Querier) error {
		u, err := s.usersFor(q).Create(ctx, in)
		if err != nil {
			return err
		}
		t, err := s.tenantsFor(q).Create(ctx, tenants.CreateTenantInput{
			Name:        tenantName(in.Email),
			APIKey:      apiKey,
			OwnerUserID: &u.ID,
		})
		if err != nil {
			return err
		}
		acc = Account{User: u, Tenant: t}
		return nil
	})
	if err != nil {
		if _, ok := apperrors.As(err); !ok {
			err = apperrors.NewInternal(err)
		}
		return Account{}, err
	}
	return acc, nil
}

// EnsureUser creates the user with the given role unless the username is
// already taken; an existing user is never changed. email is optional: ""
// leaves it unset. It is meant for bootstrapping accounts from the
// configuration.
func (s *Service) EnsureUser(ctx context.Context, username, email, password string, role Role) (created bool, err error) {
	if !role.Valid() {
		return false, apperrors.NewBadRequest(msgInvalidRole)
	}
	in, err := s.newUser(username, password, role)
	if err != nil {
		return false, err
	}
	if email != "" {
		if in.Email = NormalizeEmail(email); !validEmail(in.Email) {
			return false, apperrors.NewBadRequest(msgInvalidEmail)
		}
	}
	return s.users.CreateIfNotExists(ctx, in)
}

// SignIn checks the credentials and issues a token. identifier is an email
// or a username, as in CheckPassword.
func (s *Service) SignIn(ctx context.Context, identifier, password string) (Token, error) {
	u, err := s.CheckPassword(ctx, identifier, password)
	if err != nil {
		return Token{}, err
	}
	tok, exp, err := s.tokens.sign(u)
	if err != nil {
		return Token{}, apperrors.NewInternal(err)
	}
	return Token{AccessToken: tok, ExpiresAt: exp, User: u}, nil
}

// CheckPassword returns the active user matching the credentials. identifier
// is looked up as an email when it contains '@' and as a username otherwise,
// so accounts without an email (the bootstrap admin) can still sign in.
// Unknown users, inactive users and wrong passwords all yield the same
// unauthorized error.
func (s *Service) CheckPassword(ctx context.Context, identifier, password string) (User, error) {
	var su StoredUser
	var err error
	if strings.Contains(identifier, "@") {
		su, err = s.users.GetByEmail(ctx, NormalizeEmail(identifier))
	} else {
		su, err = s.users.GetByUsername(ctx, NormalizeUsername(identifier))
	}
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

// validEmail reports whether email, already normalized, is a plain address
// ("user@example.com", no display name) that fits the users.email column.
func validEmail(email string) bool {
	if email == "" || len(email) > maxEmailLen {
		return false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return false
	}
	_, domain, _ := strings.Cut(email, "@")
	return strings.Contains(domain, ".")
}

// usernameFromEmail derives a username matching usernamePattern from the
// local part of email.
func usernameFromEmail(email string) string {
	local, _, _ := strings.Cut(email, "@")
	name := strings.Trim(usernameInvalidChars.ReplaceAllString(local, "-"), "-")
	if len(name) > maxUsernameLen {
		name = name[:maxUsernameLen]
	}
	for len(name) < minUsernameLen {
		name += "_"
	}
	return name
}

// withRandomSuffix returns base with a random "-xxxxxx" suffix, truncating
// base so the result still fits maxUsernameLen.
func withRandomSuffix(base string) (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	suffix := "-" + hex.EncodeToString(b)
	if len(base)+len(suffix) > maxUsernameLen {
		base = base[:maxUsernameLen-len(suffix)]
	}
	return base + suffix, nil
}

// tenantName names the workspace of a new account after its email.
func tenantName(email string) string {
	if r := []rune(email); len(r) > tenants.MaxNameLen {
		return string(r[:tenants.MaxNameLen])
	}
	return email
}

func isUsernameTaken(err error) bool {
	appErr, ok := apperrors.As(err)
	return ok && appErr.Code == apperrors.CodeConflict && appErr.Message == msgUsernameTaken
}

func isNotFound(err error) bool {
	appErr, ok := apperrors.As(err)
	return ok && appErr.Code == apperrors.CodeNotFound
}
