package handlers

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"app/internal/pkg/auth"
	"app/internal/pkg/auth/oidc"
)

// testClientID is the client ID registered at fakeProvider.
const testClientID = "gogoform"

// fakeProvider is a minimal OpenID Connect provider: discovery document,
// JWKS, and a token endpoint that checks PKCE and returns an RS256 ID token
// built from the claims registered with the code.
type fakeProvider struct {
	*httptest.Server
	key *rsa.PrivateKey

	mu    sync.Mutex
	codes map[string]fakeGrant
}

// fakeGrant is what the provider remembers about an issued code.
type fakeGrant struct {
	challenge string
	claims    map[string]any
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{key: key, codes: map[string]fakeGrant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{
			"issuer":                                p.URL,
			"authorization_endpoint":                p.URL + "/authorize",
			"token_endpoint":                        p.URL + "/token",
			"jwks_uri":                              p.URL + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "test",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("POST /token", p.token)
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	return p
}

// token redeems a code: the PKCE verifier must hash to the challenge sent
// to /authorize, as a real provider checks.
func (p *fakeProvider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	grant, ok := p.codes[r.PostForm.Get("code")]
	delete(p.codes, r.PostForm.Get("code"))
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != grant.challenge {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	writeTestJSON(w, map[string]any{
		"access_token": "provider-access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     p.sign(grant.claims),
	})
}

// sign returns claims as an RS256 JWT.
func (p *fakeProvider) sign(claims map[string]any) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signingInput := enc(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "test"}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// authorize plays the provider's /authorize step for the redirect URL the
// app sent the browser to: it checks the request and returns a code whose
// ID token has the default claims for that flow, changed by edit.
func (p *fakeProvider) authorize(t *testing.T, location string, edit func(claims map[string]any)) (code, state string) {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil || !strings.HasPrefix(location, p.URL+"/authorize?") {
		t.Fatalf("redirected to %q, want the provider's authorization endpoint", location)
	}
	q := u.Query()
	if q.Get("response_type") != "code" || q.Get("client_id") != testClientID || q.Get("code_challenge_method") != "S256" ||
		q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("nonce") == "" || !strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("authorization request %v is missing parameters", q)
	}
	now := time.Now()
	claims := map[string]any{
		"iss":                p.URL,
		"sub":                "subject-1",
		"aud":                testClientID,
		"iat":                now.Unix(),
		"exp":                now.Add(time.Hour).Unix(),
		"nonce":              q.Get("nonce"),
		"email":              "alice@example.com",
		"email_verified":     true,
		"preferred_username": "Alice",
	}
	if edit != nil {
		edit(claims)
	}
	code = fmt.Sprintf("code-%d", now.UnixNano())
	p.mu.Lock()
	p.codes[code] = fakeGrant{challenge: q.Get("code_challenge"), claims: claims}
	p.mu.Unlock()
	return code, q.Get("state")
}

func writeTestJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// oidcTest is an app with OIDC sign-in against a fakeProvider.
type oidcTest struct {
	provider *fakeProvider
	svc      Services
	handler  http.Handler
}

// oidcTestConfig tunes newOIDCTest.
type oidcTestConfig struct {
	domains       []string
	passwordLogin bool
	now           func() time.Time
}

func newOIDCTest(t *testing.T, cfg oidcTestConfig) *oidcTest {
	t.Helper()
	p := newFakeProvider(t)
	client, err := oidc.New(context.Background(), oidc.Config{
		IssuerURL:           p.URL,
		ClientID:            testClientID,
		ClientSecret:        "client-secret",
		RedirectURL:         "http://example.com/app/oidc/callback",
		Scopes:              []string{"email", "profile"},
		AllowedEmailDomains: cfg.domains,
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := testServices()
	svc.auth = newTestAuthWith(auth.Options{
		PasswordLoginDisabled: !cfg.passwordLogin,
		External:              auth.ExternalOptions{AutoCreateUsers: true},
	})
	svc = svc.WithOIDC(OIDCOptions{Client: client, ProviderName: "TestIdP", FlowKey: testSecret, Now: cfg.now})
	return &oidcTest{provider: p, svc: svc, handler: Routes(svc, testAppConfig)}
}

func (o *oidcTest) serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	o.handler.ServeHTTP(rec, req)
	return rec
}

// login starts a flow and returns the provider URL and the flow cookie.
func (o *oidcTest) login(t *testing.T, next string) (string, *http.Cookie) {
	t.Helper()
	rec := o.serve(httptest.NewRequest(http.MethodGet, "/app/oidc/login?next="+url.QueryEscape(next), nil))
	assertStatus(t, rec, http.StatusFound)
	flow := findCookie(rec, oidcFlowCookie)
	if flow == nil || flow.Value == "" || !flow.HttpOnly || flow.Path != oidcFlowPath || flow.SameSite != http.SameSiteLaxMode {
		t.Fatalf("flow cookie = %+v, want an HttpOnly, SameSite=Lax cookie scoped to %s", flow, oidcFlowPath)
	}
	return rec.Header().Get("Location"), flow
}

// callback sends the provider's redirect back to the app.
func (o *oidcTest) callback(query url.Values, flow *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/app/oidc/callback?"+query.Encode(), nil)
	if flow != nil {
		req.AddCookie(flow)
	}
	return o.serve(req)
}

// run goes through login → provider → callback with the claims edited by
// edit and returns the callback response.
func (o *oidcTest) run(t *testing.T, edit func(map[string]any)) *httptest.ResponseRecorder {
	t.Helper()
	location, flow := o.login(t, "/app/account")
	code, state := o.provider.authorize(t, location, edit)
	return o.callback(url.Values{"code": {code}, "state": {state}}, flow)
}

func findCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestOIDCLoginAndCallback(t *testing.T) {
	o := newOIDCTest(t, oidcTestConfig{passwordLogin: true})

	location, flow := o.login(t, "/app/users?x=1")
	code, state := o.provider.authorize(t, location, nil)
	rec := o.callback(url.Values{"code": {code}, "state": {state}}, flow)

	assertStatus(t, rec, http.StatusSeeOther)
	if got := rec.Header().Get("Location"); got != "/app/users?x=1" {
		t.Fatalf("redirect = %q, want the next page", got)
	}
	session := findCookie(rec, sessionCookie)
	if session == nil || session.Value == "" || !session.HttpOnly || session.Path != "/app" {
		t.Fatalf("session cookie = %+v", session)
	}
	if cleared := findCookie(rec, oidcFlowCookie); cleared == nil || cleared.MaxAge >= 0 {
		t.Fatalf("flow cookie not cleared: %+v", cleared)
	}
	u, err := o.svc.auth.CheckToken(context.Background(), session.Value)
	if err != nil || u.Username != "alice" || u.Role != auth.RoleBasic {
		t.Fatalf("session user = %+v, %v; want a new BASIC alice", u, err)
	}

	// Signing in again finds the same user.
	rec = o.run(t, nil)
	assertStatus(t, rec, http.StatusSeeOther)
	again, err := o.svc.auth.CheckToken(context.Background(), findCookie(rec, sessionCookie).Value)
	if err != nil || again.ID != u.ID {
		t.Fatalf("second sign-in = %+v, %v; want user %d", again, err, u.ID)
	}

	// The account page shows the linked identity.
	req := httptest.NewRequest(http.MethodGet, "/app/account", nil)
	req.AddCookie(findCookie(rec, sessionCookie))
	rec = o.serve(req)
	assertStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `id="linked-identities"`) {
		t.Fatal("account page does not show the linked identity")
	}
}

func TestOIDCLoginRejectsOpenRedirect(t *testing.T) {
	o := newOIDCTest(t, oidcTestConfig{passwordLogin: true})
	location, flow := o.login(t, "https://evil.example.com/")
	code, state := o.provider.authorize(t, location, nil)
	rec := o.callback(url.Values{"code": {code}, "state": {state}}, flow)
	assertStatus(t, rec, http.StatusSeeOther)
	if got := rec.Header().Get("Location"); got != defaultSignInRedirect {
		t.Fatalf("redirect = %q, want %q", got, defaultSignInRedirect)
	}
}

func TestOIDCCallbackRejections(t *testing.T) {
	tests := []struct {
		name   string
		cfg    oidcTestConfig
		edit   func(map[string]any)
		mutate func(q url.Values, flow *http.Cookie) (url.Values, *http.Cookie)
		status int
		want   string
	}{
		{
			name: "bad state",
			mutate: func(q url.Values, flow *http.Cookie) (url.Values, *http.Cookie) {
				q.Set("state", "forged")
				return q, flow
			},
			status: http.StatusBadRequest, want: "does not match",
		},
		{
			name: "missing flow cookie",
			mutate: func(q url.Values, _ *http.Cookie) (url.Values, *http.Cookie) {
				return q, nil
			},
			status: http.StatusBadRequest, want: "expired",
		},
		{
			name: "tampered flow cookie",
			mutate: func(q url.Values, flow *http.Cookie) (url.Values, *http.Cookie) {
				payload, sig, _ := strings.Cut(flow.Value, ".")
				raw, _ := base64.RawURLEncoding.DecodeString(payload)
				raw = []byte(strings.Replace(string(raw), `"next":"/app/account"`, `"next":"/app/users"`, 1))
				return q, &http.Cookie{Name: flow.Name, Value: base64.RawURLEncoding.EncodeToString(raw) + "." + sig}
			},
			status: http.StatusBadRequest, want: "expired",
		},
		{
			name: "missing code",
			mutate: func(q url.Values, flow *http.Cookie) (url.Values, *http.Cookie) {
				q.Del("code")
				return q, flow
			},
			status: http.StatusBadRequest, want: "authorization code",
		},
		{
			name:   "bad nonce",
			edit:   func(c map[string]any) { c["nonce"] = "another-flow" },
			status: http.StatusUnauthorized, want: "could not be verified",
		},
		{
			name:   "wrong audience",
			edit:   func(c map[string]any) { c["aud"] = "another-client" },
			status: http.StatusUnauthorized, want: "could not be verified",
		},
		{
			name:   "wrong issuer",
			edit:   func(c map[string]any) { c["iss"] = "https://evil.example.com" },
			status: http.StatusUnauthorized, want: "could not be verified",
		},
		{
			name:   "expired token",
			edit:   func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() },
			status: http.StatusUnauthorized, want: "could not be verified",
		},
		{
			name:   "unverified email with a domain allowlist",
			cfg:    oidcTestConfig{domains: []string{"example.com"}},
			edit:   func(c map[string]any) { c["email_verified"] = false },
			status: http.StatusForbidden, want: "verified email",
		},
		{
			name:   "email outside the domain allowlist",
			cfg:    oidcTestConfig{domains: []string{"corp.example.com"}},
			status: http.StatusForbidden, want: "verified email",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := newOIDCTest(t, tt.cfg)
			location, flow := o.login(t, "/app/account")
			code, state := o.provider.authorize(t, location, tt.edit)
			q := url.Values{"code": {code}, "state": {state}}
			if tt.mutate != nil {
				q, flow = tt.mutate(q, flow)
			}
			rec := o.callback(q, flow)
			assertStatus(t, rec, tt.status)
			if !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("body does not mention %q:\n%s", tt.want, rec.Body.String())
			}
			if findCookie(rec, sessionCookie) != nil {
				t.Fatal("a session cookie was set")
			}
		})
	}
}

func TestOIDCAllowedDomainAcceptsVerifiedEmail(t *testing.T) {
	o := newOIDCTest(t, oidcTestConfig{domains: []string{"example.com"}})
	rec := o.run(t, func(c map[string]any) { c["email"] = "Bob@EXAMPLE.com"; c["email_verified"] = "true" })
	assertStatus(t, rec, http.StatusSeeOther)
}

func TestOIDCCallbackExpiredFlow(t *testing.T) {
	now := time.Now()
	o := newOIDCTest(t, oidcTestConfig{now: func() time.Time { return now }})
	location, flow := o.login(t, "/app/account")
	code, state := o.provider.authorize(t, location, nil)
	now = now.Add(oidcFlowTTL + time.Second)
	rec := o.callback(url.Values{"code": {code}, "state": {state}}, flow)
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestOIDCCallbackProviderError(t *testing.T) {
	o := newOIDCTest(t, oidcTestConfig{})
	_, flow := o.login(t, "/app/account")
	rec := o.callback(url.Values{"error": {"access_denied"}, "error_description": {"The user said no"}}, flow)
	assertStatus(t, rec, http.StatusBadRequest)
	if body := rec.Body.String(); !strings.Contains(body, "Sign-in cancelled") || !strings.Contains(body, "TestIdP") {
		t.Fatalf("body = %s", body)
	}
}

func TestOIDCSignInPage(t *testing.T) {
	o := newOIDCTest(t, oidcTestConfig{passwordLogin: true})
	rec := o.serve(httptest.NewRequest(http.MethodGet, "/app/signin?next=/app/users", nil))
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if !strings.Contains(body, "Sign in with TestIdP") || !strings.Contains(body, `href="/app/oidc/login?next=%2Fapp%2Fusers"`) {
		t.Fatalf("sign-in page has no OIDC button:\n%s", body)
	}
	if !strings.Contains(body, `id="signin-form"`) {
		t.Fatal("password form hidden while password login is enabled")
	}
}

func TestPasswordLoginDisabled(t *testing.T) {
	o := newOIDCTest(t, oidcTestConfig{passwordLogin: false})

	rec := o.serve(httptest.NewRequest(http.MethodGet, "/app/signin", nil))
	assertStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); strings.Contains(body, `id="signin-form"`) || strings.Contains(body, `data-mode="signup"`) || !strings.Contains(body, "Sign in with TestIdP") {
		t.Fatalf("sign-in page should only offer the provider:\n%s", body)
	}

	basic := func(req *http.Request) *http.Request {
		req.SetBasicAuth("admin", testPassword)
		return req
	}
	appSignIn := basic(httptest.NewRequest(http.MethodPost, "/app/signin", nil))
	appSignIn.Header.Set("Origin", "http://example.com")
	for name, req := range map[string]*http.Request{
		"app sign-in": appSignIn,
		"api sign-in": basic(httptest.NewRequest(http.MethodPost, "/auth/signin", nil)),
		"api basic":   basic(httptest.NewRequest(http.MethodGet, "/auth/me", nil)),
		"api sign-up": httptest.NewRequest(http.MethodPost, "/auth/signup", strings.NewReader(`{"username":"bob","password":"correct horse"}`)),
	} {
		t.Run(name, func(t *testing.T) {
			assertStatus(t, o.serve(req), http.StatusForbidden)
		})
	}

	// The bootstrap users exist and OIDC still works.
	assertStatus(t, o.run(t, nil), http.StatusSeeOther)
}

func TestOIDCRoutesNotRegisteredWhenDisabled(t *testing.T) {
	svc := testServices()
	for _, path := range []string{"/app/oidc/login", "/app/oidc/callback"} {
		assertStatus(t, serve(svc, httptest.NewRequest(http.MethodGet, path, nil)), http.StatusNotFound)
	}
	rec := serve(svc, httptest.NewRequest(http.MethodGet, "/app/signin", nil))
	assertStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); strings.Contains(body, "/app/oidc/login") || !strings.Contains(body, `id="signin-form"`) {
		t.Fatalf("sign-in page without OIDC:\n%s", body)
	}
}
