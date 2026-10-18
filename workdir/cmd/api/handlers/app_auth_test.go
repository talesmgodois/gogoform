package handlers

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

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
		{"/app/billing", "", http.StatusSeeOther},
		{"/app/billing", "basic", http.StatusOK},
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
	for _, want := range []string{"creator", "FORM_CREATOR", "GET /app/users", "Roles: ADMIN", "Signed in · own workspace"} {
		if !strings.Contains(body, want) {
			t.Errorf("account page misses %q", want)
		}
	}
	// Tabs only list the pages the viewer may open.
	if !strings.Contains(body, `href="/app/account"`) || strings.Contains(body, `href="/app/users"`) {
		t.Error("nav tabs do not follow the route access")
	}
}

func TestAppHeaderNavigation(t *testing.T) {
	svc := testServices()
	rec := serve(svc, pageRequest(t, svc, "/app/account", "creator"))
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	nonce := regexp.MustCompile(`'nonce-([A-Za-z0-9_=-]+)'`).FindStringSubmatch(rec.Header().Get("Content-Security-Policy"))
	if nonce == nil {
		t.Fatal("no nonce in the Content-Security-Policy")
	}
	for _, want := range []string{
		`href="/app" class="px-4`, `>Forms</a>`,
		`href="/app/account" class="px-4`, `aria-current="page">Account</a>`,
		`href="/app/billing" class="px-4`, `>Billing</a>`,
		`data-signout`, `>Sign out</button>`,
		`<script nonce="` + nonce[1] + `">`, `fetch("/app/signout", { method: "POST"`, `location.assign("/app/signin")`,
		"Your forms and submissions",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("header misses %q", want)
		}
	}
	for _, unwanted := range []string{`href="/app/builder" class="px-4`, `>All tenants</a>`, `>admin</span>`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("header shows %q to a non-admin", unwanted)
		}
	}

	admin := serve(svc, pageRequest(t, svc, "/app/admin", "admin")).Body.String()
	for _, want := range []string{`aria-current="page">All tenants</a>`, `>Users</a>`, `>admin</span>`, "Forms and files across every tenant"} {
		if !strings.Contains(admin, want) {
			t.Errorf("admin header misses %q", want)
		}
	}

	signin := serve(svc, pageRequest(t, svc, "/app/signin", "")).Body.String()
	if strings.Contains(signin, "data-signout") || strings.Contains(signin, `href="/app/billing"`) {
		t.Error("sign-in page shows the signed-in navigation")
	}
}

func TestAppBillingPlaceholder(t *testing.T) {
	svc := testServices()
	rec := serve(svc, pageRequest(t, svc, "/app/billing", "basic"))
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{"Billing is coming soon", `aria-current="page">Billing</a>`} {
		if !strings.Contains(body, want) {
			t.Errorf("billing page misses %q", want)
		}
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
	if len(cookies) != 1 || cookies[0].Name != sessionCookie || !cookies[0].HttpOnly || cookies[0].Path != "/" || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookies = %+v", cookies)
	}
	page := httptest.NewRequest(http.MethodGet, "/app/account", nil)
	page.AddCookie(cookies[0])
	assertStatus(t, serve(svc, page), http.StatusOK)

	// The tenant API accepts the session cookie ("basic" has no workspace
	// here, hence 403 rather than 401), the Guard routes do not.
	forms := httptest.NewRequest(http.MethodGet, "/forms", nil)
	forms.AddCookie(cookies[0])
	assertStatus(t, serve(svc, forms), http.StatusForbidden)
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

func TestDescribeAccess(t *testing.T) {
	tests := []struct {
		rt   appRoute
		want string
	}{
		{appRoute{Access: auth.Public}, "Public"},
		{appRoute{Access: auth.SignedIn()}, "Signed in"},
		{appRoute{Access: auth.SignedIn(auth.RoleFormCreator, auth.RoleBasic)}, "Roles: FORM_CREATOR, BASIC"},
		{appRoute{Access: auth.SignedIn(), Workspace: true}, "Signed in · own workspace"},
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
