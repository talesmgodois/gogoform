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
