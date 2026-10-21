package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
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
	msgWrongPassword      = "current password is incorrect"
	msgSameEmail          = "this is already your email address"
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
	// TokensFor returns the TokenRepository to use inside a Tx transaction;
	// NewUserTokenStore when nil.
	TokensFor func(q db.Querier) TokenRepository
	// Notifier sends the verification and password-reset emails; a no-op
	// when nil.
	Notifier Notifier
}

// noopNotifier discards every notification; the default when Options.Notifier
// is unset, so services that do not configure email still work.
type noopNotifier struct{}

func (noopNotifier) SendEmailVerification(context.Context, User, string) error { return nil }
func (noopNotifier) SendPasswordReset(context.Context, User, string) error     { return nil }

// Service authenticates users with a password (sign in, HTTP Basic) or a
// token (JWT bearer) and manages their accounts.
type Service struct {
	users      Repository
	userTokens TokenRepository
	tokens     tokenSigner
	cost       int
	notifier   Notifier
	// tx, usersFor, tenantsFor and tokensFor create accounts atomically.
	tx         database.TxRunner
	usersFor   func(q db.Querier) Repository
	tenantsFor func(q db.Querier) tenants.Repository
	tokensFor  func(q db.Querier) TokenRepository
	// dummyHash is compared against when the user is unknown, so a failed
	// sign-in takes as long whether or not the user exists.
	dummyHash []byte
	now       func() time.Time
}

// NewService returns a Service storing users in users and their email
// verification and password reset tokens in userTokens.
func NewService(users Repository, userTokens TokenRepository, opts Options) (*Service, error) {
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
	if opts.TokensFor == nil {
		opts.TokensFor = func(q db.Querier) TokenRepository { return NewUserTokenStore(q) }
	}
	if opts.Notifier == nil {
		opts.Notifier = noopNotifier{}
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("dummy-password"), opts.PasswordCost)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	return &Service{
		users:      users,
		userTokens: userTokens,
		tokens:     tokenSigner{secret: opts.Secret, ttl: opts.TokenTTL, now: opts.Now},
		cost:       opts.PasswordCost,
		notifier:   opts.Notifier,
		dummyHash:  dummy,
		tx:         opts.Tx,
		usersFor:   opts.UsersFor,
		tenantsFor: opts.TenantsFor,
		tokensFor:  opts.TokensFor,
		now:        opts.Now,
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

// createAccount stores the user, its tenant and a fresh email verification
// token in one transaction, then notifies the user once it commits: a
// failure to send never rolls the account creation back.
func (s *Service) createAccount(ctx context.Context, in CreateUserInput) (Account, error) {
	apiKey, err := tenants.GenerateAPIKey()
	if err != nil {
		return Account{}, apperrors.NewInternal(err)
	}
	var acc Account
	var verificationToken string
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
		verificationToken, err = issueToken(ctx, s.tokensFor(q), u.ID, TokenPurposeEmailVerification, EmailVerificationTokenTTL, s.now())
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
	if acc.User.Email != "" {
		s.notify(ctx, func() error { return s.notifier.SendEmailVerification(ctx, acc.User, verificationToken) })
	}
	return acc, nil
}

// notify runs send, logging rather than propagating a failure: notifications
// must never fail or delay the caller's request.
func (s *Service) notify(ctx context.Context, send func() error) {
	if err := send(); err != nil {
		slog.ErrorContext(ctx, "auth: notification failed", slog.Any("error", err))
	}
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

// GetUser returns the active user with the given ID.
func (s *Service) GetUser(ctx context.Context, id int32) (User, error) {
	return s.users.GetByID(ctx, id)
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

// ResendVerification issues a fresh email verification token for userID and
// sends it; a no-op when the email is already verified.
func (s *Service) ResendVerification(ctx context.Context, userID int32) error {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if u.EmailVerified() || u.Email == "" {
		return nil
	}
	token, err := issueToken(ctx, s.userTokens, u.ID, TokenPurposeEmailVerification, EmailVerificationTokenTTL, s.now())
	if err != nil {
		return err
	}
	s.notify(ctx, func() error { return s.notifier.SendEmailVerification(ctx, u, token) })
	return nil
}

// VerifyEmail redeems an email verification token and marks the owning
// user's email verified.
func (s *Service) VerifyEmail(ctx context.Context, token string) (User, error) {
	ut, reason, err := consumeToken(ctx, s.userTokens, token, TokenPurposeEmailVerification, s.now())
	if err != nil {
		s.logTokenFailure(ctx, "email verification", reason)
		return User{}, err
	}
	return s.users.MarkEmailVerified(ctx, ut.UserID)
}

// RequestPasswordReset issues a 1h password-reset token for email and sends
// it, invalidating any earlier reset tokens of the same account. It returns
// nil whether or not an account with that email exists, and does
// comparable work either way, so callers cannot use it to probe which
// emails are registered.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	su, err := s.users.GetByEmail(ctx, NormalizeEmail(email))
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}
	token, err := issueToken(ctx, s.userTokens, su.ID, TokenPurposePasswordReset, PasswordResetTokenTTL, s.now())
	if err != nil {
		return err
	}
	s.notify(ctx, func() error { return s.notifier.SendPasswordReset(ctx, su.User, token) })
	return nil
}

// ResetPassword redeems a password-reset token, single use, and sets
// newPassword on the user it was issued to, invalidating any remaining
// reset tokens and marking the email verified: only the owner of the inbox
// could have received the token.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword string) error {
	if len(newPassword) < minPasswordLen || len(newPassword) > maxPasswordLen {
		return apperrors.NewBadRequest(msgInvalidPassword)
	}
	ut, reason, err := consumeToken(ctx, s.userTokens, token, TokenPurposePasswordReset, s.now())
	if err != nil {
		s.logTokenFailure(ctx, "password reset", reason)
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.cost)
	if err != nil {
		return apperrors.NewInternal(err)
	}
	if _, err := s.users.UpdatePassword(ctx, ut.UserID, string(hash)); err != nil {
		return err
	}
	if err := s.userTokens.Invalidate(ctx, ut.UserID, TokenPurposePasswordReset); err != nil {
		return err
	}
	_, err = s.users.MarkEmailVerified(ctx, ut.UserID)
	return err
}

// ChangePassword sets newPassword on the user userID once currentPassword
// is checked, and revokes the password-reset tokens still pending: a link
// sent before the change must not undo it.
func (s *Service) ChangePassword(ctx context.Context, userID int32, currentPassword, newPassword string) error {
	if len(newPassword) < minPasswordLen || len(newPassword) > maxPasswordLen {
		return apperrors.NewBadRequest(msgInvalidPassword)
	}
	if _, err := s.checkOwnPassword(ctx, userID, currentPassword); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.cost)
	if err != nil {
		return apperrors.NewInternal(err)
	}
	if _, err := s.users.UpdatePassword(ctx, userID, string(hash)); err != nil {
		return err
	}
	return s.userTokens.Invalidate(ctx, userID, TokenPurposePasswordReset)
}

// ChangeEmail replaces the email of the user userID with newEmail once
// currentPassword is checked. The new address is unverified until the
// owner redeems the verification link sent to it; the password-reset links
// sent to the old address are revoked. An email registered to another
// account gets a Conflict.
func (s *Service) ChangeEmail(ctx context.Context, userID int32, currentPassword, newEmail string) (User, error) {
	email := NormalizeEmail(newEmail)
	if !validEmail(email) {
		return User{}, apperrors.NewBadRequest(msgInvalidEmail)
	}
	u, err := s.checkOwnPassword(ctx, userID, currentPassword)
	if err != nil {
		return User{}, err
	}
	if u.Email == email {
		return User{}, apperrors.NewBadRequest(msgSameEmail)
	}
	if u, err = s.users.UpdateEmail(ctx, userID, email); err != nil {
		return User{}, err
	}
	if err := s.userTokens.Invalidate(ctx, userID, TokenPurposePasswordReset); err != nil {
		return User{}, err
	}
	token, err := issueToken(ctx, s.userTokens, u.ID, TokenPurposeEmailVerification, EmailVerificationTokenTTL, s.now())
	if err != nil {
		return User{}, err
	}
	s.notify(ctx, func() error { return s.notifier.SendEmailVerification(ctx, u, token) })
	return u, nil
}

// checkOwnPassword returns the active user userID when password is theirs,
// and an InvalidArgument otherwise: the caller is already signed in, so a
// wrong password is a form error rather than a failed authentication.
func (s *Service) checkOwnPassword(ctx context.Context, userID int32, password string) (User, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return User{}, err
	}
	su, err := s.users.GetByUsername(ctx, u.Username)
	if err != nil {
		return User{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(su.PasswordHash), []byte(password)) != nil {
		return User{}, apperrors.NewBadRequest(msgWrongPassword)
	}
	return su.User, nil
}

// logTokenFailure logs the internal reason a token redemption was rejected;
// the client only ever sees msgInvalidToken.
func (s *Service) logTokenFailure(ctx context.Context, flow string, reason tokenFailureReason) {
	if reason != reasonNone {
		slog.WarnContext(ctx, "auth: token redemption failed", slog.String("flow", flow), slog.String("reason", string(reason)))
	}
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
