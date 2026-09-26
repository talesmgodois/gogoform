# 000023 sqlite_support_phase2_schema_queries_adapter

Phase 2 of the multi-database plan in `docs/architecture/multi-database.md` (repo root; `../docs/...` from `workdir/`). Read sections "Components" (2, 4, 5, 6, 7) and "Testing" first. It depends on task 000021 (phase 1): `db/postgres/`, `database.Open`, `database.IsNoRows`, app-generated file UUIDs and canonical form JSON must already exist. If they don't, stop and say so.

Goal: SQLite schema, queries, generated code and a `db.Querier` adapter exist and are tested, but the app **cannot select SQLite yet** (config still accepts only PostgreSQL; that's phase 3).

1. Add the pure-Go driver `modernc.org/sqlite` to `workdir/go.mod` (it must build with `CGO_ENABLED=0`; never use `mattn/go-sqlite3`). Pick a version compatible with the Go version in go.mod.

2. Create `workdir/db/sqlite/migrations/` with **one squashed dbmate migration** (`-- migrate:up` / `-- migrate:down`) reproducing the current PostgreSQL schema (every table, column, default, UNIQUE, CHECK, FK and index from all `db/postgres/migrations`). Follow the mapping table in the doc: `INTEGER PRIMARY KEY`; files.id `TEXT PRIMARY KEY` (no default, the app supplies it); `jsonb` → `TEXT NOT NULL CHECK (json_valid(col))`; `bytea` → `BLOB`; `timestamp` → `DATETIME DEFAULT CURRENT_TIMESTAMP`; booleans `BOOLEAN` with `1`/`0` defaults; the `user_role` enum → `TEXT` with a `CHECK (role IN (...))`; `varchar(n)` → `TEXT` with `CHECK (length(col) <= n)`; the `forms_public_accepts_anonymous` check and the `username = lower(username)` check. Declare constraints inline in `CREATE TABLE`. Use a version number later than every PostgreSQL migration, and add a header comment saying it mirrors the PostgreSQL schema up to that version.

3. Create `workdir/db/sqlite/queries/*.sql` with **the same query names, parameters and result columns** as `db/postgres/queries`, in SQLite dialect: `?`/`sqlc.arg`/`sqlc.narg` instead of `$n`; `CAST(sqlc.narg(x) AS BOOLEAN) IS NULL` instead of `::boolean`; `LIKE` instead of `ILIKE`; `CURRENT_TIMESTAMP` instead of `now()`; `json(f.form_content) = json(?)` for the draft lock in `UpdateForm`. Keep the comments of the PostgreSQL queries and note the ASCII-only case-insensitive search.

4. Add the second `sql` entry to `workdir/sqlc.yaml` (engine `sqlite`, package `sqlitegen`, out `internal/db/sqlitegen`, same emit options as the PostgreSQL entry, `json.RawMessage` overrides for `forms.form_content` and `form_submissions.payload`, `time.Time`/`*time.Time` for DATETIME columns). Run `make sqlc`: `internal/db` must be unchanged and `internal/db/sqlitegen` generated. Check that nullable params and results come out as the types the adapter needs (`*bool` for the narg filters); adjust casts until they do.

5. Error helpers in `internal/database/errors.go`: `IsUniqueViolation`, `IsForeignKeyViolation` and `IsCheckViolation` also match modernc `*sqlite.Error` extended codes: UNIQUE 2067 and PRIMARYKEY 1555 → unique, FOREIGNKEY 787 → FK, CHECK 275 → check. Extend the tests with these cases (wrapped too).

6. `internal/database/sqlite.go`: an unexported `openSQLite(ctx, path string, opts) (*DB, error)` that builds the modernc DSN with `_pragma=foreign_keys(1)`, `_pragma=journal_mode(WAL)` (skipped for `:memory:`), `_pragma=busy_timeout(<ms>)`, `_txlock=immediate` and `_time_format=sqlite`, sets `SetMaxOpenConns` (1 for `:memory:`, a small number like 4 otherwise), pings, and fills `DB{Querier: <adapter>, Driver: DriverSQLite, ...}` with `LogAttrs` showing the file path. Add `DriverSQLite` to the config `Driver` type, but do not accept it in validation yet.

7. `internal/database/sqlite_querier.go`: `sqliteQuerier` implementing **every** `db.Querier` method (`var _ db.Querier = (*sqliteQuerier)(nil)`) by calling `sqlitegen.Queries` and converting types: `int32`↔`int64` (return an error instead of wrapping when an id is out of `int32` range), `string`↔`db.UserRole`, nullable fields, rows through shared helpers (`toForm`, `toUser`, ...). Map `sql.ErrNoRows` to `database.ErrNoRows`; pass every other error through unchanged so the violation helpers still see `*sqlite.Error`.

8. **Querier contract suite** (`internal/database/contract_test.go`): a table-driven suite taking a `db.Querier` factory, covering every method: CRUD round trips, tenant scoping, pagination order, the filters of `ListFormsByTenant`/`CountFormsByTenant`, `UpdateForm` refusing to change content or return to draft once a submission exists (and accepting the same content re-sent), `CreateUserIfNotExists` idempotency, `IsNoRows`, and unique / FK / check violations (duplicate slug, form of a missing tenant, public form not accepting anonymous, uppercase username). Run it on SQLite `:memory:` with the migration applied (read the `-- migrate:up` block of the files in `db/sqlite/migrations`), always, in `go test ./...`. Also run it against PostgreSQL when `TEST_DATABASE_URL` is set (skip otherwise), applying `db/postgres/migrations` to a fresh schema. Make it pass on both.

9. Checks: `go vet ./...`, `go test ./...`, `CGO_ENABLED=0 go build ./...` in `workdir/`, and `make sqlc` leaves no diff.

<!-- RUNNER:PLAN -->
## Plan

1. [x] Add `modernc.org/sqlite` to workdir/go.mod (Go 1.22.4-compatible version) and confirm `CGO_ENABLED=0 go build ./...` still succeeds.
2. [x] Write the squashed SQLite migration in `db/sqlite/migrations/` reproducing the full PostgreSQL schema per the mapping table (tables, defaults, UNIQUE/CHECK/FK/index, including `forms_public_accepts_anonymous` and `username = lower(username)`).
3. [x] Apply the migration to a fresh SQLite database and validate every table/constraint enforces as expected.
4. [x] Port `db/postgres/queries/*.sql` to `db/sqlite/queries/*.sql` in SQLite dialect (placeholders, `LIKE`, `CURRENT_TIMESTAMP`, `json(...)` draft-lock comparison), same names/params/columns.
5. [x] Add the `sqlite` engine entry to `sqlc.yaml` (package `sqlitegen`, out `internal/db/sqlitegen`, JSON/time overrides).
6. [x] Run `make sqlc`; confirm `internal/db` is unchanged and `internal/db/sqlitegen` generates with correct nullable types (adjust casts in step 4 if needed).
7. [x] Extend `internal/database/errors.go` to also match `*sqlite.Error` extended codes for unique/FK/check violations, with wrapped and unwrapped test cases.
8. [x] Add `internal/database/sqlite.go` with `openSQLite` (DSN/pragmas, pool sizing, ping, `DB` construction) and add `DriverSQLite` to `config.Driver` without touching validation.
9. [x] Implement the first half of `sqlite_querier.go` (tenants/users/files methods) with shared row-conversion helpers and error mapping.
10. [x] Implement the remaining `sqlite_querier.go` methods (forms/submissions/webhooks) and confirm `var _ db.Querier = (*sqliteQuerier)(nil)` compiles.
11. [x] Build `internal/database/contract_test.go` core CRUD/tenant-scoping/pagination coverage running against SQLite `:memory:`.
12. [x] Extend the contract suite with forms/submissions filters, the `UpdateForm` draft-lock behavior, `CreateUserIfNotExists` idempotency, `IsNoRows`, and unique/FK/check violation scenarios.
13. [x] Wire the contract suite to also run against PostgreSQL when `TEST_DATABASE_URL` is set, and reconcile any cross-engine behavior gaps.
14. [x] Run final checks in workdir/: `go vet ./...`, `go test ./...`, `CGO_ENABLED=0 go build ./...`, and `make sqlc` with no diff.
