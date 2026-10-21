package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apperrors "app/internal/errors"
	"app/internal/pkg/tenants"
)

// accountPost sends body as JSON to path from the same origin, with the
// session cookie of username.
func accountPost(t *testing.T, svc Services, path, username, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://example.com")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tokenFor(t, svc, username)})
	return serve(svc, req)
}

func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want apperrors.Code) {
	t.Helper()
	if body := decode[apperrors.HTTPErrorResponse](t, rec); body.Code != want {
		t.Fatalf("code = %q, want %q (body %s)", body.Code, want, rec.Body.String())
	}
}

func TestAppAccountPage(t *testing.T) {
	svc := appServices()
	withAccounts(t, &svc)
	rec := getAppPath(t, svc, "/app/account")
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{
		`data-account-form="/app/account/email"`, `data-account-form="/app/account/password"`, `data-account-form="/app/account/delete"`,
		`name="current_password"`, `name="confirm_password"`,
		`id="api-key-reveal"`, `id="api-key-copy"`, `id="api-key-rotate"`, `fetch("/tenants/me"`, `post("/tenants/me/api-key/rotate")`,
		`data-expected="creator"`, "Acme", `<script nonce="`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("account page does not contain %q", want)
		}
	}
	// The key is fetched on demand, never rendered; the debug route list is gone.
	for _, unwanted := range []string{testAPIKey, "Pages and who may open them", "GET /app/users"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("account page contains %q", unwanted)
		}
	}
}

func TestAppAccountPageWithoutWorkspace(t *testing.T) {
	svc := testServices()
	rec := serve(svc, pageRequest(t, svc, "/app/account", "basic"))
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if strings.Contains(body, `id="api-key"`) || !strings.Contains(body, "has no workspace") {
		t.Error("account page without a workspace shows an API key")
	}
	// Without an accounts service, the deletion is not offered.
	if strings.Contains(body, `id="delete-form"`) {
		t.Error("account page offers a deletion that is not available")
	}
}

func TestAppAccountRotateAPIKeyWithSession(t *testing.T) {
	svc := appServices()
	rec := accountPost(t, svc, "/tenants/me/api-key/rotate", appUser, "")
	assertStatus(t, rec, http.StatusOK)
	if got := decode[TenantWithAPIKeyResponse](t, rec); got.APIKey == "" || got.APIKey == testAPIKey {
		t.Fatalf("api_key = %q, want a new key", got.APIKey)
	}
}

func TestAppChangeEmail(t *testing.T) {
	svc := testServices()
	rec := accountPost(t, svc, "/app/account/email", appUser, `{"email":"New@Example.com","current_password":"`+testPassword+`"}`)
	assertStatus(t, rec, http.StatusOK)
	got := decode[UserResponse](t, rec)
	if got.Email != "new@example.com" || got.EmailVerifiedAt != nil {
		t.Fatalf("user = %+v, want the new email unverified", got)
	}
	// A verification link went to the new address.
	lastVerificationToken(t, svc, "new@example.com")

	page := serve(svc, pageRequest(t, svc, "/app/account?email=changed", appUser)).Body.String()
	for _, want := range []string{"new@example.com", "Unverified", "data-account-notice", "Resend verification email"} {
		if !strings.Contains(page, want) {
			t.Errorf("account page does not contain %q", want)
		}
	}
}

func TestAppChangeEmailRejected(t *testing.T) {
	svc := testServices()
	signUpAndGetToken(t, svc, "taken@example.com")
	tests := []struct {
		name, body string
		status     int
		code       apperrors.Code
	}{
		{"wrong password", `{"email":"b@example.com","current_password":"wrong-password"}`, http.StatusBadRequest, apperrors.CodeInvalidArgument},
		{"invalid email", `{"email":"nope","current_password":"` + testPassword + `"}`, http.StatusBadRequest, apperrors.CodeInvalidArgument},
		{"taken", `{"email":"taken@example.com","current_password":"` + testPassword + `"}`, http.StatusConflict, apperrors.CodeConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := accountPost(t, svc, "/app/account/email", appUser, tt.body)
			assertStatus(t, rec, tt.status)
			assertErrorCode(t, rec, tt.code)
		})
	}
}

func TestAppAccountRejectsCrossOrigin(t *testing.T) {
	svc := appServices()
	withAccounts(t, &svc)
	for _, path := range []string{"/app/account/email", "/app/account/password", "/app/account/delete"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		req.Header.Set("Origin", "https://evil.test")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tokenFor(t, svc, appUser)})
		assertStatus(t, serve(svc, req), http.StatusForbidden)
	}
}

func TestAppChangePassword(t *testing.T) {
	svc := testServices()
	rec := accountPost(t, svc, "/app/account/password", appUser,
		`{"current_password":"wrong-password","new_password":"new-secret-1","confirm_password":"new-secret-1"}`)
	assertStatus(t, rec, http.StatusBadRequest)
	assertErrorCode(t, rec, apperrors.CodeInvalidArgument)

	rec = accountPost(t, svc, "/app/account/password", appUser,
		`{"current_password":"`+testPassword+`","new_password":"new-secret-1","confirm_password":"new-secret-2"}`)
	assertStatus(t, rec, http.StatusBadRequest)

	rec = accountPost(t, svc, "/app/account/password", appUser,
		`{"current_password":"`+testPassword+`","new_password":"new-secret-1","confirm_password":"new-secret-1"}`)
	assertStatus(t, rec, http.StatusNoContent)

	assertStatus(t, serve(svc, userRequest(http.MethodPost, "/app/signin", "", basicHeader(appUser, testPassword))), http.StatusUnauthorized)
	assertStatus(t, serve(svc, userRequest(http.MethodPost, "/app/signin", "", basicHeader(appUser, "new-secret-1"))), http.StatusOK)
}

func TestAppDeleteAccount(t *testing.T) {
	svc := appServices()
	owned := testTenant
	owned.OwnerUserID = ptrTo(int32(2))
	owned.Plan, owned.StripeSubscriptionID, owned.SubscriptionStatus = tenants.PlanPro, "sub_1", "active"
	svc.tenants = &fakeTenants{owned: map[int32]tenants.Tenant{2: owned}}
	store, subs := withAccounts(t, &svc)

	rec := accountPost(t, svc, "/app/account/delete", appUser, `{"confirmation":"someone-else"}`)
	assertStatus(t, rec, http.StatusBadRequest)
	assertErrorCode(t, rec, apperrors.CodeInvalidArgument)
	if len(store.deletedUsers) != 0 || len(subs.canceled) != 0 {
		t.Fatal("a wrong confirmation deleted the account")
	}

	rec = accountPost(t, svc, "/app/account/delete", appUser, `{"confirmation":"Creator"}`)
	assertStatus(t, rec, http.StatusNoContent)
	if len(subs.canceled) != 1 || subs.canceled[0] != "sub_1" {
		t.Errorf("canceled = %v, want [sub_1]", subs.canceled)
	}
	if len(store.deletedTenants) != 1 || store.deletedTenants[0] != testTenant.ID || len(store.deletedUsers) != 1 || store.deletedUsers[0] != 2 {
		t.Errorf("deleted tenants %v, users %v; want [7], [2]", store.deletedTenants, store.deletedUsers)
	}
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].Name != sessionCookie || c[0].MaxAge >= 0 {
		t.Errorf("cookies = %+v, want the session cleared", c)
	}
}

func TestAppDeleteAccountStripeFailureKeepsData(t *testing.T) {
	svc := appServices()
	owned := testTenant
	owned.OwnerUserID = ptrTo(int32(2))
	owned.StripeSubscriptionID, owned.SubscriptionStatus = "sub_1", "active"
	svc.tenants = &fakeTenants{owned: map[int32]tenants.Tenant{2: owned}}
	store, subs := withAccounts(t, &svc)
	subs.err = apperrors.New(apperrors.CodeInternal, "payment provider error")

	rec := accountPost(t, svc, "/app/account/delete", appUser, `{"confirmation":"creator"}`)
	assertStatus(t, rec, http.StatusInternalServerError)
	if len(store.deletedTenants) != 0 || len(store.deletedUsers) != 0 {
		t.Fatal("data deleted although the subscription was not cancelled")
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("session cleared although the account still exists")
	}
}

func TestAppDeleteAccountUnavailable(t *testing.T) {
	svc := appServices()
	rec := accountPost(t, svc, "/app/account/delete", appUser, `{"confirmation":"creator"}`)
	assertStatus(t, rec, http.StatusNotFound)
}
