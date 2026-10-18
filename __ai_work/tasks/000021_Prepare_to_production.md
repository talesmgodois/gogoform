# 000021 Prepare to production

Part 1 of the MVP launch (Free plan + Pro plan at 9 USD/month): accounts and data ownership.
Today forms/files belong to `tenants` (API key) while `users` (JWT, task 000017) own nothing,
and `/app` is an operator panel (APP_USERNAME/APP_PASSWORD) that shows every tenant's data.

## DB (new dbmate migration in workdir/db/migrations + sqlc queries)
- Add `users.email` (unique, case-insensitive, required for new users; backfill existing ones as nullable then enforce for new signups) and `users.email_verified_at`.
- Add `tenants.owner_user_id` (FK users). Each user gets one workspace (tenant) created automatically on signup.
- Migrations are the source of truth; do not update `__ai_work/_inputs/db_schema.dbml`.

## Backend
- Signup (`POST /auth/signup` and `/app/signin` page) takes email + password, creates the user and its tenant in one transaction.
- Signin accepts email (keep username working for the bootstrap admin).
- API: forms, submissions, webhooks and files endpoints accept either the tenant API key (unchanged) or a signed-in user (JWT/session cookie) resolved to the user's own tenant. A user can never read or change another tenant's data.
- `GET /tenants/me` also works for signed-in users and returns the API key so users can integrate.
- Add `POST /tenants/me/api-key/rotate`.
- Disable the unauthenticated `POST /tenants` and the `/samples/*` routes outside dev (config flag, default off in production).
- `/app` stops using the global APP_USERNAME/APP_PASSWORD Basic auth: pages use the user session and show only the user's tenant. ADMIN keeps a global view (e.g. `/app/admin`).

## Tests
- Two users cannot see each other's forms, submissions or files (API and /app).
- Signup creates user + tenant atomically; duplicated email returns 409.
