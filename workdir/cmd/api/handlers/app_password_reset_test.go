package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// resetTokenPattern extracts the plaintext token from a rendered password
// reset link ("https://forms.example.com/app/reset-password?token=...").
var resetTokenPattern = regexp.MustCompile(`token=([A-Za-z0-9_-]+)`)

// lastPasswordResetToken returns the token of the most recent password-reset
// email the log mailer of svc sent to email.
func lastPasswordResetToken(t *testing.T, svc Services, email string) string {
	t.Helper()
	sent := mailerFor(svc).Sent()
	for i := len(sent) - 1; i >= 0; i-- {
		msg := sent[i]
		if msg.To != email || msg.Subject != "Reset your password" {
			continue
		}
		m := resetTokenPattern.FindStringSubmatch(msg.Text)
		if m == nil {
			t.Fatalf("no token in password reset email: %q", msg.Text)
		}
		return m[1]
	}
	t.Fatalf("no password reset email sent to %q", email)
	return ""
}

// postForm sends a same-origin, application/x-www-form-urlencoded POST.
func postForm(svc Services, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return serve(svc, req)
}

func TestAppForgotPasswordKnownAndUnknownEmail(t *testing.T) {
	svc := testServices()
	const email = "alice@example.com"
	signUpAndGetToken(t, svc, email) // discard the sign-up verification token

	before := len(mailerFor(svc).Sent())
	known := postForm(svc, "/app/forgot-password", url.Values{"email": {email}})
	assertStatus(t, known, http.StatusOK)
	if body := known.Body.String(); !strings.Contains(body, "Check your email") {
		t.Fatalf("body = %s", body)
	}
	if after := len(mailerFor(svc).Sent()); after != before+1 {
		t.Fatalf("sent %d messages for a known email, want %d", after, before+1)
	}

	before = len(mailerFor(svc).Sent())
	unknown := postForm(svc, "/app/forgot-password", url.Values{"email": {"nobody@example.com"}})
	assertStatus(t, unknown, http.StatusOK)
	// The pages differ only by their per-request CSP nonce.
	if withoutNonces(unknown.Body.String()) != withoutNonces(known.Body.String()) {
		t.Fatalf("response differs for a known vs unknown email: %q vs %q", known.Body.String(), unknown.Body.String())
	}
	if after := len(mailerFor(svc).Sent()); after != before {
		t.Fatalf("sent %d messages for an unknown email, want %d", after, before)
	}
}

func TestAppForgotPasswordPage(t *testing.T) {
	svc := testServices()
	rec := serve(svc, httptest.NewRequest(http.MethodGet, "/app/forgot-password", nil))
	assertStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); !strings.Contains(body, `action="/app/forgot-password"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestAppResetPasswordSuccessThenSignIn(t *testing.T) {
	svc := testServices()
	const email = "bob@example.com"
	signUpAndGetToken(t, svc, email)

	assertStatus(t, postForm(svc, "/app/forgot-password", url.Values{"email": {email}}), http.StatusOK)
	token := lastPasswordResetToken(t, svc, email)

	page := serve(svc, httptest.NewRequest(http.MethodGet, "/app/reset-password?token="+token, nil))
	assertStatus(t, page, http.StatusOK)
	if body := page.Body.String(); !strings.Contains(body, `value="`+token+`"`) {
		t.Fatalf("body = %s", body)
	}

	const newPassword = "new secret password"
	rec := postForm(svc, "/app/reset-password", url.Values{"token": {token}, "password": {newPassword}})
	assertStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/app/signin" {
		t.Fatalf("Location = %q", loc)
	}

	// The old password no longer works; the new one does.
	oldPw := httptest.NewRequest(http.MethodPost, "/app/signin", nil)
	oldPw.SetBasicAuth(email, testPassword)
	assertStatus(t, serve(svc, oldPw), http.StatusUnauthorized)

	newPw := httptest.NewRequest(http.MethodPost, "/app/signin", nil)
	newPw.SetBasicAuth(email, newPassword)
	assertStatus(t, serve(svc, newPw), http.StatusOK)

	// The token is single use.
	reused := postForm(svc, "/app/reset-password", url.Values{"token": {token}, "password": {"another password"}})
	assertStatus(t, reused, http.StatusBadRequest)
	if body := reused.Body.String(); !strings.Contains(body, "invalid") && !strings.Contains(body, "expired") {
		t.Fatalf("body = %s", body)
	}
}

func TestAppResetPasswordInvalidOrExpiredToken(t *testing.T) {
	svc := testServices()
	rec := postForm(svc, "/app/reset-password", url.Values{"token": {"bogus"}, "password": {"a good password"}})
	assertStatus(t, rec, http.StatusBadRequest)
	if body := rec.Body.String(); !strings.Contains(body, "invalid") && !strings.Contains(body, "expired") {
		t.Fatalf("body = %s", body)
	}

	page := serve(svc, httptest.NewRequest(http.MethodGet, "/app/reset-password", nil))
	assertStatus(t, page, http.StatusBadRequest)
	if body := page.Body.String(); !strings.Contains(body, "Invalid link") {
		t.Fatalf("body = %s", body)
	}
}

func TestAppResetPasswordWeakPasswordKeepsToken(t *testing.T) {
	svc := testServices()
	const email = "carol@example.com"
	signUpAndGetToken(t, svc, email)
	assertStatus(t, postForm(svc, "/app/forgot-password", url.Values{"email": {email}}), http.StatusOK)
	token := lastPasswordResetToken(t, svc, email)

	rec := postForm(svc, "/app/reset-password", url.Values{"token": {token}, "password": {"short"}})
	assertStatus(t, rec, http.StatusBadRequest)
	if body := rec.Body.String(); !strings.Contains(body, `value="`+token+`"`) {
		t.Fatalf("body does not keep the token for a retry: %s", body)
	}

	// The token still works with a valid password.
	ok := postForm(svc, "/app/reset-password", url.Values{"token": {token}, "password": {"a fine password"}})
	assertStatus(t, ok, http.StatusSeeOther)
}

func TestAppForgotPasswordCrossOriginRejected(t *testing.T) {
	svc := testServices()
	req := httptest.NewRequest(http.MethodPost, "/app/forgot-password", strings.NewReader(url.Values{"email": {"alice@example.com"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.test")
	assertStatus(t, serve(svc, req), http.StatusForbidden)
}

// nonceAttr matches the per-request nonce of the inline scripts.
var nonceAttr = regexp.MustCompile(`nonce="[^"]*"`)

// withoutNonces returns body with the nonces of its scripts blanked.
func withoutNonces(body string) string {
	return nonceAttr.ReplaceAllString(body, `nonce=""`)
}
