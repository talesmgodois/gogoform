package auth

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	apperrors "app/internal/errors"
)

const testIssuer = "https://idp.example.com"

func newExternalTestService(t *testing.T, autoCreate bool) (*Service, *memUsers) {
	t.Helper()
	now := time.Unix(1_800_000_000, 0)
	return newTestServiceWith(t, &now, Options{
		Methods:  []Method{MethodBasic, MethodJWT, MethodOIDC},
		External: ExternalOptions{AutoCreateUsers: autoCreate, DefaultRole: RoleFormCreator},
	})
}

func TestSignInExternalCreatesThenFindsUser(t *testing.T) {
	svc, users := newExternalTestService(t, true)
	ctx := context.Background()
	id := ExternalIdentity{Issuer: testIssuer, Subject: "sub-1", Email: "Alice@Example.com", EmailVerified: true, PreferredUsername: "Alice"}

	tok, err := svc.SignInExternal(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if tok.User.Username != "alice" || tok.User.Role != RoleFormCreator || tok.AccessToken == "" {
		t.Fatalf("token = %+v, want a FORM_CREATOR alice", tok)
	}
	if got, err := svc.CheckToken(ctx, tok.AccessToken); err != nil || got.ID != tok.User.ID {
		t.Fatalf("CheckToken = %+v, %v", got, err)
	}
	if users.users[0].PasswordHash != "" {
		t.Fatal("OIDC user got a password hash")
	}

	id.Email = "alice@new.example.com"
	again, err := svc.SignInExternal(ctx, id)
	if err != nil || again.User.ID != tok.User.ID {
		t.Fatalf("second sign-in = %+v, %v; want the same user", again.User, err)
	}
	if len(users.users) != 1 || users.identities[0].logins != 2 || users.identities[0].email != "alice@new.example.com" {
		t.Fatalf("users = %+v, identities = %+v", users.users, users.identities)
	}
	if n, err := svc.LinkedIdentities(ctx, tok.User.ID); err != nil || n != 1 {
		t.Fatalf("LinkedIdentities = %d, %v", n, err)
	}
}

func TestSignInExternalSuffixesTakenUsernames(t *testing.T) {
	svc, _ := newExternalTestService(t, true)
	ctx := context.Background()
	if _, err := svc.SignUp(ctx, "alice", "correct horse"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SignUp(ctx, "alice2", "correct horse"); err != nil {
		t.Fatal(err)
	}
	tok, err := svc.SignInExternal(ctx, ExternalIdentity{Issuer: testIssuer, Subject: "sub-1", PreferredUsername: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if tok.User.Username != "alice3" {
		t.Fatalf("username = %q, want alice3", tok.User.Username)
	}
}

// An identity whose email or username matches an existing local account
// gets its own new account: matching on those would let anyone who can pick
// their email at a provider take the local account over.
func TestSignInExternalNeverLinksByEmailOrUsername(t *testing.T) {
	svc, _ := newExternalTestService(t, true)
	ctx := context.Background()
	admin, err := svc.SignUp(ctx, "admin", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := svc.SignInExternal(ctx, ExternalIdentity{Issuer: testIssuer, Subject: "attacker", Email: "admin@example.com", EmailVerified: true, PreferredUsername: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if tok.User.ID == admin.ID || tok.User.Username == "admin" {
		t.Fatalf("identity signed in as the existing admin account: %+v", tok.User)
	}
}

func TestSignInExternalUnknownIdentityWithoutAutoCreate(t *testing.T) {
	svc, users := newExternalTestService(t, false)
	_, err := svc.SignInExternal(context.Background(), ExternalIdentity{Issuer: testIssuer, Subject: "sub-1", PreferredUsername: "alice"})
	assertCode(t, err, apperrors.CodeForbidden)
	if len(users.users) != 0 {
		t.Fatalf("created %+v with auto-create off", users.users)
	}
}

func TestSignInExternalRefusesInactiveUser(t *testing.T) {
	svc, users := newExternalTestService(t, true)
	ctx := context.Background()
	id := ExternalIdentity{Issuer: testIssuer, Subject: "sub-1", PreferredUsername: "alice"}
	if _, err := svc.SignInExternal(ctx, id); err != nil {
		t.Fatal(err)
	}
	users.users[0].IsActive = false
	_, err := svc.SignInExternal(ctx, id)
	assertCode(t, err, apperrors.CodeForbidden)
	if len(users.users) != 1 {
		t.Fatal("an inactive user's identity created a second account")
	}
}

func TestSignInExternalRequiresIssuerAndSubject(t *testing.T) {
	svc, _ := newExternalTestService(t, true)
	_, err := svc.SignInExternal(context.Background(), ExternalIdentity{Issuer: testIssuer})
	assertCode(t, err, apperrors.CodeUnauthorized)
}

func TestExternalUsername(t *testing.T) {
	for _, tt := range []struct {
		id   ExternalIdentity
		want string
	}{
		{ExternalIdentity{PreferredUsername: "Alice.Smith"}, "alice.smith"},
		{ExternalIdentity{PreferredUsername: "alice@corp.example.com"}, "alice_corp.example.com"},
		{ExternalIdentity{PreferredUsername: "John  Doe!"}, "john_doe"},
		{ExternalIdentity{PreferredUsername: "jo", Email: "joanna+x@example.com"}, "joanna_x"},
		{ExternalIdentity{PreferredUsername: "日本", Email: "@example.com"}, "user"},
		{ExternalIdentity{}, "user"},
		{ExternalIdentity{PreferredUsername: strings.Repeat("a", 60)}, strings.Repeat("a", 50)},
	} {
		if got := externalUsername(tt.id); got != tt.want {
			t.Errorf("externalUsername(%+v) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestUsernameWithSuffix(t *testing.T) {
	if got := usernameWithSuffix("alice", 1); got != "alice" {
		t.Errorf("attempt 1 = %q", got)
	}
	if got := usernameWithSuffix("alice", 12); got != "alice12" {
		t.Errorf("attempt 12 = %q", got)
	}
	long := strings.Repeat("a", 50)
	if got := usernameWithSuffix(long, 7); len(got) != 50 || !strings.HasSuffix(got, "7") {
		t.Errorf("long attempt = %q", got)
	}
}

func TestCheckPasswordRejectsUserWithoutPassword(t *testing.T) {
	svc, _ := newExternalTestService(t, true)
	ctx := context.Background()
	if _, err := svc.SignInExternal(ctx, ExternalIdentity{Issuer: testIssuer, Subject: "sub-1", PreferredUsername: "alice"}); err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{"", "anything"} {
		_, err := svc.CheckPassword(ctx, "alice", password)
		assertCode(t, err, apperrors.CodeUnauthorized)
		if appErr, _ := apperrors.As(err); appErr.Message != msgInvalidCredentials {
			t.Fatalf("message = %q, want the generic invalid-credentials one", appErr.Message)
		}
	}
}

func TestMethods(t *testing.T) {
	ctx := context.Background()
	identity := ExternalIdentity{Issuer: testIssuer, Subject: "sub-1", PreferredUsername: "carol"}
	tests := []struct {
		methods                         []Method
		signUp, signIn, basic, external apperrors.Code // "" = allowed
	}{
		{methods: nil, external: apperrors.CodeForbidden},
		{methods: []Method{MethodBasic}, signIn: apperrors.CodeForbidden, external: apperrors.CodeForbidden},
		{methods: []Method{MethodJWT}, basic: apperrors.CodeUnauthorized, external: apperrors.CodeForbidden},
		{methods: []Method{MethodOIDC}, signUp: apperrors.CodeForbidden, signIn: apperrors.CodeForbidden, basic: apperrors.CodeUnauthorized},
		{methods: []Method{MethodBasic, MethodJWT, MethodOIDC}},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.methods), func(t *testing.T) {
			now := time.Now()
			svc, _ := newTestServiceWith(t, &now, Options{Methods: tt.methods, External: ExternalOptions{AutoCreateUsers: true}})

			// The bootstrap admin is created whatever the methods.
			if created, err := svc.EnsureUser(ctx, "admin", "correct horse", RoleAdmin); err != nil || !created {
				t.Fatalf("EnsureUser = %v, %v", created, err)
			}
			_, err := svc.SignUp(ctx, "bob", "correct horse")
			assertAllowed(t, "SignUp", err, tt.signUp)
			_, err = svc.SignIn(ctx, "admin", "correct horse")
			assertAllowed(t, "SignIn", err, tt.signIn)
			_, err = svc.CheckPassword(ctx, "admin", "correct horse")
			assertAllowed(t, "CheckPassword", err, tt.basic)
			_, err = svc.SignInExternal(ctx, identity)
			assertAllowed(t, "SignInExternal", err, tt.external)
		})
	}
}

// assertAllowed checks err is nil when want is empty, else has code want.
func assertAllowed(t *testing.T, name string, err error, want apperrors.Code) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("%s: unexpected error %v", name, err)
		}
		return
	}
	appErr, ok := apperrors.As(err)
	if !ok || appErr.Code != want {
		t.Fatalf("%s: err = %v, want code %s", name, err, want)
	}
}

func TestNewServiceRejectsUnknownMethod(t *testing.T) {
	if _, err := NewService(&memUsers{}, Options{Secret: testSecret, Methods: []Method{"kerberos"}}); err == nil {
		t.Fatal("want error for an unknown method")
	}
}

func TestNewServiceRejectsInvalidExternalRole(t *testing.T) {
	if _, err := NewService(&memUsers{}, Options{Secret: testSecret, External: ExternalOptions{DefaultRole: "ROOT"}}); err == nil {
		t.Fatal("want error for an invalid default role")
	}
}
