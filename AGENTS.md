# AGENTS.md

Single source of agent instructions for Claude Code (via `CLAUDE.md` → `@AGENTS.md`) and OpenCode
(reads this file natively). Subdirectories have their own `AGENTS.md`/`CLAUDE.md`, loaded only when
you work there — keep this file short and push area-specific detail down.

Go web app (module `app`) in `workdir/`: multi-tenant form builder + submissions API with an `/app`
web UI, public form pages, file uploads, webhooks, Stripe billing, plan limits, transactional email.

## Stack
Go 1.22.4, net/http, pgx v5, sqlc, dbmate, swaggo, Stripe, bcrypt/JWT, `html/template` (embedded via
`//go:embed`), Tailwind via CDN. Postgres via docker compose.

## Code map (paths under `workdir/`)
| Path | What lives there |
|---|---|
| `cmd/api/main.go` | wiring: config → db → services → `handlers.Routes` |
| `cmd/api/handlers/` | HTTP layer, one file per area (+ `_test.go`). See its AGENTS.md |
| `cmd/api/handlers/templates/` | `layout.html` + page templates, `static/form-render.js` |
| `internal/pkg/<domain>/` | auth, billing, files, forms, mailer, plans, submissions, tenants, webhooks. See its AGENTS.md |
| `internal/errors/` | `apperrors`: `New(code,msg)`, `Wrap(err,code,msg)`, `As`, `WriteHTTP` |
| `internal/database/` | pool, tx helper, pg error mapping (`pg_errors.go`) |
| `internal/config/` | `config.toml` + env (`.env`, `.env.example`); `[security]` = rate limits, trusted proxies, spam checks. Non-development envs require `AUTH_JWT_SECRET` |
| `internal/db/` | **sqlc output — never edit**. Read `querier.go` for the method list |
| `db/queries/*.sql` | sqlc queries (source of `internal/db`) |
| `db/migrations/` | dbmate migrations, `YYYYMMDDHHMMSS_name.sql` |
| `docs/` | **generated swagger (~240 KB) — never read; run `make swagger`** |

## Commands (repo root works too: root Makefile forwards to `workdir/`)
- `make check` — `go build ./... && go vet ./...`; cheapest check after edits.
- `make test` — quiet, prints failures only. Narrow it: `make test PKGS=./internal/pkg/forms/... RUN=TestCreate`; `V=1` for verbose.
- `make sqlc` after editing `db/queries`; `make swagger` after changing handler annotations.
- `make migrate-new NAME=x`, `make db-up`, `make migrate-up` need Docker/Postgres; unit tests don't.

## Conventions
- Errors: always `apperrors` (Code + safe message + optional cause). Domain returns them; handlers call `apperrors.WriteHTTP`. Codes: invalid_argument, unauthorized, forbidden, not_found, conflict, internal, plan_limit_reached, plan_feature_unavailable, rate_limited.
- Layering: handlers thin → `internal/pkg/<domain>` Service (behind a `Repository` port) → `db.Querier`.
- Tests: handler fakes in `cmd/api/handlers/fakes_test.go`; domain fakes beside each package's tests. No DB needed (`DATABASE_URL` unset).
- Comments: doc comment on every exported identifier, explaining why/what, matching existing tone.
- Codebase conventions beat generic best practice; copy the nearest similar file.

## Token discipline (read this)
- Use this map; don't `ls -R`/explore to rediscover it. Grep for a symbol, then read the hit with an offset/limit.
- Big files — never read whole: `templates/builder.html` (53 KB; grep ids like `id="canvas"`), `templates/app.html`, `layout.html`, `fakes_test.go` (700 lines; grep `func (f \*fakeX)`), `internal/db/*.sql.go` (read `querier.go` instead).
- Verify narrowly: `make check`, then `make test PKGS=<touched pkgs>`; run the full `make test` once at the end.
- Don't paste long command output back; summarize it. Don't re-read files you just edited.
- Task history lives in `__ai_work/finished/` (excluded from search); open a file there only when a task refers to it.

## AI workflow (`__ai_work/`)
`make new-task NAME=x` creates `__ai_work/tasks/NNNNNN_x.md`; `make run-tasks` (runner.py) plans,
implements, reviews (`/code-review`), fixes (`/code-review-fixer`) and commits (`/commits`).
Skills live in `.claude/skills/*/SKILL.md`; OpenCode commands in `.opencode/command/` include them.
