package handlers

import (
	"net/http"
	"strconv"

	"app/internal/config"
	apperrors "app/internal/errors"
	"app/internal/ratelimit"
)

// rateLimitGroup names an independently limited class of routes, matching
// the per-group knobs of config.RateLimitConfig.
type rateLimitGroup string

const (
	rateLimitSignin           rateLimitGroup = "signin"
	rateLimitSignup           rateLimitGroup = "signup"
	rateLimitForgotPassword   rateLimitGroup = "forgot_password"
	rateLimitPublicSubmission rateLimitGroup = "public_submission"
	rateLimitPublicUpload     rateLimitGroup = "public_upload"
)

// rateLimiters holds one token-bucket ratelimit.Limiter per rateLimitGroup
// and the clientIPResolver used to key them, so every group limits the same
// notion of "client" that submission metadata records.
type rateLimiters struct {
	limiters map[rateLimitGroup]*ratelimit.Limiter
	ip       clientIPResolver
}

// newRateLimiters builds the limiters of every group from cfg. A group whose
// PerMinute or Burst is less than 1 (the zero value, unset) gets a nil
// Limiter and is left unlimited; config.Config.validate rejects such values
// once loaded through Load, so in practice this only matters for a
// SecurityConfig built directly (as tests that don't care about rate
// limiting do).
func newRateLimiters(cfg config.SecurityConfig) *rateLimiters {
	g := func(c config.RateLimitGroupConfig) *ratelimit.Limiter {
		if c.PerMinute < 1 || c.Burst < 1 {
			return nil
		}
		return ratelimit.New(c.PerMinute, c.Burst)
	}
	return &rateLimiters{
		ip: newClientIPResolver(cfg),
		limiters: map[rateLimitGroup]*ratelimit.Limiter{
			rateLimitSignin:           g(cfg.RateLimit.Signin),
			rateLimitSignup:           g(cfg.RateLimit.Signup),
			rateLimitForgotPassword:   g(cfg.RateLimit.ForgotPassword),
			rateLimitPublicSubmission: g(cfg.RateLimit.PublicSubmission),
			rateLimitPublicUpload:     g(cfg.RateLimit.PublicUpload),
		},
	}
}

// limit wraps next so a client IP that has exhausted group's bucket gets a
// 429 with a Retry-After header instead of reaching the handler. next is
// returned unwrapped when group has no Limiter (see newRateLimiters).
func (rl *rateLimiters) limit(group rateLimitGroup, next http.HandlerFunc) http.HandlerFunc {
	limiter := rl.limiters[group]
	if limiter == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if allowed, retryAfter := limiter.Allow(rl.ip.limitKey(r)); !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
			apperrors.WriteHTTP(w, r, apperrors.NewRateLimited("too many requests; try again later"))
			return
		}
		next(w, r)
	}
}
