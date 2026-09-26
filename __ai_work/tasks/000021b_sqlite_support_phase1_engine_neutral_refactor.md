# 000021 sqlite_support_phase1_engine_neutral_refactor

Phase 1 of the multi-database plan in `docs/architecture/multi-database.md` (repo root; `../docs/...` from `workdir/`). Read that document first: sections "Components" and "Rollout" describe the target design.

This phase is a refactor with NO behavior change: the app must still run only on PostgreSQL, and every existing test must keep passing. It prepares the code so SQLite can be plugged in later. Do not add SQLite, modernc or any new database driver yet.

1. Move the PostgreSQL schema and queries into an engine folder with `git mv`: `workdir/db/migrations` -> `workdir/db/postgres/migrations` and `workdir/db/queries` -> `workdir/db/postgres/queries`. Keep the dbmate version numbers. Update every reference: `sqlc.yaml` (schema/queries paths), `workdir/Makefile` (`MIGRATIONS_DIR`), `scripts/setup_migrations.sh`, `.dockerignore`/Dockerfile if relevant, and comments. `make sqlc` must produce no diff in `internal/db`.

2. Add an engine to the config: in `internal/config`, add a `Driver` type with `DriverPostgres`, plus `DatabaseConfig.Driver()` worked out from the `DATABASE_URL` scheme (`postgres`/`postgresql`). Validation stays the same (only PostgreSQL accepted for now), and error messages must never include the URI. Add tests.

3. Replace `database.Connect` with an engine-neutral `database.Open(ctx, cfg config.DatabaseConfig) (*DB, error)`. `DB` exposes `Querier db.Querier`, `Driver`, `Close()` and `LogAttrs() []any` (host/port/database, never credentials). Move the pgx pool code into `internal/database/postgres.go`. Update `cmd/api/main.go` to use it. Keep the existing test that invalid URIs don't leak credentials.

4. Make the error helpers engine-neutral: rename `internal/database/pg_errors.go` to `errors.go` (and its test), and add `database.ErrNoRows` and `database.IsNoRows(err)` (true for `pgx.ErrNoRows` and `database.ErrNoRows`). Replace every `errors.Is(err, pgx.ErrNoRows)` in `internal/pkg` (forms, files, tenants, auth store) with `database.IsNoRows(err)`, so `internal/pkg` no longer imports pgx.

5. Generate file UUIDs in the application: `files.Service` creates a random v4 UUID with `crypto/rand` (canonical lowercase format, matching the existing `uuidPattern`) and passes it to `CreateFile`. Change the `CreateFile` query to take the id as a parameter (keep the column default in the schema), regenerate with sqlc, and add tests.

6. Store form content as canonical JSON: in `forms.Service`, before `CreateForm` and `UpdateForm`, re-marshal `form_content` into a compact canonical form (e.g. decode into `any` with `json.Decoder.UseNumber()` so numbers keep their precision, then `json.Marshal`, which sorts object keys), so later engines compare content byte for byte the way `jsonb` does. Add tests showing that the same content with different key order or whitespace does not trigger the "form has submissions" lock.

7. Checks: `go vet ./...`, `go test ./...` and `go build ./...` pass in `workdir/`; `make sqlc` leaves `internal/db` unchanged apart from the `CreateFile` change in step 5; `grep -r "jackc/pgx" internal/pkg` finds nothing outside tests.
