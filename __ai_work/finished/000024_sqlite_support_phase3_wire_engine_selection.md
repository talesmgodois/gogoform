# 000024 sqlite_support_phase3_wire_engine_selection

Phase 3 of the multi-database plan in `docs/architecture/multi-database.md` (repo root; `../docs/...` from `workdir/`). Read sections "Selecting the engine with env vars", "Components" (3) and "Tooling and ops" first. It depends on tasks 000021 and 000023: `database.Open`, `openSQLite`, the SQLite adapter, `db/sqlite/` and the contract suite must already exist. If they don't, stop and say so.

Goal: users switch between PostgreSQL and SQLite by changing only `DATABASE_URL`. PostgreSQL stays the default.

1. **Config** (`internal/config`): `DatabaseConfig.Driver()` returns `DriverSQLite` for the `sqlite:` scheme (dbmate's format: `sqlite:./data/app.db`, `sqlite:/abs/path.db`, `sqlite:///abs/path.db`, and `sqlite::memory:` for tests). Add `DatabaseConfig.SQLitePath()` that returns the file path from those forms. `validateDatabaseURI` checks each engine separately: PostgreSQL keeps requiring a host; SQLite requires a non-empty path; any other scheme is rejected with a message listing the supported ones. Error messages never include the URI. Add the optional SQLite tuning fields `DATABASE_SQLITE_BUSY_TIMEOUT_MS` (default 5000, must be >= 0) and `DATABASE_SQLITE_JOURNAL_MODE` (default `WAL`, one of `WAL`, `DELETE`, `TRUNCATE`), with TOML keys under `[database]`. Table-driven tests for every form above, plus invalid ones.

2. **Open**: `database.Open` picks `openSQLite` or the PostgreSQL path based on `cfg.Driver()`, passing the tuning options. Before opening a file database, create its parent directory if missing (0o750) and return a clear error if it isn't writable. `main.go` logs `driver` together with `LogAttrs()` when the database is connected.

3. **Makefile** (`workdir/Makefile`): derive `DB_ENGINE := $(if $(filter sqlite:%,$(DATABASE_URL)),sqlite,postgres)` and `MIGRATIONS_DIR ?= db/$(DB_ENGINE)/migrations`. `make dev` only runs `db-up` (docker) when `DB_ENGINE` is `postgres`. `db-up`, `db-logs`, `adminer`, `generate-schema` and `migrate-init` print a clear message and exit non-zero when used with SQLite. `migrate-up/down/status/new` work with both (dbmate supports `sqlite:` URLs). Add `DB_ENGINE` to `make help` output where useful. Update `scripts/setup_migrations.sh` the same way.

4. **Docs and examples**:
   - `.env.example` and `config.toml`: show both URLs, SQLite commented out, with a one-line explanation of each tuning option.
   - `Dockerfile`: create `/app/data` owned by the `nonroot` user in the runtime image (distroless has no shell, so prepare the directory in the build stage and `COPY --chown=nonroot:nonroot` it), and document `DATABASE_URL=sqlite:/app/data/gogoform.db` with a volume mounted there.
   - Add a "Choosing a database" section to the docs (the doc in `docs/architecture/multi-database.md` stays the design record; put user-facing instructions in a user doc or README): how to switch, how to run migrations for each engine, and SQLite's known limits (single writer, uploads stored in the database file, ASCII-only case-insensitive search, backups with `.backup`/`VACUUM INTO`).

5. **End-to-end check**: with `DATABASE_URL=sqlite:<scratch dir>/app.db`, run `make migrate-up` then start the API, and exercise with curl: create a tenant, create a form, submit to it, list submissions, upload and download a file, sign up and sign in. Then do the same against PostgreSQL if Docker is available. Report what you ran and the results.

6. Checks: `go vet ./...`, `go test ./...`, `CGO_ENABLED=0 go build ./...` in `workdir/`; the Docker image still builds.

<!-- RUNNER:PLAN -->
## Plan

1. [x] Extend `internal/config`: implement the `sqlite` case of `Driver()`, add `SQLitePath()`, split `validateDatabaseURI` per engine (postgres needs host, sqlite needs a non-empty path, unknown scheme lists supported ones, never echoes the URI), add `DATABASE_SQLITE_BUSY_TIMEOUT_MS` (default 5000, >=0) and `DATABASE_SQLITE_JOURNAL_MODE` (default WAL, enum WAL/DELETE/TRUNCATE) with TOML+env tags, and add table-driven tests for every URL form plus invalid ones.
2. [x] Update `internal/database.Open` to dispatch to `openSQLite` (passing `cfg.SQLitePath()` and tuning options) when `cfg.Driver() == config.DriverSQLite`, create the parent directory (0o750) before opening a file database with a clear error if unwritable, add tests for that, and make `cmd/api/main.go` log `driver` alongside `LogAttrs()`.
3. [x] Update `workdir/Makefile` to derive `DB_ENGINE`/`MIGRATIONS_DIR`, make `dev` skip `db-up` for sqlite, make `db-up`/`db-logs`/`adminer`/`generate-schema`/`migrate-init` fail clearly under sqlite, keep `migrate-up/down/status/new` and `sqlc` working for both, surface `DB_ENGINE` in `help`, and apply the equivalent postgres-only guards to `scripts/setup_migrations.sh`.
4. [x] Update `.env.example` and `config.toml` to show both URL forms (sqlite commented out) with one-line explanations of the tuning options, update the `Dockerfile` to create and `COPY --chown=nonroot:nonroot` `/app/data` plus document the sqlite `DATABASE_URL`/volume, and add a "Choosing a database" section to a user-facing doc (e.g. `README.md`) covering switching engines, per-engine migrations, and SQLite's known limits.
5. [x] Run an end-to-end check against a scratch SQLite `DATABASE_URL` (migrate-up, start API, curl through tenant/form/submission/file/auth flows) and repeat against PostgreSQL if Docker is available, then report what was run and the results.
6. [x] Run `go vet ./...`, `go test ./...`, `CGO_ENABLED=0 go build ./...` in `workdir/`, and confirm the Docker image still builds.
