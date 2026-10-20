package handlers

import (
	"net/http"
	"strconv"
)

// permissionsPolicy turns off the powerful browser features no page of the
// application uses, so an injected script or a framing site cannot ask for
// them.
const permissionsPolicy = "camera=(), microphone=(), geolocation=(), payment=(), usb=(), " +
	"accelerometer=(), gyroscope=(), magnetometer=()"

// defaultContentSecurityPolicy forbids framing every response that does not
// set its own policy (the JSON API, Swagger UI, error pages). The /app and
// public pages replace it with their full policy; only the embed mode of the
// public pages may be framed by other sites (see embedFrameAncestors).
const defaultContentSecurityPolicy = "frame-ancestors 'none'"

// corsMaxAge is how long, in seconds, browsers may cache a CORS preflight
// response of the public form endpoints.
const corsMaxAge = 600

// securityHeaders sets the headers shared by every response: no MIME
// sniffing, no Referer leaking page URLs (which may carry tokens, e.g. the
// password reset link) to other sites, the
// permissionsPolicy and defaultContentSecurityPolicy. Handlers may override
// them (Content-Security-Policy in particular). HSTS is set by the reverse
// proxy, which terminates TLS.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", permissionsPolicy)
		h.Set("Content-Security-Policy", defaultContentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}

// publicCORS lets any origin call next, one of the public form endpoints a
// site embedding a form calls directly (GET /public/forms/{slug} and its
// submissions/files). The origin is the wildcard, so browsers
// never attach cookies to these cross-origin requests: the session cookie of
// the /app pages stays same-origin. Every other route sends no CORS headers,
// so browsers keep blocking cross-origin reads of it.
func publicCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Expose-Headers", "Retry-After")
		next(w, r)
	}
}

// publicCORSPreflight answers the CORS preflight (OPTIONS) request of a
// public form endpoint served with methods; see publicCORS.
func publicCORSPreflight(methods string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", methods)
		h.Set("Access-Control-Allow-Headers", "Content-Type, Accept, Authorization")
		h.Set("Access-Control-Max-Age", strconv.Itoa(corsMaxAge))
		w.WriteHeader(http.StatusNoContent)
	}
}
