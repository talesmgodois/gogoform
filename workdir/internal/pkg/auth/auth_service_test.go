package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	apperrors "app/internal/errors"
)

var testSecret = []byte(strings.Repeat("k", MinSecretLen))

// memUsers is an in-memory Repository.
type memUsers struct {
	users []StoredUser
}

func (m *memUsers) Create(_ context.Context, in CreateUserInput) (User, error) {
	for _, u := range m.users {
		if u.Username == in.Username {
			return User{}, apperrors.NewConflict(msgUsernameTaken)
		}
	}
	u := StoredUser{User: User{ID: int32(len(m.users) + 1), Username: in.Username, Role: in.Role, IsActive: true}, PasswordHash: in.PasswordHash}
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

// newTestService returns a Service over an empty memUsers whose clock is *now.
func newTestService(t *testing.T, now *time.Time) (*Service, *memUsers) {
	t.Helper()
	users := &memUsers{}
	svc, err := NewService(users, Options{Secret: testSecret, TokenTTL: time.Hour, PasswordCost: bcrypt.MinCost, Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	return svc, users
}

func assertCode(t *testing.T, err error, want apperrors.Code) {
	t.Helper()
	appErr, ok := apperrors.As(err)
	if !ok || appErr.Code != want {
		t.Fatalf("err = %v, want code %s", err, want)
	}
}

func TestNewServiceRejectsShortSecret(t *testing.T) {
	if _, err := NewService(&memUsers{}, Options{Secret: []byte("short")}); err == nil {
		t.Fatal("want error for a short secret")
	}
}

func TestSignUpAndSignIn(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	svc, users := newTestService(t, &now)
	ctx := context.Background()

	u, err := svc.SignUp(ctx, "  Alice ", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "alice" || u.Role != RoleBasic {
		t.Fatalf("user = %+v, want alice/BASIC", u)
	}
	if users.users[0].PasswordHash == "correct horse" {
		t.Fatal("password stored in clear")
	}

	_, err = svc.SignUp(ctx, "alice", "another password")
	assertCode(t, err, apperrors.CodeConflict)

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

func TestSignUpValidation(t *testing.T) {
	now := time.Now()
	svc, _ := newTestService(t, &now)
	for _, tt := range []struct{ username, password string }{
		{"ab", "long enough"},
		{"has:colon", "long enough"},
		{"has space", "long enough"},
		{strings.Repeat("a", 51), "long enough"},
		{"alice", "short"},
		{"alice", strings.Repeat("p", 73)},
	} {
		_, err := svc.SignUp(context.Background(), tt.username, tt.password)
		assertCode(t, err, apperrors.CodeInvalidArgument)
	}
}

func TestCheckTokenRejectsBadTokens(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	svc, users := newTestService(t, &now)
	ctx := context.Background()
	u, _ := svc.SignUp(ctx, "alice", "correct horse")
	tok, _ := svc.SignIn(ctx, "alice", "correct horse")

	other, _ := NewService(users, Options{Secret: []byte(strings.Repeat("x", MinSecretLen)), PasswordCost: bcrypt.MinCost, Now: func() time.Time { return now }})
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
	_, _ = svc.SignUp(ctx, "alice", "correct horse")
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

	created, err := svc.EnsureUser(ctx, "Admin", "change-me-now", RoleAdmin)
	if err != nil || !created {
		t.Fatalf("EnsureUser = %v, %v", created, err)
	}
	created, err = svc.EnsureUser(ctx, "admin", "another-password", RoleAdmin)
	if err != nil || created {
		t.Fatalf("second EnsureUser = %v, %v; want not created", created, err)
	}
	if _, err := svc.CheckPassword(ctx, "admin", "change-me-now"); err != nil {
		t.Fatalf("existing user was changed: %v", err)
	}
	if users.users[0].Role != RoleAdmin {
		t.Fatalf("role = %s, want ADMIN", users.users[0].Role)
	}
	_, err = svc.EnsureUser(ctx, "root", "change-me-now", Role("ROOT"))
	assertCode(t, err, apperrors.CodeInvalidArgument)
}

func TestSetRole(t *testing.T) {
	now := time.Now()
	svc, _ := newTestService(t, &now)
	ctx := context.Background()
	admin, _ := svc.SignUp(ctx, "admin", "correct horse")
	bob, _ := svc.SignUp(ctx, "bob", "correct horse")

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
