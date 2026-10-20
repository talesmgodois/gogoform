# 000027 security_hardening

- Rate limiting middleware (in-memory token bucket per IP, keyed by route group): signin, signup, forgot-password, public submissions, public uploads. Return 429 with `Retry-After`. Respect `X-Forwarded-For` only from trusted proxies (config).
- Spam protection on public forms: honeypot field + minimum completion time; optional Cloudflare Turnstile when `TURNSTILE_SECRET` is set.
- Refuse to start in production without `AUTH_JWT_SECRET` (currently a random secret is generated with a warning).
- Compare Basic/API key secrets in constant time.
- CORS: allow cross-origin only for the public form JSON/submission endpoints; `frame-ancestors` only on the embed route.
- Security headers on all HTML responses: `X-Content-Type-Options`, `Referrer-Policy`, `Permissions-Policy`; HSTS is handled by Caddy (000028).
- Webhook target URLs: block private/loopback IPs (SSRF) when they are saved.

## Tests
- Rate limiter, honeypot and startup config validation tests.

<!-- RUNNER:PLAN -->
## Plan

1. [x] Config and startup validation: add a security config section in `internal/config/config.go` with trusted proxy CIDRs, rate-limit knobs per route group, the minimum form completion seconds and the optional `TURNSTILE_SECRET`/site key. Make `Config.validate` refuse any non-development `server.env` when `AUTH_JWT_SECRET` is missing, and keep the random-secret fallback in `cmd/api/main.go` (`newAuthService`) for development only. Update `config.toml`, `.env.example` and `internal/config/config_test.go`, including tests for the startup check.
2. [x] Rate limiting: add an in-memory token-bucket limiter keyed by client IP and route group, with an injectable clock and eviction of idle buckets. Add a client-IP helper that trusts `X-Forwarded-For` only when `RemoteAddr` is in a trusted proxy CIDR, and use it for the submission metadata `clientIP` too. Wrap `POST /auth/signin`, `POST /app/signin`, `POST /auth/signup`, `POST /app/forgot-password`, `POST /public/forms/{slug}/submissions` and `POST /public/forms/{slug}/files` in `routes.go`/`app_routes.go`. Return 429 with `Retry-After`, using a new `apperrors` rate_limited code. Add limiter and middleware tests: burst, refill, separate groups, trusted vs untrusted XFF, and the 429 headers.
3. [x] Spam protection on public forms: add a hidden honeypot field to `templates/public_form.html` and send it with the existing completion time. In `submissionsController.Create`, reject submissions that fill the honeypot or finish faster than the configured minimum. When `TURNSTILE_SECRET` is set, render the Turnstile widget (allowing it in `publicContentSecurityPolicy`) and verify the token server-side through an injectable verifier. Add handler tests for the honeypot, the minimum time and Turnstile pass/fail, using a fake verifier.
4. [x] Headers, CORS and constant-time secrets: add middleware that sets `X-Content-Type-Options: nosniff`, `Referrer-Policy` and `Permissions-Policy` on every HTML response (/app pages, `/f/{slug}`, message pages). Allow cross-origin requests, including OPTIONS preflight, only on `GET /public/forms/{slug}`, the public submission endpoint and the public upload endpoint. Allow framing only on the embed route (`/f/{slug}?embed=1`) and keep `frame-ancestors 'none'` everywhere else. Compare the API key in `tenantOrUser`/`tenants.GetByAPIKey` with `subtle.ConstantTimeCompare`, and do the same for any other Basic or plain secret comparison. Add tests for the headers, CORS on allowed vs other routes, and frame-ancestors on embed vs non-embed.
5. [x] Webhook SSRF and wrap-up: extend `validateTargetURL` in `cmd/api/handlers/webhooks.go` to resolve hostnames when a webhook is saved, through an injectable resolver. Reject the URL if any resolved address is loopback, private, link-local, CGNAT, unspecified or multicast, including IPv4-mapped IPv6. Add tests for literal IPs and for hostnames that resolve to private addresses. Then update the swagger annotations for 429/CORS and run `make swagger`, update the handlers/config `AGENTS.md` notes, and run `make check` and the full `make test`.
