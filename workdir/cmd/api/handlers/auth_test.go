package handlers

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"app/internal/pkg/auth"
	"app/internal/pkg/forms"
)

// userRequest returns a request authenticated with the given Authorization
// header value, if any.
func userRequest(method, path, body, authorization string) *http.Request {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	return req
}

func basicHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func bearer(token string) string { return "Bearer " + token }

func TestSignUp(t *testing.T) {
	svc := testServices()
	rec := serve(svc, userRequest(http.MethodPost, "/auth/signup", `{"username":"Alice","password":"long enough"}`, ""))
	assertStatus(t, rec, http.StatusCreated)
	if u := decode[UserResponse](t, rec); u.Username != "alice" || u.Role != auth.RoleBasic {
		t.Fatalf("user = %+v", u)
	}

	assertStatus(t, serve(svc, userRequest(http.MethodPost, "/auth/signup", `{"username":"alice","password":"long enough"}`, "")), http.StatusConflict)
	assertStatus(t, serve(svc, userRequest(http.MethodPost, "/auth/signup", `{"username":"bob","password":"short"}`, "")), http.StatusBadRequest)
	// The role cannot be chosen at sign-up.
	assertStatus(t, serve(svc, userRequest(http.MethodPost, "/auth/signup", `{"username":"eve","password":"long enough","role":"ADMIN"}`, "")), http.StatusBadRequest)
}

func TestSignIn(t *testing.T) {
	svc := testServices()
	rec := serve(svc, userRequest(http.MethodPost, "/auth/signin", "", basicHeader("creator", testPassword)))
	assertStatus(t, rec, http.StatusOK)
	tok := decode[TokenResponse](t, rec)
	if tok.TokenType != "Bearer" || tok.AccessToken == "" || tok.User.Role != auth.RoleFormCreator {
		t.Fatalf("token = %+v", tok)
	}

	rec = serve(svc, userRequest(http.MethodGet, "/auth/me", "", bearer(tok.AccessToken)))
	assertStatus(t, rec, http.StatusOK)
	if u := decode[UserResponse](t, rec); u.Username != "creator" {
		t.Fatalf("me = %+v", u)
	}

	assertStatus(t, serve(svc, userRequest(http.MethodPost, "/auth/signin", "", basicHeader("creator", "wrong password"))), http.StatusUnauthorized)
	assertStatus(t, serve(svc, userRequest(http.MethodPost, "/auth/signin", "", "")), http.StatusUnauthorized)
}

func TestMeAuthentication(t *testing.T) {
	svc := testServices()
	tests := []struct {
		name, authorization string
		want                int
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"bearer", bearer(tokenFor(t, svc, "basic")), http.StatusOK},
		{"lowercase scheme", "bearer " + tokenFor(t, svc, "basic"), http.StatusOK},
		{"basic", basicHeader("basic", testPassword), http.StatusOK},
		{"wrong password", basicHeader("basic", "nope"), http.StatusUnauthorized},
		{"bad token", bearer("not.a.token"), http.StatusUnauthorized},
		{"unknown scheme", "Digest abc", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		rec := serve(svc, userRequest(http.MethodGet, "/auth/me", "", tt.authorization))
		if rec.Code != tt.want {
			t.Errorf("%s: status = %d, want %d", tt.name, rec.Code, tt.want)
		}
		if rec.Code == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%s: missing WWW-Authenticate", tt.name)
		}
	}
}

func TestSamplePolicies(t *testing.T) {
	svc := testServices()
	users := map[string]string{
		"anonymous": "",
		"basic":     bearer(tokenFor(t, svc, "basic")),
		"creator":   bearer(tokenFor(t, svc, "creator")),
		"admin":     bearer(tokenFor(t, svc, "admin")),
	}
	tests := []struct {
		path string
		want map[string]int
	}{
		{"/samples/public", map[string]int{"anonymous": 200, "basic": 200, "creator": 200, "admin": 200}},
		{"/samples/signed-in", map[string]int{"anonymous": 401, "basic": 200, "creator": 200, "admin": 200}},
		{"/samples/form-creator", map[string]int{"anonymous": 401, "basic": 403, "creator": 200, "admin": 200}},
		{"/samples/admin", map[string]int{"anonymous": 401, "basic": 403, "creator": 403, "admin": 200}},
	}
	for _, tt := range tests {
		for who, want := range tt.want {
			rec := serve(svc, userRequest(http.MethodGet, tt.path, "", users[who]))
			if rec.Code != want {
				t.Errorf("%s as %s: status = %d, want %d", tt.path, who, rec.Code, want)
			}
		}
	}

	rec := serve(svc, userRequest(http.MethodGet, "/samples/public", "", users["creator"]))
	if body := decode[SampleResponse](t, rec); body.User == nil || body.User.Username != "creator" {
		t.Fatalf("body = %+v", body)
	}
	// Invalid credentials are rejected even on public routes.
	assertStatus(t, serve(svc, userRequest(http.MethodGet, "/samples/public", "", bearer("forged"))), http.StatusUnauthorized)
}

func TestUsersAdminOnly(t *testing.T) {
	svc := testServices()
	admin := bearer(tokenFor(t, svc, "admin"))

	assertStatus(t, serve(svc, userRequest(http.MethodGet, "/users", "", bearer(tokenFor(t, svc, "creator")))), http.StatusForbidden)
	rec := serve(svc, userRequest(http.MethodGet, "/users", "", admin))
	assertStatus(t, rec, http.StatusOK)
	if list := decode[UserListResponse](t, rec); len(list.Items) != 3 {
		t.Fatalf("users = %+v", list.Items)
	}

	rec = serve(svc, userRequest(http.MethodPut, "/users/3/role", `{"role":"FORM_CREATOR"}`, admin))
	assertStatus(t, rec, http.StatusOK)
	if u := decode[UserResponse](t, rec); u.ID != 3 || u.Role != auth.RoleFormCreator {
		t.Fatalf("user = %+v", u)
	}
	// The new role applies to tokens issued before the change.
	assertStatus(t, serve(svc, userRequest(http.MethodGet, "/samples/form-creator", "", basicHeader("basic", testPassword))), http.StatusOK)

	assertStatus(t, serve(svc, userRequest(http.MethodPut, "/users/1/role", `{"role":"BASIC"}`, admin)), http.StatusBadRequest)
	assertStatus(t, serve(svc, userRequest(http.MethodPut, "/users/3/role", `{"role":"ROOT"}`, admin)), http.StatusBadRequest)
	assertStatus(t, serve(svc, userRequest(http.MethodPut, "/users/99/role", `{"role":"BASIC"}`, admin)), http.StatusNotFound)
	assertStatus(t, serve(svc, userRequest(http.MethodPut, "/users/2/role", `{"role":"ADMIN"}`, bearer(tokenFor(t, svc, "creator")))), http.StatusForbidden)
}

// privateForm requires signing in; identifiedForm also records the submitter.
var (
	privateForm    = forms.Form{ID: 3, TenantID: testTenant.ID, Title: "Staff poll", Slug: "staff-poll", IsActive: true, Content: []byte(`{}`), AcceptAnonymous: true}
	identifiedForm = forms.Form{ID: 4, TenantID: testTenant.ID, Title: "Leave request", Slug: "leave", IsActive: true, Content: []byte(`{}`)}
)

func TestPrivateFormRequiresSignIn(t *testing.T) {
	svc := testServices(ownForm, privateForm)
	token := bearer(tokenFor(t, svc, "basic"))

	rec := serve(svc, userRequest(http.MethodGet, "/public/forms/staff-poll", "", ""))
	assertStatus(t, rec, http.StatusUnauthorized)
	rec = serve(svc, userRequest(http.MethodGet, "/public/forms/staff-poll", "", token))
	assertStatus(t, rec, http.StatusOK)
	if body := decode[PublicFormResponse](t, rec); body.PublicAvailable || !body.AcceptAnonymous {
		t.Fatalf("body = %+v", body)
	}

	assertStatus(t, serve(svc, userRequest(http.MethodPost, "/public/forms/staff-poll/submissions", `{"payload":{}}`, "")), http.StatusUnauthorized)
	rec = serve(svc, userRequest(http.MethodPost, "/public/forms/staff-poll/submissions", `{"payload":{}}`, token))
	assertStatus(t, rec, http.StatusCreated)
	// The form accepts anonymous submissions: the user is not recorded.
	if got := svc.submissions.(*fakeSubmissions).created.UserID; got != nil {
		t.Fatalf("user id = %d, want nil", *got)
	}
}

func TestIdentifiedFormRecordsSubmitter(t *testing.T) {
	svc := testServices(identifiedForm)
	assertStatus(t, serve(svc, userRequest(http.MethodPost, "/public/forms/leave/submissions", `{"payload":{}}`, "")), http.StatusUnauthorized)

	rec := serve(svc, userRequest(http.MethodPost, "/public/forms/leave/submissions", `{"payload":{}}`, basicHeader("creator", testPassword)))
	assertStatus(t, rec, http.StatusCreated)
	if body := decode[SubmissionResponse](t, rec); body.UserID == nil || *body.UserID != 2 {
		t.Fatalf("body = %+v", body)
	}
}

func TestPublicFormIgnoresSubmitter(t *testing.T) {
	svc := testServices(ownForm)
	rec := serve(svc, userRequest(http.MethodPost, "/public/forms/contact/submissions", `{"payload":{}}`, bearer(tokenFor(t, svc, "basic"))))
	assertStatus(t, rec, http.StatusCreated)
	if got := svc.submissions.(*fakeSubmissions).created.UserID; got != nil {
		t.Fatalf("user id = %d, want nil", *got)
	}
}

func TestCreateFormAccessFlags(t *testing.T) {
	svc := testServices()
	rec := do(t, svc, http.MethodPost, "/forms", `{"title":"Poll","slug":"poll","content":{},"public_available":false,"accept_anonymous":false}`, true)
	assertStatus(t, rec, http.StatusCreated)
	in := svc.forms.(*fakeForms).created
	if in.PublicAvailable == nil || *in.PublicAvailable || in.AcceptAnonymous == nil || *in.AcceptAnonymous {
		t.Fatalf("created = %+v", in)
	}
}
