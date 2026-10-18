package handlers

import (
	"net/http"
	"testing"

	"app/internal/pkg/files"
	"app/internal/pkg/forms"
	"app/internal/pkg/tenants"
)

// Users of newTestAuth owning a workspace in twoTenantServices: "creator"
// (ID 2) owns tenantA and "basic" (ID 3) owns tenantB. "admin" has none.
var (
	tenantA = tenants.Tenant{ID: 20, Name: "A", APIKey: "key-a", IsActive: true, OwnerUserID: ptrTo(int32(2))}
	tenantB = tenants.Tenant{ID: 30, Name: "B", APIKey: "key-b", IsActive: true, OwnerUserID: ptrTo(int32(3))}
)

const (
	formOfA, formOfB, formOfAPIKey = 1, 2, 3
	fileOfA                        = "aaaaaaaa-0000-4000-8000-000000000001"
	fileOfB                        = "bbbbbbbb-0000-4000-8000-000000000002"
)

// twoTenantServices returns testServices with one form per tenant (tenantA,
// tenantB and testTenant) and one file each for tenantA and tenantB.
func twoTenantServices() Services {
	svc := testServices(
		forms.Form{ID: formOfA, TenantID: tenantA.ID, Title: "A's form", Slug: "a"},
		forms.Form{ID: formOfB, TenantID: tenantB.ID, Title: "B's form", Slug: "b"},
		forms.Form{ID: formOfAPIKey, TenantID: testTenant.ID, Title: "Key form", Slug: "k"},
	)
	svc.tenants = &fakeTenants{owned: map[int32]tenants.Tenant{2: tenantA, 3: tenantB}}
	svc.files.(*fakeFiles).files[fileOfA] = files.File{ID: fileOfA, TenantID: tenantA.ID}
	svc.files.(*fakeFiles).files[fileOfB] = files.File{ID: fileOfB, TenantID: tenantB.ID}
	return svc
}

func TestTenantOrUserResolvesTheUsersTenant(t *testing.T) {
	svc := twoTenantServices()
	for _, authorization := range []string{
		bearer(tokenFor(t, svc, "creator")),
		basicHeader("creator", testPassword),
	} {
		rec := serve(svc, userRequest(http.MethodGet, "/tenants/me", "", authorization))
		assertStatus(t, rec, http.StatusOK)
		if body := decode[TenantResponse](t, rec); body.ID != tenantA.ID {
			t.Fatalf("tenant = %d, want %d", body.ID, tenantA.ID)
		}

		rec = serve(svc, userRequest(http.MethodGet, "/forms", "", authorization))
		assertStatus(t, rec, http.StatusOK)
		list := decode[FormListResponse](t, rec)
		if len(list.Items) != 1 || list.Items[0].ID != formOfA {
			t.Fatalf("forms = %+v", list.Items)
		}
	}

	rec := serve(svc, userRequest(http.MethodPost, "/forms", validForm, bearer(tokenFor(t, svc, "basic"))))
	assertStatus(t, rec, http.StatusCreated)
	if got := svc.forms.(*fakeForms).created.TenantID; got != tenantB.ID {
		t.Fatalf("form created for tenant %d, want %d", got, tenantB.ID)
	}
}

func TestTenantOrUserIsolatesTenants(t *testing.T) {
	svc := twoTenantServices()
	tokenA := bearer(tokenFor(t, svc, "creator"))
	formB := "/forms/2"
	for _, tt := range []struct {
		method, path, body string
	}{
		{http.MethodGet, formB, ""},
		{http.MethodPut, formB, validForm},
		{http.MethodDelete, formB, ""},
		{http.MethodGet, formB + "/submissions", ""},
		{http.MethodGet, formB + "/webhooks", ""},
		{http.MethodPost, formB + "/webhooks", `{"target_url":"https://example.com/hook"}`},
		{http.MethodDelete, "/files/" + fileOfB, ""},
		// The API key's tenant is not reachable either.
		{http.MethodGet, "/forms/3", ""},
	} {
		rec := serve(svc, userRequest(tt.method, tt.path, tt.body, tokenA))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404 (body %s)", tt.method, tt.path, rec.Code, rec.Body.String())
		}
	}
	if _, ok := svc.forms.(*fakeForms).forms[formOfB]; !ok {
		t.Fatal("user A deleted user B's form")
	}
	if _, ok := svc.files.(*fakeFiles).files[fileOfB]; !ok {
		t.Fatal("user A deleted user B's file")
	}

	// Each user still reaches their own data.
	assertStatus(t, serve(svc, userRequest(http.MethodGet, "/forms/1", "", tokenA)), http.StatusOK)
	assertStatus(t, serve(svc, userRequest(http.MethodDelete, "/files/"+fileOfA, "", tokenA)), http.StatusNoContent)
	assertStatus(t, serve(svc, userRequest(http.MethodGet, formB, "", bearer(tokenFor(t, svc, "basic")))), http.StatusOK)
}

func TestTenantOrUserAPIKeyUnchanged(t *testing.T) {
	svc := twoTenantServices()
	rec := do(t, svc, http.MethodGet, "/forms", "", true)
	assertStatus(t, rec, http.StatusOK)
	if list := decode[FormListResponse](t, rec); len(list.Items) != 1 || list.Items[0].ID != formOfAPIKey {
		t.Fatalf("forms = %+v", list.Items)
	}
	assertStatus(t, do(t, svc, http.MethodGet, "/forms/3", "", true), http.StatusOK)
	assertStatus(t, do(t, svc, http.MethodGet, "/forms/1", "", true), http.StatusNotFound)
	assertStatus(t, do(t, svc, http.MethodDelete, "/files/"+fileOfA, "", true), http.StatusNotFound)

	// The API key wins over an Authorization header.
	req := userRequest(http.MethodGet, "/tenants/me", "", bearer(tokenFor(t, svc, "creator")))
	req.Header.Set(apiKeyHeader, testAPIKey)
	rec = serve(svc, req)
	assertStatus(t, rec, http.StatusOK)
	if body := decode[TenantResponse](t, rec); body.ID != testTenant.ID {
		t.Fatalf("tenant = %d, want %d", body.ID, testTenant.ID)
	}
}

func TestTenantOrUserRejections(t *testing.T) {
	svc := twoTenantServices()
	tests := []struct {
		name, authorization, apiKey string
		want                        int
	}{
		{"no credentials", "", "", http.StatusUnauthorized},
		{"invalid API key", "", "wrong", http.StatusUnauthorized},
		{"bad token", bearer("not.a.token"), "", http.StatusUnauthorized},
		{"wrong password", basicHeader("creator", "nope"), "", http.StatusUnauthorized},
		{"unknown scheme", "Digest abc", "", http.StatusUnauthorized},
		{"user without workspace", bearer(tokenFor(t, svc, "admin")), "", http.StatusForbidden},
	}
	for _, tt := range tests {
		for _, path := range []string{"/tenants/me", "/forms", "/forms/1/submissions"} {
			req := userRequest(http.MethodGet, path, "", tt.authorization)
			if tt.apiKey != "" {
				req.Header.Set(apiKeyHeader, tt.apiKey)
			}
			if rec := serve(svc, req); rec.Code != tt.want {
				t.Errorf("%s: GET %s = %d, want %d", tt.name, path, rec.Code, tt.want)
			}
		}
	}
}

// cookieRequest returns userRequest carrying the session cookie of username
// and, when origin is not empty, an Origin header.
func cookieRequest(t *testing.T, svc Services, method, path, body, username, origin string) *http.Request {
	t.Helper()
	req := userRequest(method, path, body, "")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tokenFor(t, svc, username)})
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return req
}

func TestTenantOrUserAcceptsSessionCookie(t *testing.T) {
	svc := twoTenantServices()
	const origin = "http://example.com" // httptest.NewRequest's host

	rec := serve(svc, cookieRequest(t, svc, http.MethodGet, "/forms", "", "creator", ""))
	assertStatus(t, rec, http.StatusOK)
	if list := decode[FormListResponse](t, rec); len(list.Items) != 1 || list.Items[0].ID != formOfA {
		t.Fatalf("forms = %+v", list.Items)
	}
	assertStatus(t, serve(svc, cookieRequest(t, svc, http.MethodGet, "/forms/1", "", "creator", "")), http.StatusOK)

	rec = serve(svc, cookieRequest(t, svc, http.MethodPost, "/forms", validForm, "creator", origin))
	assertStatus(t, rec, http.StatusCreated)
	if got := svc.forms.(*fakeForms).created.TenantID; got != tenantA.ID {
		t.Fatalf("form created for tenant %d, want %d", got, tenantA.ID)
	}

	rec = serve(svc, cookieRequest(t, svc, http.MethodPut, "/forms/1", validForm, "creator", origin))
	assertStatus(t, rec, http.StatusOK)
	if got := svc.forms.(*fakeForms).updated; got.ID != formOfA || got.TenantID != tenantA.ID {
		t.Fatalf("updated = %+v", got)
	}

	// Without an Origin header, same-origin is told by Sec-Fetch-Site.
	req := cookieRequest(t, svc, http.MethodDelete, "/forms/1", "", "creator", "")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	assertStatus(t, serve(svc, req), http.StatusNoContent)
	if _, ok := svc.forms.(*fakeForms).forms[formOfA]; ok {
		t.Fatal("form of A was not deleted")
	}
}

func TestTenantOrUserSessionCookieRejections(t *testing.T) {
	svc := twoTenantServices()
	const origin = "http://example.com"
	tests := []struct {
		name, method, path, body, user, origin, fetchSite string
		want                                              int
	}{
		{"cross-origin PUT", http.MethodPut, "/forms/1", validForm, "creator", "https://evil.test", "", http.StatusForbidden},
		{"cross-origin DELETE", http.MethodDelete, "/forms/1", "", "creator", "https://evil.test", "", http.StatusForbidden},
		{"cross-site DELETE without Origin", http.MethodDelete, "/forms/1", "", "creator", "", "cross-site", http.StatusForbidden},
		{"cross-origin POST", http.MethodPost, "/forms", validForm, "creator", "https://evil.test", "", http.StatusForbidden},
		{"other user's form GET", http.MethodGet, "/forms/2", "", "creator", "", "", http.StatusNotFound},
		{"other user's form PUT", http.MethodPut, "/forms/2", validForm, "creator", origin, "", http.StatusNotFound},
		{"other user's form DELETE", http.MethodDelete, "/forms/2", "", "creator", origin, "", http.StatusNotFound},
		{"API key tenant's form", http.MethodGet, "/forms/3", "", "creator", "", "", http.StatusNotFound},
		{"user without workspace", http.MethodGet, "/forms", "", "admin", "", "", http.StatusForbidden},
	}
	for _, tt := range tests {
		req := cookieRequest(t, svc, tt.method, tt.path, tt.body, tt.user, tt.origin)
		if tt.fetchSite != "" {
			req.Header.Set("Sec-Fetch-Site", tt.fetchSite)
		}
		if rec := serve(svc, req); rec.Code != tt.want {
			t.Errorf("%s: %s %s = %d, want %d (body %s)", tt.name, tt.method, tt.path, rec.Code, tt.want, rec.Body.String())
		}
	}
	if _, ok := svc.forms.(*fakeForms).forms[formOfA]; !ok {
		t.Fatal("a cross-origin request deleted A's form")
	}
	if _, ok := svc.forms.(*fakeForms).forms[formOfB]; !ok {
		t.Fatal("user A deleted user B's form")
	}
	// The fake records the attempt on B's form; the cross-origin PUT on A's
	// never reaches the handler.
	if got := svc.forms.(*fakeForms).updated; got.ID == formOfA {
		t.Fatalf("a cross-origin request updated form %+v", got)
	}

	forged := userRequest(http.MethodGet, "/forms", "", "")
	forged.AddCookie(&http.Cookie{Name: sessionCookie, Value: "forged"})
	assertStatus(t, serve(svc, forged), http.StatusUnauthorized)
}

func TestTenantOrUserHeadersWinOverSessionCookie(t *testing.T) {
	svc := twoTenantServices()

	// The API key wins over the session cookie.
	req := cookieRequest(t, svc, http.MethodGet, "/tenants/me", "", "creator", "")
	req.Header.Set(apiKeyHeader, testAPIKey)
	rec := serve(svc, req)
	assertStatus(t, rec, http.StatusOK)
	if body := decode[TenantResponse](t, rec); body.ID != testTenant.ID {
		t.Fatalf("tenant = %d, want %d", body.ID, testTenant.ID)
	}

	// So does a Bearer token, and a bad one is not rescued by the cookie.
	req = cookieRequest(t, svc, http.MethodGet, "/tenants/me", "", "creator", "")
	req.Header.Set("Authorization", bearer(tokenFor(t, svc, "basic")))
	rec = serve(svc, req)
	assertStatus(t, rec, http.StatusOK)
	if body := decode[TenantResponse](t, rec); body.ID != tenantB.ID {
		t.Fatalf("tenant = %d, want %d", body.ID, tenantB.ID)
	}
	req = cookieRequest(t, svc, http.MethodGet, "/tenants/me", "", "creator", "")
	req.Header.Set("Authorization", bearer("not.a.token"))
	assertStatus(t, serve(svc, req), http.StatusUnauthorized)

	// Header credentials need no same-origin check: browsers never add them
	// to cross-site requests on their own.
	req = userRequest(http.MethodDelete, "/forms/1", "", bearer(tokenFor(t, svc, "creator")))
	req.Header.Set("Origin", "https://evil.test")
	assertStatus(t, serve(svc, req), http.StatusNoContent)
}
