package handlers

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// verifyTokenPattern extracts the plaintext token from a rendered
// verification link ("https://forms.example.com/app/verify?token=...").
var verifyTokenPattern = regexp.MustCompile(`token=([A-Za-z0-9_-]+)`)

// signUpAndGetToken signs email up through POST /auth/signup and returns the
// verification token the log mailer recorded for it.
func signUpAndGetToken(t *testing.T, svc Services, email string) string {
	t.Helper()
	rec := serve(svc, userRequest(http.MethodPost, "/auth/signup", `{"email":"`+email+`","password":"`+testPassword+`"}`, ""))
	assertStatus(t, rec, http.StatusCreated)
	return lastVerificationToken(t, svc, email)
}

// lastVerificationToken returns the token of the most recent verification
// email the log mailer of svc sent to email.
func lastVerificationToken(t *testing.T, svc Services, email string) string {
	t.Helper()
	sent := mailerFor(svc).Sent()
	for i := len(sent) - 1; i >= 0; i-- {
		msg := sent[i]
		if msg.To != email || msg.Subject != "Verify your email address" {
			continue
		}
		m := verifyTokenPattern.FindStringSubmatch(msg.Text)
		if m == nil {
			t.Fatalf("no token in verification email: %q", msg.Text)
		}
		return m[1]
	}
	t.Fatalf("no verification email sent to %q", email)
	return ""
}

func TestAppVerifyEmail(t *testing.T) {
	svc := testServices()
	const email = "alice@example.com"
	token := signUpAndGetToken(t, svc, email)

	rec := serve(svc, httptest.NewRequest(http.MethodGet, "/app/verify?token="+token, nil))
	assertStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); !strings.Contains(body, "Email verified") {
		t.Fatalf("body = %s", body)
	}

	// Signing in now shows the account as verified, with no banner.
	signin := serve(svc, userRequest(http.MethodPost, "/app/signin", "", basicHeader(email, testPassword)))
	cookie := signin.Result().Cookies()[0]
	page := httptest.NewRequest(http.MethodGet, "/app/account", nil)
	page.AddCookie(cookie)
	rec = serve(svc, page)
	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if !strings.Contains(body, "Verified") || strings.Contains(body, "Unverified") {
		t.Fatalf("account page does not show the email as verified: %s", body)
	}
	if strings.Contains(body, "Resend verification email") {
		t.Error("verified user still sees the resend banner")
	}

	// The token is single use.
	rec = serve(svc, httptest.NewRequest(http.MethodGet, "/app/verify?token="+token, nil))
	assertStatus(t, rec, http.StatusBadRequest)
	if body := rec.Body.String(); !strings.Contains(body, "invalid") && !strings.Contains(body, "expired") {
		t.Fatalf("body = %s", body)
	}
}

func TestAppVerifyEmailInvalidToken(t *testing.T) {
	svc := testServices()
	rec := serve(svc, httptest.NewRequest(http.MethodGet, "/app/verify?token=bogus", nil))
	assertStatus(t, rec, http.StatusBadRequest)

	rec = serve(svc, httptest.NewRequest(http.MethodGet, "/app/verify", nil))
	assertStatus(t, rec, http.StatusBadRequest)
	if body := rec.Body.String(); !strings.Contains(body, "Invalid link") {
		t.Fatalf("body = %s", body)
	}
}

func TestAppVerifyBannerAndResend(t *testing.T) {
	svc := testServices()
	const email = "bob@example.com"
	signUpAndGetToken(t, svc, email) // discard the sign-up token; only the count matters below

	signin := serve(svc, userRequest(http.MethodPost, "/app/signin", "", basicHeader(email, testPassword)))
	cookie := signin.Result().Cookies()[0]

	account := httptest.NewRequest(http.MethodGet, "/app/account", nil)
	account.AddCookie(cookie)
	rec := serve(svc, account)
	assertStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); !strings.Contains(body, "Verify your email address to publish forms.") || !strings.Contains(body, "Resend verification email") {
		t.Fatalf("account page does not show the verification banner: %s", body)
	}

	before := len(mailerFor(svc).Sent())
	resend := httptest.NewRequest(http.MethodPost, "/app/verify/resend", nil)
	resend.AddCookie(cookie)
	resend.Header.Set("Origin", "http://example.com")
	assertStatus(t, serve(svc, resend), http.StatusNoContent)
	if after := len(mailerFor(svc).Sent()); after != before+1 {
		t.Fatalf("sent %d messages after resend, want %d", after, before+1)
	}

	// Cross-origin resend requests are rejected.
	cross := httptest.NewRequest(http.MethodPost, "/app/verify/resend", nil)
	cross.AddCookie(cookie)
	cross.Header.Set("Origin", "https://evil.test")
	assertStatus(t, serve(svc, cross), http.StatusForbidden)

	// Fixed test users have no email, so they never see the banner.
	noBanner := serve(svc, pageRequest(t, svc, "/app/account", "creator"))
	if strings.Contains(noBanner.Body.String(), "Resend verification email") {
		t.Error("banner shown to a user without an email")
	}
}
