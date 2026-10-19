# CLAUDE.md

Go web app (module `app`) in `workdir/`. Postgres + sqlc + embedded HTML templates + Tailwind (CDN).

## Stack
- Go 1.22.4, pgx v5, sqlc, swaggo, Stripe, bcrypt/JWT.
- Templates: `cmd/api/handlers/templates/*.html` + `layout.html`, embedded via `//go:embed`.
- DB: dbmate migrations in `db/migrations`, queries in `db/queries`, generated code in `internal/db`.

## Layout (under workdir/)
- `cmd/api` — entrypoint (`main.go`) + HTTP handlers, one file per area.
- `cmd/api/handlers` — handlers: forms, submissions, files, tenants, auth, billing, app pages, public pages.
- `cmd/api/handlers/templates` — HTML templates + static assets.
- `internal/pkg/<domain>` — domain packages: auth, billing, files, forms, mailer, plans, submissions, tenants, webhooks.
  Each has `*_domain.go`, `*_service.go`, `*_ports.go`, `*_store.go` and tests.
- `internal/db` — sqlc generated code (do not hand-edit).
- `internal/errors` — `apperrors` typed errors.
- `internal/config`, `database`, `handler`, `logger` — infra.
- `db/migrations`, `db/queries`, `docs` (swagger).

## Conventions
- Errors: always `apperrors` (Code + message + optional cause). Propagate; handlers call `apperrors.WriteHTTP`.
- Tests: fakes live in `cmd/api/handlers/fakes_test.go`; domain fakes beside package tests. Run `make test`.
- CSP/nonce: `/app` pages use per-request nonce in `appContentSecurityPolicy`; inline scripts carry `nonce="{{.Nonce}}"`. Tailwind injects inline styles (`style-src 'unsafe-inline'`).
- Templates embed partials; parse with `parseAppPage`.
- Keep handlers thin; logic belongs in `internal/pkg`.

## Make targets (run in workdir/)
- `make test` — unit/integration tests (DATABASE_URL unset).
- `make sqlc` — regenerate `internal/db` from `db/queries`.
- `make swagger` — regenerate `docs/`.
- `make build` / `make run` — build/run API.
- `make db-up` / `migrate-up` — Postgres + migrations.

## Rules for edits
- Run `make test` and `make sqlc` after DB query changes.
- Never edit `internal/db` by hand; change queries then `make sqlc`.
- Match existing naming/error style; codebase conventions beat generic best practice.