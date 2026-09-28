# Signing in with an OpenID Connect provider

gogoform can sign people in to the `/app` pages through an external OpenID
Connect (OIDC) provider. It works with any standards-compliant provider and is
configured only through `config.toml` (`[oidc]`) or environment variables.
It is on when `AUTH_METHODS` includes `oidc`, which is the default once the
provider settings below are complete. `AUTH_METHODS=oidc` turns off the
password methods for SSO only. See "Choosing how users authenticate" in the
README.

To try it locally with no setup, run `make dev-sqlite AUTH=keycloak`. It
starts a Keycloak with a ready-made realm from `workdir/keycloak/` (users
`alice`/`alice` and `bob`/`bob`) and points the app at it.

## How it works

1. The sign-in page shows **Sign in with `<OIDC_PROVIDER_NAME>`**, which opens
   `GET /app/oidc/login`.
2. The app starts an authorization code flow with PKCE (S256), a random
   `state` and a random `nonce`. It keeps them in a signed, HttpOnly cookie
   scoped to `/app/oidc` for 10 minutes. Then it redirects to the provider.
3. The provider redirects back to `GET /app/oidc/callback`. The app checks the
   `state`, exchanges the code and verifies the ID token: signature, issuer,
   audience (the client id), expiry and nonce.
4. The person is identified by the token's issuer and subject (`iss`, `sub`).
   The linked local user signs in. An identity signing in for the first time
   gets a new user with `OIDC_DEFAULT_ROLE`. This only happens when
   `OIDC_AUTO_CREATE_USERS` is on, which is the default. The username comes
   from `preferred_username`, else the email's local part, else `user`. A
   number is appended when the name is taken.
5. The app issues its **own** token in the usual `gogoform_session` cookie, as
   a password sign-in does. Provider tokens are never stored, and the API
   never accepts them.

An identity is **never** linked to an existing account because the email or
username matches. Some providers let people pick their email, so matching on
it would let them take over accounts. Users created this way have no
password. Disabled users cannot sign in through the provider either.

## Settings

| Environment variable | TOML (`[oidc]`) | Default | Meaning |
|---|---|---|---|
| `OIDC_ISSUER_URL` | `issuer_url` | — | Issuer URL; discovery is fetched from `<issuer>/.well-known/openid-configuration` at startup, which fails if it is unreachable |
| `OIDC_CLIENT_ID` | `client_id` | — | Client id registered at the provider |
| `OIDC_CLIENT_SECRET` | `client_secret` | empty | Client secret; leave empty for a public client (PKCE only) |
| `OIDC_REDIRECT_URL` | `redirect_url` | — | `https://<your host>/app/oidc/callback`, registered at the provider |
| `OIDC_PROVIDER_NAME` | `provider_name` | `SSO` | Label of the sign-in button |
| `OIDC_SCOPES` | `scopes` | `openid,email,profile` | Scopes requested; `openid` is always included |
| `OIDC_ALLOWED_EMAIL_DOMAINS` | `allowed_email_domains` | empty | When set, only people with a **verified** email in one of these domains may sign in |
| `OIDC_AUTO_CREATE_USERS` | `auto_create_users` | `true` | Create a user on first sign-in; when false, only identities already linked may sign in |
| `OIDC_DEFAULT_ROLE` | `default_role` | `BASIC` | Role of the users created through OIDC |
| `AUTH_METHODS` | `[auth] methods` | `basic,jwt` (+ `oidc` when configured) | Methods that are on; `oidc` alone is SSO only (the `AUTH_ADMIN_*` bootstrap admin is still created) |

Issuer, client id and redirect URL must be set together; setting only some
of them is a startup error, and so is `oidc` in `AUTH_METHODS` without them. Lists are comma-separated in
environment variables.

The flow cookie is signed with `AUTH_JWT_SECRET`. Set that secret when you run
several replicas, so the callback can reach any of them.

## Registering the app at a provider

Every provider needs the same things:

- **Application type:** web application, with the authorization code flow.
  Use a confidential client with a secret, or a public client with PKCE.
- **Redirect URI:** exactly the value of `OIDC_REDIRECT_URL`, e.g.
  `http://localhost:8080/app/oidc/callback` locally or
  `https://forms.example.com/app/oidc/callback` in production.
- **Scopes:** `openid`, plus `email` and `profile` so accounts get a
  meaningful username and the domain allowlist can work.

### Keycloak

1. In your realm, open **Clients → Create client**. Choose client type
   *OpenID Connect* and client ID `gogoform`.
2. Turn **Client authentication** on for a confidential client, or leave it
   off for a public one. Keep **Standard flow** enabled.
3. Set **Valid redirect URIs** to `http://localhost:8080/app/oidc/callback`
   and **Web origins** to `http://localhost:8080`.
4. For a confidential client, copy the secret from the **Credentials** tab.

```sh
OIDC_ISSUER_URL=http://localhost:8081/realms/<realm>
OIDC_CLIENT_ID=gogoform
OIDC_CLIENT_SECRET=<secret from the Credentials tab>
OIDC_REDIRECT_URL=http://localhost:8080/app/oidc/callback
OIDC_PROVIDER_NAME=Keycloak
```

### Google

1. In the Google Cloud console, open **APIs & Services → Credentials →
   Create credentials → OAuth client ID** (configure the consent screen first
   if asked). Choose application type *Web application*.
2. Add `https://forms.example.com/app/oidc/callback` under **Authorized
   redirect URIs**. `http://localhost:8080/...` is accepted for local testing.
3. Copy the client ID and secret.

```sh
OIDC_ISSUER_URL=https://accounts.google.com
OIDC_CLIENT_ID=<id>.apps.googleusercontent.com
OIDC_CLIENT_SECRET=<secret>
OIDC_REDIRECT_URL=https://forms.example.com/app/oidc/callback
OIDC_PROVIDER_NAME=Google
# Optional: only your Google Workspace domain
OIDC_ALLOWED_EMAIL_DOMAINS=example.com
```

## Not supported yet

- Linking an identity to an existing, signed-in local account.
- Mapping provider groups or roles to app roles.
- Several providers at once.
- API clients (SPA, mobile, CLI) exchanging a provider token for an app
  token.
- Logging out at the provider (RP-initiated logout).
