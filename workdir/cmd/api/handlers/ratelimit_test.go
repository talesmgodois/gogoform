package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"app/internal/config"
	apperrors "app/internal/errors"
)

// rateLimitTestConfig returns a SecurityConfig with every group limited
// identically to perMinute/burst, trusting trustedProxies (a comma-separated
// CIDR list, as in config.toml).
func rateLimitTestConfig(perMinute, burst int, trustedProxies string) config.SecurityConfig {
	g := config.RateLimitGroupConfig{PerMinute: perMinute, Burst: burst}
	return config.SecurityConfig{
		TrustedProxies: trustedProxies,
		RateLimit: config.RateLimitConfig{
			Signin: g, Signup: g, ForgotPassword: g, PublicSubmission: g, PublicUpload: g,
		},
	}
}

// okHandler always responds 200, so a test can tell "reached the handler"
// apart from "blocked by the limiter".
func okHandler(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

func rateLimitRequest(remoteAddr, xForwardedFor string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.RemoteAddr = remoteAddr
	if xForwardedFor != "" {
		r.Header.Set("X-Forwarded-For", xForwardedFor)
	}
	return r
}

func TestRateLimiterAllowsBurstThenBlocks(t *testing.T) {
	rl := newRateLimiters(rateLimitTestConfig(60, 2, ""))
	h := rl.limit(rateLimitSignin, okHandler)

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h(rec, rateLimitRequest("203.0.113.9:1234", ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 (within burst)", i, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	h(rec, rateLimitRequest("203.0.113.9:1234", ""))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd request: status = %d, want 429", rec.Code)
	}
}

func TestRateLimiterWrites429WithRetryAfterAndCode(t *testing.T) {
	rl := newRateLimiters(rateLimitTestConfig(60, 1, ""))
	h := rl.limit(rateLimitSignin, okHandler)

	h(httptest.NewRecorder(), rateLimitRequest("203.0.113.9:1234", ""))

	rec := httptest.NewRecorder()
	h(rec, rateLimitRequest("203.0.113.9:1234", ""))
	assertStatus(t, rec, http.StatusTooManyRequests)

	ra := rec.Header().Get("Retry-After")
	if secs, err := strconv.Atoi(ra); err != nil || secs < 1 {
		t.Fatalf("Retry-After = %q, want a positive integer", ra)
	}
	body := decode[apperrors.HTTPErrorResponse](t, rec)
	if body.Code != apperrors.CodeRateLimited {
		t.Fatalf("code = %q, want %q", body.Code, apperrors.CodeRateLimited)
	}
}

func TestRateLimiterGroupsAreIndependent(t *testing.T) {
	rl := newRateLimiters(rateLimitTestConfig(60, 1, ""))
	signin := rl.limit(rateLimitSignin, okHandler)
	signup := rl.limit(rateLimitSignup, okHandler)

	assertStatus(t, httptestDo(signin, rateLimitRequest("203.0.113.9:1234", "")), http.StatusOK)
	assertStatus(t, httptestDo(signin, rateLimitRequest("203.0.113.9:1234", "")), http.StatusTooManyRequests)

	// The same IP's signup bucket is untouched by the signin group above.
	assertStatus(t, httptestDo(signup, rateLimitRequest("203.0.113.9:1234", "")), http.StatusOK)
}

func TestRateLimiterUnconfiguredGroupIsUnlimited(t *testing.T) {
	// The zero value (as used by most handler tests, via testSecurity)
	// leaves every group unlimited rather than blocking everything.
	rl := newRateLimiters(config.SecurityConfig{})
	h := rl.limit(rateLimitSignin, okHandler)

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		h(rec, rateLimitRequest("203.0.113.9:1234", ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 (unlimited group)", i, rec.Code)
		}
	}
}

func TestRateLimiterTrustsForwardedForOnlyFromTrustedProxy(t *testing.T) {
	rl := newRateLimiters(rateLimitTestConfig(60, 1, "203.0.113.0/24"))
	h := rl.limit(rateLimitSignin, okHandler)

	// Untrusted peer: X-Forwarded-For is ignored, so both requests share the
	// peer's own bucket despite differing XFF values, and the second blocks.
	assertStatus(t, httptestDo(h, rateLimitRequest("198.51.100.5:1111", "10.0.0.1")), http.StatusOK)
	assertStatus(t, httptestDo(h, rateLimitRequest("198.51.100.5:2222", "10.0.0.2")), http.StatusTooManyRequests)

	// Trusted proxy: X-Forwarded-For identifies the respondent, so two
	// different respondents behind it get independent buckets...
	assertStatus(t, httptestDo(h, rateLimitRequest("203.0.113.9:1111", "10.0.0.1")), http.StatusOK)
	assertStatus(t, httptestDo(h, rateLimitRequest("203.0.113.9:2222", "10.0.0.2")), http.StatusOK)
	// ...but the same respondent behind it twice is still blocked.
	assertStatus(t, httptestDo(h, rateLimitRequest("203.0.113.9:3333", "10.0.0.1")), http.StatusTooManyRequests)
}

// httptestDo runs h against req and returns the recorder, for tests that
// only need the response of a single call inline.
func httptestDo(h http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestRouteLimitingIsWiredIntoSignup(t *testing.T) {
	sec := rateLimitTestConfig(60, 2, "")
	h := Routes(testServices(), testFeatures, testStripe, testApp, sec)

	signupReq := func() *http.Request {
		return httptest.NewRequest(http.MethodPost, "/auth/signup",
			strings.NewReader(`{"email":"a@example.com","password":"long enough password"}`))
	}
	for _, r := range []*http.Request{signupReq(), signupReq()} {
		r.RemoteAddr = "203.0.113.9:1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("unexpected 429 within the burst: %d", rec.Code)
		}
	}

	r := signupReq()
	r.RemoteAddr = "203.0.113.9:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	assertStatus(t, rec, http.StatusTooManyRequests)
}
