package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"app/internal/config"
)

// fakeTurnstile is a TurnstileVerifier that accepts the token "valid",
// recording the last call, or fails with err when set.
type fakeTurnstile struct {
	err             error
	token, remoteIP string
	calls           int
}

func (f *fakeTurnstile) Verify(_ context.Context, token, remoteIP string) (bool, error) {
	f.calls++
	f.token, f.remoteIP = token, remoteIP
	if f.err != nil {
		return false, f.err
	}
	return token == "valid", nil
}

// submitWith posts body to the submissions of ownForm through Routes with
// security, and returns the response.
func submitWith(t *testing.T, svc Services, security config.SecurityConfig, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/public/forms/contact/submissions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "203.0.113.7:5555"
	rec := httptest.NewRecorder()
	Routes(svc, testFeatures, testStripe, testApp, security).ServeHTTP(rec, req)
	return rec
}

// assertNotCreated fails the test when svc stored a submission.
func assertNotCreated(t *testing.T, svc Services) {
	t.Helper()
	if fake := svc.submissions.(*fakeSubmissions); fake.created.FormID != 0 {
		t.Fatalf("created = %+v, want no submission", fake.created)
	}
}

func TestCreateSubmissionHoneypot(t *testing.T) {
	svc := testServices(ownForm)
	rec := submitWith(t, svc, testSecurity, `{"payload":{},"website":"http://spam.example"}`)
	assertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), msgSubmissionRejected) {
		t.Fatalf("body = %s", rec.Body)
	}
	assertNotCreated(t, svc)

	assertStatus(t, submitWith(t, svc, testSecurity, `{"payload":{},"website":""}`), http.StatusCreated)
}

func TestCreateSubmissionMinimumTime(t *testing.T) {
	security := config.SecurityConfig{MinFormSeconds: 3}
	for _, body := range []string{
		`{"payload":{}}`,
		`{"payload":{},"completion_time_seconds":0}`,
		`{"payload":{},"completion_time_seconds":2}`,
	} {
		svc := testServices(ownForm)
		rec := submitWith(t, svc, security, body)
		assertStatus(t, rec, http.StatusBadRequest)
		if !strings.Contains(rec.Body.String(), msgTooFast) {
			t.Fatalf("%s: body = %s", body, rec.Body)
		}
		assertNotCreated(t, svc)
	}
	svc := testServices(ownForm)
	assertStatus(t, submitWith(t, svc, security, `{"payload":{},"completion_time_seconds":3}`), http.StatusCreated)
}

func TestCreateSubmissionTurnstile(t *testing.T) {
	security := config.SecurityConfig{TurnstileSecret: "secret", TurnstileSiteKey: "site-key"}
	cases := []struct {
		name   string
		body   string
		err    error
		status int
	}{
		{name: "valid token", body: `{"payload":{},"turnstile_token":"valid"}`, status: http.StatusCreated},
		{name: "rejected token", body: `{"payload":{},"turnstile_token":"forged"}`, status: http.StatusBadRequest},
		{name: "missing token", body: `{"payload":{}}`, status: http.StatusBadRequest},
		{name: "verifier down", body: `{"payload":{},"turnstile_token":"valid"}`, err: errors.New("timeout"), status: http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := testServices(ownForm)
			verifier := &fakeTurnstile{err: tc.err}
			svc.turnstile = verifier
			rec := submitWith(t, svc, security, tc.body)
			assertStatus(t, rec, tc.status)
			if tc.status != http.StatusCreated {
				assertNotCreated(t, svc)
			}
			if verifier.calls > 0 && verifier.remoteIP != "203.0.113.7" {
				t.Fatalf("remoteIP = %q", verifier.remoteIP)
			}
		})
	}
}

func TestCreateSubmissionTurnstileDisabled(t *testing.T) {
	// Without a secret, no verifier runs and no token is needed.
	svc := testServices(ownForm)
	assertStatus(t, submitWith(t, svc, testSecurity, `{"payload":{}}`), http.StatusCreated)
}

func TestPublicFormPageTurnstile(t *testing.T) {
	svc := testServices(publicSurvey())
	svc.turnstile = &fakeTurnstile{}
	security := config.SecurityConfig{TurnstileSecret: "secret", TurnstileSiteKey: "site-key"}
	rec := httptest.NewRecorder()
	Routes(svc, testFeatures, testStripe, testApp, security).ServeHTTP(rec, pageRequest(t, svc, "/f/feedback", ""))
	assertStatus(t, rec, http.StatusOK)

	body := rec.Body.String()
	for _, want := range []string{`class="cf-turnstile`, `data-sitekey="site-key"`, `src="https://challenges.cloudflare.com/turnstile/v0/api.js"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'nonce-", " " + turnstileOrigin + ";", "frame-src " + turnstileOrigin, "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q does not contain %q", csp, want)
		}
	}
}

func TestPublicFormPageHoneypotWithoutTurnstile(t *testing.T) {
	svc := testServices(publicSurvey())
	rec := serve(svc, pageRequest(t, svc, "/f/feedback", ""))
	assertStatus(t, rec, http.StatusOK)

	body := rec.Body.String()
	for _, want := range []string{`id="hp-website" name="website" tabindex="-1"`, `website: document.getElementById("hp-website").value`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	if strings.Contains(body, "cf-turnstile\"") || strings.Contains(body, "turnstile/v0/api.js") {
		t.Error("page renders Turnstile while it is disabled")
	}
	if csp := rec.Header().Get("Content-Security-Policy"); strings.Contains(csp, turnstileOrigin) || strings.Contains(csp, "frame-src") {
		t.Errorf("CSP %q allows Turnstile while it is disabled", csp)
	}
}

func TestCloudflareTurnstileVerify(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got, _ = url.ParseQuery(string(raw))
		if got.Get("response") == "good" {
			io.WriteString(w, `{"success":true}`)
			return
		}
		io.WriteString(w, `{"success":false,"error-codes":["invalid-input-response"]}`)
	}))
	defer srv.Close()
	v := newCloudflareTurnstile("secret")
	v.url = srv.URL

	ok, err := v.Verify(context.Background(), "good", "203.0.113.7")
	if err != nil || !ok {
		t.Fatalf("Verify(good) = %v, %v", ok, err)
	}
	if got.Get("secret") != "secret" || got.Get("remoteip") != "203.0.113.7" {
		t.Fatalf("form = %v", got)
	}
	if ok, err := v.Verify(context.Background(), "bad", ""); err != nil || ok {
		t.Fatalf("Verify(bad) = %v, %v", ok, err)
	}

	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	if _, err := v.Verify(context.Background(), "good", ""); err == nil {
		t.Fatal("Verify on HTTP 502 succeeded")
	}
}
