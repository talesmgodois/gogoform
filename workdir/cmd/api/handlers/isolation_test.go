package handlers

import (
	"net/http"
	"reflect"
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
		"/app/builder?copy=2",
	} {
		rec := serve(svc, pageRequest(t, svc, path, "creator"))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s as A = %d, want 404 (body %s)", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), ">Form not found</h1>") {
			t.Errorf("GET %s as A is not the form not found page", path)
		}
		if strings.Contains(rec.Body.String(), "B's form") || strings.Contains(rec.Body.String(), "B&#39;s form") {
			t.Errorf("GET %s as A leaks B's title", path)
		}
	}

	// User A's own form is reachable.
	assertStatus(t, serve(svc, pageRequest(t, svc, "/app/forms/1", "creator")), http.StatusOK)

	// User B keeps their own data.
	assertStatus(t, serve(svc, pageRequest(t, svc, "/app/forms/2", "basic")), http.StatusOK)
}

// TestIsolationTwoSignedInUsers walks the dashboard, the form page, its
// exports, the builder and the cookie-authenticated API as user A ("creator",
// tenantA), who must reach only A's form and get 404 for B's ("basic",
// tenantB) while B's form stays untouched.
func TestIsolationTwoSignedInUsers(t *testing.T) {
	svc := twoTenantServices()
	fake := svc.forms.(*fakeForms)
	formB := fake.forms[formOfB]
	const origin = "http://example.com" // httptest.NewRequest's host

	// A's dashboard lists only A's form.
	rec := serve(svc, cookieRequest(t, svc, http.MethodGet, "/app", "", "creator", ""))
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if !strings.Contains(body, "A&#39;s form") || !strings.Contains(body, `data-form-id="1"`) {
		t.Error("A's dashboard misses A's form")
	}
	if strings.Contains(body, "B&#39;s form") || strings.Contains(body, `data-form-id="2"`) {
		t.Error("A's dashboard leaks B's form")
	}

	// A opens, exports and edits their own form.
	assertStatus(t, serve(svc, cookieRequest(t, svc, http.MethodGet, "/app/forms/1", "", "creator", "")), http.StatusOK)
	for _, tt := range []struct{ path, contentType string }{
		{"/app/forms/1/submissions.json", "application/json"},
		{"/app/forms/1/submissions.csv", "text/csv; charset=utf-8"},
	} {
		rec := serve(svc, cookieRequest(t, svc, http.MethodGet, tt.path, "", "creator", ""))
		assertStatus(t, rec, http.StatusOK)
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, tt.contentType) {
			t.Errorf("GET %s Content-Type = %q, want %q", tt.path, got, tt.contentType)
		}
		if !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
			t.Errorf("GET %s is not an attachment", tt.path)
		}
	}
	rec = serve(svc, cookieRequest(t, svc, http.MethodGet, "/app/builder?form=1", "", "creator", ""))
	assertStatus(t, rec, http.StatusOK)
	body = rec.Body.String()
	if !strings.Contains(body, `data-mode="edit"`) || !strings.Contains(body, `data-form-id="1"`) {
		t.Error("builder is not editing A's form")
	}
	if got := pageDraft(t, body); got.Slug != "a" {
		t.Errorf("builder draft slug = %q, want %q", got.Slug, "a")
	}

	// A gets 404 for every page and API call on B's form.
	for _, path := range []string{
		"/app/forms/2",
		"/app/forms/2/submissions.json",
		"/app/forms/2/submissions.csv",
		"/app/builder?form=2",
	} {
		rec := serve(svc, cookieRequest(t, svc, http.MethodGet, path, "", "creator", ""))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s as A = %d, want 404", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "B&#39;s form") || strings.Contains(rec.Body.String(), "B's form") {
			t.Errorf("GET %s as A leaks B's title", path)
		}
	}
	for _, tt := range []struct{ method, body string }{
		{http.MethodPut, validForm},
		{http.MethodDelete, ""},
	} {
		rec := serve(svc, cookieRequest(t, svc, tt.method, "/forms/2", tt.body, "creator", origin))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s /forms/2 as A = %d, want 404 (body %s)", tt.method, rec.Code, rec.Body.String())
		}
	}
	if got, ok := fake.forms[formOfB]; !ok || !reflect.DeepEqual(got, formB) {
		t.Fatalf("B's form = %+v (present %v), want unchanged %+v", got, ok, formB)
	}

	// A changes and then deletes their own form through the API.
	rec = serve(svc, cookieRequest(t, svc, http.MethodPut, "/forms/1", validForm, "creator", origin))
	assertStatus(t, rec, http.StatusOK)
	if got := fake.updated; got.ID != formOfA || got.TenantID != tenantA.ID || got.Slug != "contact-us" {
		t.Fatalf("updated = %+v, want A's form in tenantA", got)
	}
	assertStatus(t, serve(svc, cookieRequest(t, svc, http.MethodDelete, "/forms/1", "", "creator", origin)), http.StatusNoContent)
	if _, ok := fake.forms[formOfA]; ok {
		t.Fatal("A's form was not deleted")
	}
	if got, ok := fake.forms[formOfB]; !ok || !reflect.DeepEqual(got, formB) {
		t.Fatalf("B's form = %+v (present %v), want unchanged %+v", got, ok, formB)
	}

	// B still reaches their own form.
	assertStatus(t, serve(svc, cookieRequest(t, svc, http.MethodGet, "/app/forms/2", "", "basic", "")), http.StatusOK)
	assertStatus(t, serve(svc, cookieRequest(t, svc, http.MethodGet, "/forms/2", "", "basic", "")), http.StatusOK)
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

	// The builder works with the session and shows no API key.
	rec := serve(svc, pageRequest(t, svc, "/app/builder", "creator"))
	assertStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "key-a") {
		t.Error("builder shows user A's API key")
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
