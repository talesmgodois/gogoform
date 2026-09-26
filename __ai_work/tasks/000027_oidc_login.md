# 000027 oidc_login

Let users sign in with an external OpenID Connect provider (Keycloak, Authentik, Auth0, Okta, Google, Microsoft Entra, GitLab, ...). This is an open source project, so it must work with any standards-compliant provider, be configured only through config/env vars, and stay fully optional: with OIDC unset the app behaves exactly as today.

## Current state (read before planning)

- Accounts live in the `users` table (`username` unique and lowercase, `password_hash` NOT NULL, `role`, `is_active`), see `workdir/db/.../migrations/20260925150000_add_users_and_form_access.sql`. Migrations may be in `db/migrations` or, if task 000021 already ran, in `db/postgres/migrations`: use whichever exists. If a `db/sqlite` folder exists too, add the matching SQLite migration and queries as well.
- `internal/pkg/auth` holds the domain: `Service` (SignUp, SignIn, CheckPassword, CheckToken, EnsureUser), `Repository` port, `UserStore` on top of sqlc, and the HS256 `tokenSigner` that issues the app's own JWTs.
- The API authenticates with `Authorization: Bearer <app jwt>` or HTTP Basic (`cmd/api/handlers/auth.go`, `Guard`).
- The `/app` pages keep the app JWT in the `gogoform_session` HttpOnly cookie, scoped to `/app` (`cmd/api/handlers/app_auth.go`); sign-in UI is `templates/signin.html`, routes are in `app_routes.go`.
- Config is `internal/config` (TOML + `env` tags). `applyEnv` only supports string and int fields today.

## Design (follow it unless the code makes it impossible, and say why)

OIDC only *authenticates* the person. Once the ID token is verified, the app finds or creates the local user and issues its **own** JWT exactly as a password sign-in does, so `Guard`, `CheckToken`, roles and the session cookie are unchanged. Provider tokens are never stored or accepted by the API.

Libraries: `github.com/coreos/go-oidc/v3/oidc` and `golang.org/x/oauth2`. Pick versions compatible with the Go version in `workdir/go.mod`, or bump the Go version consistently (go.mod, Dockerfile, CI) if needed.

## Requirements

1. **Config** (`[oidc]` section in `config.toml`, commented out, plus env vars, documented in `.env.example`):
   - `OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` (secret optional, for public clients using PKCE only), `OIDC_REDIRECT_URL` (e.g. `http://localhost:8080/app/oidc/callback`).
   - `OIDC_PROVIDER_NAME`: label shown on the button ("Sign in with <name>"), default `SSO`.
   - `OIDC_SCOPES`: comma-separated, default `openid,email,profile`; `openid` is always included.
   - `OIDC_ALLOWED_EMAIL_DOMAINS`: comma-separated; when set, only users with a verified email in these domains may sign in.
   - `OIDC_AUTO_CREATE_USERS` (default true): create a local user on first login; when false, only already linked identities may sign in.
   - `OIDC_DEFAULT_ROLE` (default `BASIC`): role of users created through OIDC.
   - `AUTH_PASSWORD_LOGIN_ENABLED` (default true): lets operators run SSO-only. When false, password sign-in, sign-up and HTTP Basic on the API are rejected, but the bootstrap admin from `AUTH_ADMIN_*` is still created.
   - OIDC is enabled when issuer, client id and redirect URL are all set; setting only some of them is a validation error. Validate URLs and the role. Extend `applyEnv` to support `bool` fields (and use a `[]string`-friendly representation or split comma-separated strings in the config). Error messages must never include the client secret.

2. **Schema**: new migration adding
   - `user_identities` (`id`, `user_id` FK → users ON DELETE CASCADE, `issuer`, `subject`, `email` nullable, `created_at`, `last_login_at`, UNIQUE(`issuer`, `subject`), index on `user_id`);
   - `users.password_hash` becomes nullable, so OIDC-only users have no password.
   Add the sqlc queries (get identity by issuer+subject joined to an active user, create identity, update `last_login_at`) and regenerate. Creating the user and its identity must be atomic (one transaction, or a single statement with a CTE).

3. **Domain** (`internal/pkg/auth`):
   - Extend the `Repository` port and `UserStore` with the identity operations; `StoredUser.PasswordHash` handles NULL.
   - `CheckPassword` rejects users without a password hash with the same unauthorized error and timing as a wrong password (compare against the dummy hash).
   - `Service.SignInExternal(ctx, ExternalIdentity{Issuer, Subject, Email, EmailVerified, PreferredUsername, Name}) (Token, error)`: find the user by (issuer, subject); otherwise, if auto-create is on, create one with the default role and link the identity. Username: derived from `preferred_username`, else the email local part, else `user`, normalized to the existing `usernamePattern` rules, with a numeric suffix on collision.
   - **Never link to an existing local account by email or username match**: that allows account takeover through a provider that lets users pick their email. Linking existing accounts is out of scope (see follow-ups).
   - Inactive users cannot sign in through OIDC either.

4. **OIDC client** (new package, e.g. `internal/pkg/auth/oidc` or `internal/oidc`): discovery through `oidc.NewProvider` at startup (fail startup with a clear error if the issuer is unreachable or misconfigured), authorization code flow with **PKCE (S256)**, a random `state` and a random `nonce`. Verify the ID token with `provider.Verifier` (issuer, audience = client id, expiry) and check the nonce. Read `email`, `email_verified`, `preferred_username`, `name` from the claims. When `OIDC_ALLOWED_EMAIL_DOMAINS` is set, require `email_verified == true` and a matching domain (compare the part after the last `@`, lowercase).

5. **HTTP flow** (browser, `/app` pages), registered only when OIDC is enabled:
   - `GET /app/oidc/login?next=<local path>`: create state, nonce and PKCE verifier; store them with `next` (through `safeNext`) in a short-lived (10 min) HttpOnly, `SameSite=Lax`, `Secure` on HTTPS cookie scoped to `/app/oidc`, integrity-protected (HMAC with the JWT secret, or encrypted); redirect to the provider.
   - `GET /app/oidc/callback`: reject a missing/mismatched `state` or an expired cookie; handle the `error` query parameter from the provider with a friendly message page (`renderAppMessage`); exchange the code with the verifier; verify the token; call `SignInExternal`; set the `gogoform_session` cookie exactly like `SignIn` does; delete the flow cookie; redirect to `next`.
   - Never log tokens, codes or the client secret. Log the issuer, subject and user id on success, and the reason on failure.

6. **UI**: on `templates/signin.html`, when OIDC is enabled, show a "Sign in with <OIDC_PROVIDER_NAME>" button linking to `/app/oidc/login?next=...`. Hide the password form and the sign-up tab when password login is disabled. The account page may show that the user is linked to an external identity.

7. **Swagger/docs**: document the new routes and env vars (swag annotations, `.env.example`, `config.toml`). Add a short section to the docs explaining how to register the app at a provider (redirect URI, scopes), with a Keycloak and a Google example.

8. **Tests** (no real provider in CI):
   - Config: enabled/disabled detection, partial config errors, bool/list env parsing, secret not leaked.
   - Domain: `SignInExternal` creates, finds, suffixes colliding usernames, refuses inactive users and unknown identities when auto-create is off, never links by email; `CheckPassword` for a user without password.
   - HTTP: a fake OIDC provider built with `httptest.Server` (discovery document, JWKS with a test RSA key, token endpoint) to test the whole login → callback flow, including bad state, bad nonce, wrong audience, expired token, unverified email with a domain allowlist, and the provider `error` parameter.
   - With OIDC unset, every existing test passes unchanged and the new routes are not registered.

9. **Checks**: `go vet ./...`, `go test ./...`, `go build ./...` in `workdir/`, `make sqlc` leaves no diff, `make swagger` updated.

## Out of scope (follow-up tasks)

- Linking an OIDC identity to an existing, signed-in local account from the account page.
- Mapping provider groups/roles claims to app roles.
- Several providers at once.
- API clients (SPA/mobile/CLI) exchanging a provider token for an app JWT.
- RP-initiated logout at the provider.
