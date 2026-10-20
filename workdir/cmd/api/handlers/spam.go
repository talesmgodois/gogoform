package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	apperrors "app/internal/errors"
)

// Messages of the spam checks of public form submissions. They are shown to
// respondents, so they stay generic and never say which check failed.
const (
	msgSubmissionRejected = "your submission could not be accepted; reload the page and try again"
	msgTooFast            = "that was quick! Take a moment to review your answers, then submit again"
	msgVerificationFailed = "verification failed; complete the challenge and submit again"
)

// turnstileVerifyURL is the Cloudflare Turnstile siteverify endpoint.
const turnstileVerifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// turnstileOrigin is the origin the Turnstile widget script and iframe are
// loaded from, allowed by the public pages' Content-Security-Policy when
// Turnstile is enabled.
const turnstileOrigin = "https://challenges.cloudflare.com"

// TurnstileVerifier checks a Cloudflare Turnstile token sent by a public form
// page. Verify returns (false, nil) for a token Cloudflare rejects and an
// error only when the verification itself could not be completed.
type TurnstileVerifier interface {
	Verify(ctx context.Context, token, remoteIP string) (bool, error)
}

// cloudflareTurnstile verifies tokens against the Cloudflare siteverify API
// with the secret key of the site.
type cloudflareTurnstile struct {
	secret string
	client *http.Client
	url    string
}

// newCloudflareTurnstile returns a TurnstileVerifier for secret with a short
// timeout, so a slow Cloudflare cannot hold submissions for long.
func newCloudflareTurnstile(secret string) *cloudflareTurnstile {
	return &cloudflareTurnstile{secret: secret, client: &http.Client{Timeout: 5 * time.Second}, url: turnstileVerifyURL}
}

// Verify implements TurnstileVerifier.
func (t *cloudflareTurnstile) Verify(ctx context.Context, token, remoteIP string) (bool, error) {
	form := url.Values{"secret": {t.secret}, "response": {token}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, strings.NewReader(form.Encode()))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := t.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("turnstile: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return false, fmt.Errorf("turnstile: siteverify returned HTTP %d", res.StatusCode)
	}
	var body struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return false, fmt.Errorf("turnstile: decode response: %w", err)
	}
	return body.Success, nil
}

// spamGuard runs the spam checks of public form submissions: a honeypot
// field that only bots fill in, a minimum completion time, and Cloudflare
// Turnstile when a verifier is configured.
type spamGuard struct {
	// minSeconds is the minimum completion time; 0 disables the check.
	minSeconds int32
	// turnstile is nil when Turnstile is not configured.
	turnstile TurnstileVerifier
	ip        clientIPResolver
}

// check returns a 400 for a submission flagged as spam, or the error of a
// Turnstile verification that could not be completed. Submissions are
// refused rather than silently dropped so a real respondent tripping a
// check can fix it and resubmit.
func (g spamGuard) check(r *http.Request, req CreateSubmissionRequest) error {
	if req.Website != "" {
		return apperrors.NewBadRequest(msgSubmissionRejected)
	}
	if g.minSeconds > 0 && (req.CompletionTimeSeconds == nil || *req.CompletionTimeSeconds < g.minSeconds) {
		return apperrors.NewBadRequest(msgTooFast)
	}
	if g.turnstile == nil {
		return nil
	}
	if req.TurnstileToken == "" {
		return apperrors.NewBadRequest(msgVerificationFailed)
	}
	ok, err := g.turnstile.Verify(r.Context(), req.TurnstileToken, g.ip.resolve(r))
	if err != nil {
		return apperrors.NewInternal(err)
	}
	if !ok {
		return apperrors.NewBadRequest(msgVerificationFailed)
	}
	return nil
}
