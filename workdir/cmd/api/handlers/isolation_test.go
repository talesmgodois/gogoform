package handlers

import (
	"net/http"
	"strings"
	"testing"

)

func TestIsolationAppPagesHideOtherTenant(t *testing.T) {
	svc := twoTenantServices()

	// User A's dashboard shows only A's form.
	rec := serve(svc, pageRequest(t, svc, "/app", "creator"))
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if !strings.Contains(body, "A&#39;s form") {
		t.Error("A's dashboard misses A's form")
	}
	if strings.Contains(body, "B&#39;s form") {
		t.Error("A's dashboard leaks B's form")
	}

	// User A cannot open B's form, its exports or its builder copy.
	for _, path := range []string{
		"/app/forms/2",
		"/app/forms/2/submissions.json",
		"/app/forms/2/submissions.csv",
		"/app/builder?form=2",
	} {
		rec := serve(svc, pageRequest(t, svc, path, "creator"))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s as A = %d, want 404 (body %s)", path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "B's form") {
			t.Errorf("GET %s as A leaks B's title", path)
		}
	}

	// User A's own form is reachable.
	assertStatus(t, serve(svc, pageRequest(t, svc, "/app/forms/1", "creator")), http.StatusOK)

	// User B keeps their own data.
	assertStatus(t, serve(svc, pageRequest(t, svc, "/app/forms/2", "basic")), http.StatusOK)
}

func TestIsolationAdminPages(t *testing.T) {
	svc := twoTenantServices()

	// A non-admin is forbidden from the global admin view.
	for _, user := range []string{"creator", "basic"} {
		rec := serve(svc, pageRequest(t, svc, "/app/admin", user))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s on /app/admin = %d, want 403", user, rec.Code)
		}
	}

	// An admin sees every tenant's forms.
	rec := serve(svc, pageRequest(t, svc, "/app/admin", "admin"))
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{"A&#39;s form", "B&#39;s form"} {
		if !strings.Contains(body, want) {
			t.Errorf("admin dashboard misses %q", want)
		}
	}

	// An admin can open another tenant's form and export it.
	for _, path := range []string{
		"/app/admin/forms/2",
		"/app/admin/forms/2/submissions.json",
		"/app/admin/forms/2/submissions.csv",
	} {
		assertStatus(t, serve(svc, pageRequest(t, svc, path, "admin")), http.StatusOK)
	}
}

func TestIsolationAPIAndFiles(t *testing.T) {
	svc := twoTenantServices()
	tokenA := bearer(tokenFor(t, svc, "creator"))

	// User A cannot read or delete B's form or file through the API.
	for _, tt := range []struct {
		method, path string
	}{
		{http.MethodGet, "/forms/2"},
		{http.MethodPut, "/forms/2"},
		{http.MethodDelete, "/forms/2"},
		{http.MethodGet, "/forms/2/submissions"},
		{http.MethodGet, "/forms/2/webhooks"},
		{http.MethodPost, "/forms/2/webhooks"},
		{http.MethodDelete, "/files/" + fileOfB},
	} {
		var body string
		if tt.method == http.MethodPut {
			body = validForm
		}
		if tt.method == http.MethodPost {
			body = `{"target_url":"https://example.com/hook"}`
		}
		rec := serve(svc, userRequest(tt.method, tt.path, body, tokenA))
		if rec.Code == http.StatusOK || rec.Code == http.StatusNoContent || rec.Code == http.StatusCreated {
			t.Errorf("%s %s as A succeeded (%d), want denial", tt.method, tt.path, rec.Code)
		}
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s as A = %d, want 404/403/401", tt.method, tt.path, rec.Code)
		}
	}
	// B's form and file survive.
	if _, ok := svc.forms.(*fakeForms).forms[formOfB]; !ok {
		t.Fatal("user A deleted user B's form")
	}
	if _, ok := svc.files.(*fakeFiles).files[fileOfB]; !ok {
		t.Fatal("user A deleted user B's file")
	}

	// Builder shows the viewer's own API key.
	rec := serve(svc, pageRequest(t, svc, "/app/builder", "creator"))
	assertStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `data-api-key="key-a"`) {
		t.Error("builder does not show user A's API key")
	}
}

func TestIsolationSignupHTTP(t *testing.T) {
	svc := testServices()
	rec := serve(svc, userRequest(http.MethodPost, "/auth/signup", `{"email":"iso@example.com","password":"long enough"}`, ""))
	assertStatus(t, rec, http.StatusCreated)
	u := decode[SignUpResponse](t, rec)
	if u.TenantID == 0 || u.Email != "iso@example.com" {
		t.Fatalf("signup = %+v", u)
	}

	// Duplicate email returns 409 at the HTTP level.
	rec = serve(svc, userRequest(http.MethodPost, "/auth/signup", `{"email":"ISO@example.com","password":"long enough"}`, ""))
	assertStatus(t, rec, http.StatusConflict)
	if !strings.Contains(rec.Body.String(), "email already registered") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

