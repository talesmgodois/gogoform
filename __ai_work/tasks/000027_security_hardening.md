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
