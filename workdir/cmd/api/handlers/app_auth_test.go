package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"app/internal/config"
	"app/internal/pkg/auth"
	"app/internal/pkg/forms"
)

// pageRequest returns a GET request for path carrying the session cookie of
// username, or none when username is empty.
func pageRequest(t *testing.T, svc Services, path, username string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if username != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tokenFor(t, svc, username)})
	}
	return req
}

func TestAppPageAccess(t *testing.T) {
	svc := testServices()
	tests := []struct {
		path, user string
		want       int
	}{
		{"/app/signin", "", http.StatusOK},
		{"/app/account", "", http.StatusSeeOther},
		{"/app/account", "basic", http.StatusOK},
		{"/app/users", "", http.StatusSeeOther},
		{"/app/users", "creator", http.StatusForbidden},
		{"/app/users", "admin", http.StatusOK},
	}
	for _, tt := range tests {
		rec := serve(svc, pageRequest(t, svc, tt.path, tt.user))
		if rec.Code != tt.want {
			t.Errorf("%s as %q: status = %d, want %d", tt.path, tt.user, rec.Code, tt.want)
		}
	}
}

func TestAppAnonymousRedirectsToSignIn(t *testing.T) {
	svc := testServices()
	rec := serve(svc, pageRequest(t, svc, "/app/users?x=1", ""))
	assertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/app/signin?next=%2Fapp%2Fusers%3Fx%3D1" {
		t.Fatalf("Location = %q", loc)
	}
}

func TestAppInvalidSessionIsCleared(t *testing.T) {
	svc := testServices()
	req := httptest.NewRequest(http.MethodGet, "/app/account", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "forged"})
	rec := serve(svc, req)
	assertStatus(t, rec, http.StatusSeeOther)
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].Name != sessionCookie || c[0].MaxAge >= 0 {
		t.Fatalf("cookies = %+v, want the session cleared", c)
	}
}

func TestAppAccountListsRoutes(t *testing.T) {
	svc := testServices()
	rec := serve(svc, pageRequest(t, svc, "/app/account", "creator"))
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{"creator", "FORM_CREATOR", "GET /app/users", "Roles: ADMIN", "operator credentials"} {
		if !strings.Contains(body, want) {
			t.Errorf("account page misses %q", want)
		}
	}
	// Tabs only list the pages the viewer may open.
	if !strings.Contains(body, `href="/app/account"`) || strings.Contains(body, `href="/app/users"`) {
		t.Error("nav tabs do not follow the route access")
	}
}

func TestAppSignIn(t *testing.T) {
	svc := testServices()
	req := httptest.NewRequest(http.MethodPost, "/app/signin", nil)
	req.SetBasicAuth("basic", testPassword)
	req.Header.Set("Origin", "http://example.com")
	rec := serve(svc, req)
	assertStatus(t, rec, http.StatusOK)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookie || !cookies[0].HttpOnly || cookies[0].Path != "/app" || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookies = %+v", cookies)
	}
	page := httptest.NewRequest(http.MethodGet, "/app/account", nil)
	page.AddCookie(cookies[0])
	assertStatus(t, serve(svc, page), http.StatusOK)

	// The session cookie is not accepted by the API.
	api := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	api.AddCookie(cookies[0])
	assertStatus(t, serve(svc, api), http.StatusUnauthorized)
}

func TestAppSignInRejected(t *testing.T) {
	svc := testServices()

	wrong := httptest.NewRequest(http.MethodPost, "/app/signin", nil)
	wrong.SetBasicAuth("basic", "wrong password")
	rec := serve(svc, wrong)
	assertStatus(t, rec, http.StatusUnauthorized)
	// No Basic challenge, so browsers do not show their own prompt.
	if h := rec.Header().Get("WWW-Authenticate"); h != "" {
		t.Fatalf("WWW-Authenticate = %q", h)
	}

	cross := httptest.NewRequest(http.MethodPost, "/app/signin", nil)
	cross.SetBasicAuth("basic", testPassword)
	cross.Header.Set("Origin", "https://evil.test")
	assertStatus(t, serve(svc, cross), http.StatusForbidden)

	signout := httptest.NewRequest(http.MethodPost, "/app/signout", nil)
	signout.Header.Set("Sec-Fetch-Site", "cross-site")
	assertStatus(t, serve(svc, signout), http.StatusForbidden)
}

func TestAppSignOut(t *testing.T) {
	rec := serve(testServices(), httptest.NewRequest(http.MethodPost, "/app/signout", nil))
	assertStatus(t, rec, http.StatusNoContent)
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].MaxAge >= 0 {
		t.Fatalf("cookies = %+v, want the session cleared", c)
	}
}

func TestAppSignInPageRedirectsSignedInUsers(t *testing.T) {
	svc := testServices()
	rec := serve(svc, pageRequest(t, svc, "/app/signin?next=/app/users", "admin"))
	assertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/app/users" {
		t.Fatalf("Location = %q", loc)
	}
}

func TestSafeNext(t *testing.T) {
	tests := map[string]string{
		"":                      defaultSignInRedirect,
		"/app":                  "/app",
		"/app/users?x=1":        "/app/users?x=1",
		"/app/signin":           defaultSignInRedirect,
		"/application":          defaultSignInRedirect,
		"/swagger/":             defaultSignInRedirect,
		"https://evil.test/app": defaultSignInRedirect,
		"//evil.test/app":       defaultSignInRedirect,
		`/app\..\evil`:          defaultSignInRedirect,
		"javascript:alert(1)":   defaultSignInRedirect,
	}
	for in, want := range tests {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAppUserPagesWithoutOperatorCredentials(t *testing.T) {
	svc := testServices()
	h := Routes(svc, config.AppConfig{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, pageRequest(t, svc, "/app/account", "basic"))
	assertStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), `href="/app/builder"`) {
		t.Error("nav lists the unmounted builder")
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, pageRequest(t, svc, "/app/builder", "admin"))
	assertStatus(t, rec, http.StatusNotFound)
}

func TestDescribeAccess(t *testing.T) {
	tests := []struct {
		rt   appRoute
		want string
	}{
		{appRoute{Access: auth.Public}, "Public"},
		{appRoute{Access: auth.SignedIn()}, "Signed in"},
		{appRoute{Access: auth.SignedIn(auth.RoleFormCreator, auth.RoleBasic)}, "Roles: FORM_CREATOR, BASIC"},
		{appRoute{Access: auth.Public, Operator: true}, "Public + operator credentials"},
	}
	for _, tt := range tests {
		if got := describeAccess(tt.rt); got != tt.want {
			t.Errorf("describeAccess = %q, want %q", got, tt.want)
		}
	}
}

func TestFormAccess(t *testing.T) {
	tests := []struct {
		public, anonymous bool
		want              string
	}{
		{true, true, "Public"},
		{false, true, "Private · anonymous"},
		{false, false, "Private · identified"},
	}
	for _, tt := range tests {
		if got := formAccess(forms.Form{PublicAvailable: tt.public, AcceptAnonymous: tt.anonymous}).Label; got != tt.want {
			t.Errorf("formAccess(%v, %v) = %q, want %q", tt.public, tt.anonymous, got, tt.want)
		}
	}
}
