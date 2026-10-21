package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"app/internal/database"
	"app/internal/db"
	apperrors "app/internal/errors"
	"app/internal/pkg/tenants"
)

var testSecret = []byte(strings.Repeat("k", MinSecretLen))

// memUsers is an in-memory Repository.
type memUsers struct {
	users []StoredUser
}

func (m *memUsers) Create(_ context.Context, in CreateUserInput) (User, error) {
	for _, u := range m.users {
		if in.Email != "" && u.Email == in.Email {
			return User{}, apperrors.NewConflict(msgEmailTaken)
		}
		if u.Username == in.Username {
			return User{}, apperrors.NewConflict(msgUsernameTaken)
		}
	}
	u := StoredUser{User: User{ID: int32(len(m.users) + 1), Username: in.Username, Email: in.Email, Role: in.Role, IsActive: true}, PasswordHash: in.PasswordHash}
	m.users = append(m.users, u)
	return u.User, nil
}

func (m *memUsers) CreateIfNotExists(ctx context.Context, in CreateUserInput) (bool, error) {
	if _, err := m.Create(ctx, in); err != nil {
		return false, nil
	}
	return true, nil
}

func (m *memUsers) GetByUsername(_ context.Context, username string) (StoredUser, error) {
	for _, u := range m.users {
		if u.Username == username && u.IsActive {
			return u, nil
		}
	}
	return StoredUser{}, apperrors.NewNotFound(msgUserNotFound)
}

func (m *memUsers) GetByEmail(_ context.Context, email string) (StoredUser, error) {
	for _, u := range m.users {
		if u.Email != "" && u.Email == email && u.IsActive {
			return u, nil
		}
	}
	return StoredUser{}, apperrors.NewNotFound(msgUserNotFound)
}

func (m *memUsers) GetByID(_ context.Context, id int32) (User, error) {
	for _, u := range m.users {
		if u.ID == id && u.IsActive {
			return u.User, nil
		}
	}
	return User{}, apperrors.NewNotFound(msgUserNotFound)
}

func (m *memUsers) List(context.Context, Page) ([]User, error) {
	out := make([]User, len(m.users))
	for i, u := range m.users {
		out[i] = u.User
	}
	return out, nil
}

func (m *memUsers) UpdateRole(_ context.Context, id int32, role Role) (User, error) {
	for i := range m.users {
		if m.users[i].ID == id {
			m.users[i].Role = role
			return m.users[i].User, nil
		}
	}
	return User{}, apperrors.NewNotFound(msgUserNotFound)
}

func (m *memUsers) MarkEmailVerified(_ context.Context, id int32) (User, error) {
	for i := range m.users {
		if m.users[i].ID == id {
			now := time.Now()
			m.users[i].EmailVerifiedAt = &now
			return m.users[i].User, nil
		}
	}
	return User{}, apperrors.NewNotFound(msgUserNotFound)
}

func (m *memUsers) UpdatePassword(_ context.Context, id int32, passwordHash string) (User, error) {
	for i := range m.users {
		if m.users[i].ID == id {
			m.users[i].PasswordHash = passwordHash
			return m.users[i].User, nil
		}
	}
	return User{}, apperrors.NewNotFound(msgUserNotFound)
}

func (m *memUsers) UpdateEmail(_ context.Context, id int32, email string) (User, error) {
	for _, u := range m.users {
		if u.ID != id && u.Email != "" && u.Email == email {
			return User{}, apperrors.NewConflict(msgEmailTaken)
		}
	}
	for i := range m.users {
		if m.users[i].ID == id {
			m.users[i].Email, m.users[i].EmailVerifiedAt = email, nil
			return m.users[i].User, nil
		}
	}
	return User{}, apperrors.NewNotFound(msgUserNotFound)
}

// fakeNotifier is a recording Notifier.
type fakeNotifier struct {
	verifications []notifierCall
	resets        []notifierCall
}

type notifierCall struct {
	user  User
	token string
}

func (f *fakeNotifier) SendEmailVerification(_ context.Context, u User, token string) error {
	f.verifications = append(f.verifications, notifierCall{u, token})
	return nil
}

func (f *fakeNotifier) SendPasswordReset(_ context.Context, u User, token string) error {
	f.resets = append(f.resets, notifierCall{u, token})
	return nil
}

// memTenants is an in-memory tenants.Repository; only Create is used.
type memTenants struct {
	tenants.Repository
	created []tenants.CreateTenantInput
	err     error
}

func (m *memTenants) Create(_ context.Context, in tenants.CreateTenantInput) (tenants.Tenant, error) {
	if m.err != nil {
		return tenants.Tenant{}, m.err
	}
	m.created = append(m.created, in)
	return tenants.Tenant{ID: int32(len(m.created)), Name: in.Name, APIKey: in.APIKey, IsActive: true, OwnerUserID: in.OwnerUserID}, nil
}

// testEnv is a Service over an empty memUsers and memTenants, whose writes go
// through tx.
type testEnv struct {
	svc      *Service
	users    *memUsers
	tenants  *memTenants
	tokens   *memTokens
	notifier *fakeNotifier
	tx       *database.FakeTxRunner
}

// newTestEnv returns a testEnv whose clock is *now.
func newTestEnv(t *testing.T, now *time.Time) testEnv {
	t.Helper()
	env := testEnv{
		users:    &memUsers{},
		tenants:  &memTenants{},
		tokens:   newMemTokens(func() time.Time { return *now }),
		notifier: &fakeNotifier{},
		tx:       &database.FakeTxRunner{},
	}
	svc, err := NewService(env.users, env.tokens, Options{
		Secret:       testSecret,
		TokenTTL:     time.Hour,
		PasswordCost: bcrypt.MinCost,
		Now:          func() time.Time { return *now },
		Tx:           env.tx,
		UsersFor:     func(db.Querier) Repository { return env.users },
		TenantsFor:   func(db.Querier) tenants.Repository { return env.tenants },
		TokensFor:    func(db.Querier) TokenRepository { return env.tokens },
		Notifier:     env.notifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	env.svc = svc
	return env
}

// newTestService returns the Service and memUsers of a newTestEnv.
func newTestService(t *testing.T, now *time.Time) (*Service, *memUsers) {
	t.Helper()
	env := newTestEnv(t, now)
	return env.svc, env.users
}

// signUp signs up email with password and returns the user, failing the
// test on error.
func signUp(t *testing.T, svc *Service, email, password string) User {
	t.Helper()
	acc, err := svc.SignUp(context.Background(), SignUpInput{Email: email, Password: password})
	if err != nil {
		t.Fatalf("SignUp(%q) = %v", email, err)
	}
	return acc.User
}

func assertCode(t *testing.T, err error, want apperrors.Code) {
	t.Helper()
	appErr, ok := apperrors.As(err)
	if !ok || appErr.Code != want {
		t.Fatalf("err = %v, want code %s", err, want)
	}
}

func TestNewServiceRejectsShortSecret(t *testing.T) {
	if _, err := NewService(&memUsers{}, nil, Options{Secret: []byte("short")}); err == nil {
		t.Fatal("want error for a short secret")
	}
}

func TestSignUpAndSignIn(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	env := newTestEnv(t, &now)
	svc, users := env.svc, env.users
	ctx := context.Background()

	acc, err := svc.SignUp(ctx, SignUpInput{Email: "  Alice@Example.COM ", Password: "correct horse"})
	if err != nil {
		t.Fatal(err)
	}
	u := acc.User
	if u.Username != "alice" || u.Email != "alice@example.com" || u.Role != RoleBasic {
		t.Fatalf("user = %+v, want alice/alice@example.com/BASIC", u)
	}
	if users.users[0].PasswordHash == "correct horse" {
		t.Fatal("password stored in clear")
	}
	if acc.Tenant.OwnerUserID == nil || *acc.Tenant.OwnerUserID != u.ID || acc.Tenant.Name != "alice@example.com" || len(acc.Tenant.APIKey) != 64 {
		t.Fatalf("tenant = %+v", acc.Tenant)
	}
	if env.tx.Calls != 1 || env.tx.Commits != 1 {
		t.Fatalf("tx = %+v, want one committed transaction", env.tx)
	}

	tok, err := svc.SignIn(ctx, "ALICE", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !tok.ExpiresAt.Equal(now.Add(time.Hour)) || tok.User.ID != u.ID {
		t.Fatalf("token = %+v", tok)
	}
	got, err := svc.CheckToken(ctx, tok.AccessToken)
	if err != nil || got.ID != u.ID {
		t.Fatalf("CheckToken = %+v, %v", got, err)
	}

	_, err = svc.SignIn(ctx, "alice", "wrong password")
	assertCode(t, err, apperrors.CodeUnauthorized)
	_, err = svc.SignIn(ctx, "bob", "correct horse")
	assertCode(t, err, apperrors.CodeUnauthorized)
}

func TestSignUpDuplicateEmail(t *testing.T) {
	now := time.Now()
	env := newTestEnv(t, &now)
	signUp(t, env.svc, "alice@example.com", "correct horse")

	_, err := env.svc.SignUp(context.Background(), SignUpInput{Email: "ALICE@example.com", Password: "another password"})
	assertCode(t, err, apperrors.CodeConflict)
	if appErr, _ := apperrors.As(err); appErr.Message != "email already registered" {
		t.Fatalf("message = %q", appErr.Message)
	}
	if len(env.users.users) != 1 || len(env.tenants.created) != 1 {
		t.Fatalf("users = %d, tenants = %d; want 1 each", len(env.users.users), len(env.tenants.created))
	}
}

func TestSignUpDerivesUniqueUsername(t *testing.T) {
	now := time.Now()
	env := newTestEnv(t, &now)
	first := signUp(t, env.svc, "alice@example.com", "correct horse")
	second := signUp(t, env.svc, "alice@example.org", "correct horse")
	if first.Username != "alice" || !usernamePattern.MatchString(second.Username) || !strings.HasPrefix(second.Username, "alice-") {
		t.Fatalf("usernames = %q, %q", first.Username, second.Username)
	}
	// The colliding attempt was rolled back before the retry.
	if env.tx.Calls != 3 || env.tx.Commits != 2 || env.tx.Rollbacks != 1 {
		t.Fatalf("tx = %+v", env.tx)
	}

	for email, want := range map[string]string{
		"a@example.com":                     "a__",
		"john+news@example.com":             "john-news",
		strings.Repeat("x", 60) + "@ex.com": strings.Repeat("x", 50),
		"--@example.com":                    "___",
	} {
		if got := usernameFromEmail(email); got != want || !usernamePattern.MatchString(got) {
			t.Errorf("usernameFromEmail(%q) = %q, want %q", email, got, want)
		}
	}
	long, err := withRandomSuffix(strings.Repeat("x", 50))
	if err != nil || len(long) != maxUsernameLen || !usernamePattern.MatchString(long) {
		t.Fatalf("withRandomSuffix = %q, %v", long, err)
	}
}

func TestSignUpWithUsername(t *testing.T) {
	now := time.Now()
	env := newTestEnv(t, &now)
	ctx := context.Background()
	acc, err := env.svc.SignUp(ctx, SignUpInput{Email: "alice@example.com", Username: " Wonder.Land ", Password: "correct horse"})
	if err != nil {
		t.Fatal(err)
	}
	if acc.User.Username != "wonder.land" {
		t.Fatalf("username = %q", acc.User.Username)
	}
	// An explicit username is never suffixed: a collision is reported.
	_, err = env.svc.SignUp(ctx, SignUpInput{Email: "bob@example.com", Username: "wonder.land", Password: "correct horse"})
	assertCode(t, err, apperrors.CodeConflict)
	if appErr, _ := apperrors.As(err); appErr.Message != "username already taken" {
		t.Fatalf("message = %q", appErr.Message)
	}
	_, err = env.svc.SignUp(ctx, SignUpInput{Email: "carol@example.com", Username: "x!", Password: "correct horse"})
	assertCode(t, err, apperrors.CodeInvalidArgument)
}

func TestSignUpRollsBackWhenTenantCreationFails(t *testing.T) {
	now := time.Now()
	env := newTestEnv(t, &now)
	env.tenants.err = apperrors.NewInternal(errors.New("insert tenant failed"))

	acc, err := env.svc.SignUp(context.Background(), SignUpInput{Email: "alice@example.com", Password: "correct horse"})
	assertCode(t, err, apperrors.CodeInternal)
	if acc != (Account{}) {
		t.Fatalf("account = %+v, want zero", acc)
	}
	// The user insert ran inside the transaction, which was rolled back.
	if len(env.users.users) != 1 || env.tx.Calls != 1 || env.tx.Rollbacks != 1 || env.tx.Commits != 0 {
		t.Fatalf("users = %d, tx = %+v; want the user insert rolled back", len(env.users.users), env.tx)
	}
}

func TestSignUpWithoutTxRunner(t *testing.T) {
	svc, err := NewService(&memUsers{}, nil, Options{Secret: testSecret, PasswordCost: bcrypt.MinCost})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.SignUp(context.Background(), SignUpInput{Email: "alice@example.com", Password: "correct horse"})
	assertCode(t, err, apperrors.CodeInternal)
}

func TestSignUpValidation(t *testing.T) {
	now := time.Now()
	env := newTestEnv(t, &now)
	for _, tt := range []struct{ email, password string }{
		{"", "long enough"},
		{"alice", "long enough"},
		{"alice@", "long enough"},
		{"@example.com", "long enough"},
		{"alice@localhost", "long enough"},
		{"Alice <alice@example.com>", "long enough"},
		{"al ice@example.com", "long enough"},
		{strings.Repeat("a", 250) + "@ex.com", "long enough"},
		{"alice@example.com", "short"},
		{"alice@example.com", strings.Repeat("p", 73)},
	} {
		_, err := env.svc.SignUp(context.Background(), SignUpInput{Email: tt.email, Password: tt.password})
		assertCode(t, err, apperrors.CodeInvalidArgument)
	}
	if env.tx.Calls != 0 {
		t.Fatalf("tx calls = %d, want 0 for invalid input", env.tx.Calls)
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got := NormalizeEmail("  Alice@Example.COM\t"); got != "alice@example.com" {
		t.Fatalf("NormalizeEmail = %q", got)
	}
}

func TestCheckTokenRejectsBadTokens(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	svc, users := newTestService(t, &now)
	ctx := context.Background()
	u := signUp(t, svc, "alice@example.com", "correct horse")
	tok, _ := svc.SignIn(ctx, "alice", "correct horse")

	other, _ := NewService(users, nil, Options{Secret: []byte(strings.Repeat("x", MinSecretLen)), PasswordCost: bcrypt.MinCost, Now: func() time.Time { return now }})
	forged, _ := other.SignIn(ctx, "alice", "correct horse")
	parts := strings.Split(tok.AccessToken, ".")

	for name, token := range map[string]string{
		"empty":         "",
		"garbage":       "not.a.token",
		"other secret":  forged.AccessToken,
		"alg none":      b64.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + parts[1] + ".",
		"tampered body": parts[0] + "." + b64.EncodeToString([]byte(`{"iss":"gogoform","sub":"1","exp":9999999999}`)) + "." + parts[2],
	} {
		if _, err := svc.CheckToken(ctx, token); err == nil {
			t.Errorf("%s: CheckToken accepted the token", name)
		}
	}

	// Expired.
	now = now.Add(time.Hour)
	_, err := svc.CheckToken(ctx, tok.AccessToken)
	assertCode(t, err, apperrors.CodeUnauthorized)

	// Deactivated users lose access even with an unexpired token.
	now = now.Add(-30 * time.Minute)
	users.users[u.ID-1].IsActive = false
	_, err = svc.CheckToken(ctx, tok.AccessToken)
	assertCode(t, err, apperrors.CodeUnauthorized)
}

func TestCheckTokenUsesCurrentRole(t *testing.T) {
	now := time.Now()
	svc, users := newTestService(t, &now)
	ctx := context.Background()
	signUp(t, svc, "alice@example.com", "correct horse")
	tok, _ := svc.SignIn(ctx, "alice", "correct horse")
	users.users[0].Role = RoleFormCreator

	u, err := svc.CheckToken(ctx, tok.AccessToken)
	if err != nil || u.Role != RoleFormCreator {
		t.Fatalf("CheckToken = %+v, %v; want FORM_CREATOR", u, err)
	}
}

func TestEnsureUser(t *testing.T) {
	now := time.Now()
	svc, users := newTestService(t, &now)
	ctx := context.Background()

	created, err := svc.EnsureUser(ctx, "Admin", "", "change-me-now", RoleAdmin)
	if err != nil || !created {
		t.Fatalf("EnsureUser = %v, %v", created, err)
	}
	created, err = svc.EnsureUser(ctx, "admin", "", "another-password", RoleAdmin)
	if err != nil || created {
		t.Fatalf("second EnsureUser = %v, %v; want not created", created, err)
	}
	if _, err := svc.CheckPassword(ctx, "admin", "change-me-now"); err != nil {
		t.Fatalf("existing user was changed: %v", err)
	}
	if users.users[0].Role != RoleAdmin {
		t.Fatalf("role = %s, want ADMIN", users.users[0].Role)
	}
	_, err = svc.EnsureUser(ctx, "root", "", "change-me-now", Role("ROOT"))
	assertCode(t, err, apperrors.CodeInvalidArgument)

	// An optional email is normalized, validated and usable to sign in.
	created, err = svc.EnsureUser(ctx, "ops", " Ops@Example.com ", "change-me-now", RoleAdmin)
	if err != nil || !created || users.users[1].Email != "ops@example.com" {
		t.Fatalf("EnsureUser with email = %v, %v; user = %+v", created, err, users.users[1])
	}
	if _, err := svc.CheckPassword(ctx, "ops@example.com", "change-me-now"); err != nil {
		t.Fatalf("CheckPassword by email = %v", err)
	}
	_, err = svc.EnsureUser(ctx, "ops2", "not-an-email", "change-me-now", RoleAdmin)
	assertCode(t, err, apperrors.CodeInvalidArgument)
}

func TestCheckPasswordIdentifierKinds(t *testing.T) {
	now := time.Now()
	svc, users := newTestService(t, &now)
	ctx := context.Background()
	alice := signUp(t, svc, "alice@example.com", "correct horse")
	if _, err := svc.EnsureUser(ctx, "admin", "", "change-me-now", RoleAdmin); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name, identifier, password, wantUser string
	}{
		{"email", "alice@example.com", "correct horse", "alice"},
		{"email is case-insensitive and trimmed", "  ALICE@Example.Com ", "correct horse", "alice"},
		{"username of an email user", "alice", "correct horse", "alice"},
		{"bootstrap admin by username", "Admin", "change-me-now", "admin"},
	} {
		u, err := svc.CheckPassword(ctx, tt.identifier, tt.password)
		if err != nil || u.Username != tt.wantUser {
			t.Errorf("%s: CheckPassword(%q) = %+v, %v; want %s", tt.name, tt.identifier, u, err, tt.wantUser)
		}
	}
	tok, err := svc.SignIn(ctx, "alice@example.com", "correct horse")
	if err != nil || tok.User.ID != alice.ID {
		t.Fatalf("SignIn by email = %+v, %v", tok, err)
	}

	for _, tt := range []struct {
		name, identifier, password string
	}{
		{"wrong password by email", "alice@example.com", "wrong password"},
		{"unknown email", "bob@example.com", "correct horse"},
		{"email is not a username", "alice@example.com.", "correct horse"},
		{"username is not an email", "admin@", "change-me-now"},
		{"unknown username", "bob", "correct horse"},
	} {
		_, err := svc.CheckPassword(ctx, tt.identifier, tt.password)
		assertCode(t, err, apperrors.CodeUnauthorized)
		if appErr, _ := apperrors.As(err); appErr.Message != msgInvalidCredentials {
			t.Errorf("%s: message = %q, want %q", tt.name, appErr.Message, msgInvalidCredentials)
		}
	}

	// Inactive users cannot sign in by email either.
	users.users[0].IsActive = false
	_, err = svc.CheckPassword(ctx, "alice@example.com", "correct horse")
	assertCode(t, err, apperrors.CodeUnauthorized)
}

func TestSignUpSendsEmailVerification(t *testing.T) {
	now := time.Now()
	env := newTestEnv(t, &now)
	ctx := context.Background()

	acc, err := env.svc.SignUp(ctx, SignUpInput{Email: "alice@example.com", Password: "correct horse"})
	if err != nil {
		t.Fatal(err)
	}
	if len(env.notifier.verifications) != 1 || env.notifier.verifications[0].user.ID != acc.User.ID {
		t.Fatalf("verifications = %+v, want one for %d", env.notifier.verifications, acc.User.ID)
	}
	if acc.User.EmailVerified() {
		t.Fatal("new user should not start out verified")
	}

	got, err := env.svc.VerifyEmail(ctx, env.notifier.verifications[0].token)
	if err != nil || !got.EmailVerified() {
		t.Fatalf("VerifyEmail = %+v, %v", got, err)
	}
}

func TestResendVerification(t *testing.T) {
	now := time.Now()
	env := newTestEnv(t, &now)
	ctx := context.Background()
	u := signUp(t, env.svc, "alice@example.com", "correct horse")
	firstToken := env.notifier.verifications[0].token

	if err := env.svc.ResendVerification(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if len(env.notifier.verifications) != 2 {
		t.Fatalf("verifications = %d, want 2", len(env.notifier.verifications))
	}
	secondToken := env.notifier.verifications[1].token
	if firstToken == secondToken {
		t.Fatal("ResendVerification reused the sign-up token")
	}

	// The sign-up token was invalidated by the resend.
	_, err := env.svc.VerifyEmail(ctx, firstToken)
	assertCode(t, err, apperrors.CodeUnauthorized)

	// The latest token still verifies.
	if _, err := env.svc.VerifyEmail(ctx, secondToken); err != nil {
		t.Fatal(err)
	}

	// Once verified, resending is a no-op.
	if err := env.svc.ResendVerification(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if len(env.notifier.verifications) != 2 {
		t.Fatalf("verifications = %d, want still 2 after verification", len(env.notifier.verifications))
	}
}

func TestVerifyEmailRejectsReusedAndExpiredTokens(t *testing.T) {
	now := time.Now()
	env := newTestEnv(t, &now)
	ctx := context.Background()
	signUp(t, env.svc, "alice@example.com", "correct horse")
	token := env.notifier.verifications[0].token

	if _, err := env.svc.VerifyEmail(ctx, token); err != nil {
		t.Fatal(err)
	}
	_, err := env.svc.VerifyEmail(ctx, token)
	assertCode(t, err, apperrors.CodeUnauthorized)

	signUp(t, env.svc, "bob@example.com", "correct horse")
	expired := env.notifier.verifications[1].token
	now = now.Add(EmailVerificationTokenTTL + time.Minute)
	_, err = env.svc.VerifyEmail(ctx, expired)
	assertCode(t, err, apperrors.CodeUnauthorized)

	_, err = env.svc.VerifyEmail(ctx, "does-not-exist")
	assertCode(t, err, apperrors.CodeUnauthorized)
}

func TestRequestPasswordReset(t *testing.T) {
	now := time.Now()
	env := newTestEnv(t, &now)
	ctx := context.Background()
	u := signUp(t, env.svc, "alice@example.com", "correct horse")

	// Unknown email: same nil result, no email sent.
	if err := env.svc.RequestPasswordReset(ctx, "nobody@example.com"); err != nil {
		t.Fatal(err)
	}
	if len(env.notifier.resets) != 0 {
		t.Fatalf("resets = %d, want 0 for an unknown email", len(env.notifier.resets))
	}

	if err := env.svc.RequestPasswordReset(ctx, "ALICE@example.com"); err != nil {
		t.Fatal(err)
	}
	if len(env.notifier.resets) != 1 || env.notifier.resets[0].user.ID != u.ID {
		t.Fatalf("resets = %+v, want one for %d", env.notifier.resets, u.ID)
	}
	firstToken := env.notifier.resets[0].token

	// A second request invalidates the first token.
	if err := env.svc.RequestPasswordReset(ctx, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	secondToken := env.notifier.resets[1].token
	if firstToken == secondToken {
		t.Fatal("RequestPasswordReset reused the token")
	}
	if err := env.svc.ResetPassword(ctx, firstToken, "a whole new password"); err == nil {
		t.Fatal("the invalidated first token should no longer reset the password")
	}
}

func TestResetPassword(t *testing.T) {
	now := time.Now()
	env := newTestEnv(t, &now)
	ctx := context.Background()
	signUp(t, env.svc, "alice@example.com", "correct horse")

	if err := env.svc.RequestPasswordReset(ctx, "alice@example.com"); err != nil {
		t.Fatal(err)
	}
	token := env.notifier.resets[0].token

	// An invalid password is rejected without burning the token.
	err := env.svc.ResetPassword(ctx, token, "short")
	assertCode(t, err, apperrors.CodeInvalidArgument)

	if err := env.svc.ResetPassword(ctx, token, "a whole new password"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.CheckPassword(ctx, "alice@example.com", "correct horse"); err == nil {
		t.Fatal("old password should no longer work")
	}
	if _, err := env.svc.CheckPassword(ctx, "alice@example.com", "a whole new password"); err != nil {
		t.Fatalf("new password should work: %v", err)
	}
	if !env.users.users[0].EmailVerified() {
		t.Fatal("ResetPassword should mark the email verified")
	}

	// Single use: the same token cannot reset the password again.
	err = env.svc.ResetPassword(ctx, token, "yet another password")
	assertCode(t, err, apperrors.CodeUnauthorized)
}

func TestSetRole(t *testing.T) {
	now := time.Now()
	svc, _ := newTestService(t, &now)
	ctx := context.Background()
	admin := signUp(t, svc, "admin@example.com", "correct horse")
	bob := signUp(t, svc, "bob@example.com", "correct horse")

	u, err := svc.SetRole(ctx, admin, bob.ID, RoleFormCreator)
	if err != nil || u.Role != RoleFormCreator {
		t.Fatalf("SetRole = %+v, %v", u, err)
	}
	_, err = svc.SetRole(ctx, admin, admin.ID, RoleBasic)
	assertCode(t, err, apperrors.CodeInvalidArgument)
	_, err = svc.SetRole(ctx, admin, bob.ID, Role("ROOT"))
	assertCode(t, err, apperrors.CodeInvalidArgument)
	_, err = svc.SetRole(ctx, admin, 99, RoleBasic)
	assertCode(t, err, apperrors.CodeNotFound)
}
