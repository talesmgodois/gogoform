# Multi-database support: SQLite next to PostgreSQL

Status: proposed · Scope: `workdir/` (the Go API)

## Goal

Let people who self-host gogoform pick their database with one environment
variable. PostgreSQL stays the default and the reference engine. SQLite is the
first new engine, so the app can run as a single binary with no database
server. The design should let MySQL be added the same way later.

```sh
DATABASE_URL=postgres://postgres:postgres@localhost:5432/app_db?sslmode=disable  # PostgreSQL
DATABASE_URL=sqlite:./data/gogoform.db                                           # SQLite
```

## How it works today

| Layer | Today | Tied to PostgreSQL? |
|---|---|---|
| Config | `DATABASE_URL`; `validateDatabaseURI` accepts only `postgres`/`postgresql` with a host | yes |
| Connection | `internal/database.Connect` returns a `*pgxpool.Pool` | yes |
| Schema | dbmate migrations in `db/migrations` (IDENTITY, `jsonb`, `bytea`, `uuid` + `gen_random_uuid()`, `CREATE TYPE ... AS ENUM`, `ALTER TABLE ... ADD CONSTRAINT`) | yes |
| Queries | `db/queries/*.sql` use `$n`, `::boolean` casts, `ILIKE`, `now()`, jsonb `=` | yes |
| Generated code | sqlc `engine: postgresql`, `sql_package: pgx/v5` → `internal/db` (`Querier`, models, params) | yes |
| Domain | `internal/pkg/*` services take `db.Querier` and map to domain types | only through `db.Querier` types, plus `pgx.ErrNoRows` and `database.Is*Violation` (pgconn SQLSTATEs) |
| Handlers | use the domain `Repository` interfaces only | no |

The services already use `db.Querier` as their only way into the database.
That interface is where the engine can be swapped.

## Decision

**`db.Querier` stays the single interface every service uses. Each extra
engine gets its own sqlc-generated package plus a small hand-written adapter
that implements `db.Querier` on top of it.**

```
                        handlers  →  domain Repository interfaces
                                          │
                              internal/pkg/* services
                                          │  db.Querier  (canonical, generated from PostgreSQL)
                    ┌─────────────────────┴─────────────────────┐
         internal/db  (sqlc, pgx/v5)              internal/database/sqlite  (adapter, hand-written)
                    │                                           │
              pgxpool.Pool                           internal/db/sqlitegen  (sqlc, database/sql)
                                                                │
                                                     modernc.org/sqlite (pure Go)
```

Why this approach:

- **No changes to services or handlers.** The 6 services, their tests and the
  handler fakes stay as they are. The engine choice is made once, in `main.go`.
- **The type-safe query workflow stays.** Each engine's SQL is written in its
  own dialect and checked by sqlc against its own schema. We don't need an ORM
  or dialect-generic SQL.
- **Each engine's code is confined.** An adapter method only converts types
  (`int64`↔`int32`, `sql.Null*`↔pointers, `string`↔`UserRole`) and passes the
  call through. The compiler reports any `Querier` method an adapter is missing.

A rejected alternative: making each domain `Repository` the seam, with one
store implementation per engine. The services currently implement
`Repository` *and* hold business rules (`resolveAccess`, the availability
window, the draft lock). Splitting those out would touch every package for no
gain over the adapter.

## Selecting the engine with env vars

The `DATABASE_URL` scheme picks the engine. We don't add a separate
`DB_DRIVER` variable because it could disagree with the URL. The URL format is
the same one dbmate uses, so the app and `make migrate-*` read the same
variable.

| Scheme | Engine | Example |
|---|---|---|
| `postgres://`, `postgresql://` | PostgreSQL (pgx) | `postgres://u:p@host:5432/db?sslmode=disable` |
| `sqlite:` | SQLite (modernc) | `sqlite:./data/gogoform.db`, `sqlite:///var/lib/gogoform/app.db`, `sqlite::memory:` (tests only) |

Changes in `internal/config`:

- Add `DatabaseConfig.Driver() Driver` (`DriverPostgres`, `DriverSQLite`),
  worked out from the scheme.
- Make `validateDatabaseURI` check each engine separately. PostgreSQL keeps
  requiring a host. SQLite requires a non-empty path. Error messages must never
  include the URI, same as today.
- Optional SQLite tuning, each with a TOML key and an env var:
  `DATABASE_SQLITE_BUSY_TIMEOUT_MS` (default 5000) and
  `DATABASE_SQLITE_JOURNAL_MODE` (default `WAL`). Everything else is fixed by
  the driver setup below.

## Components

### 1. Directory layout (per engine)

```
workdir/db/
  postgres/migrations/   ← moved from db/migrations (git mv; dbmate version numbers are kept)
  postgres/queries/      ← moved from db/queries
  sqlite/migrations/     ← new
  sqlite/queries/        ← new, same query names as postgres/queries
workdir/internal/
  db/                    ← unchanged import path: generated from postgres, owns Querier + models
  db/sqlitegen/          ← generated from sqlite
  database/
    database.go          ← Open(ctx, cfg) (*DB, error)
    postgres.go          ← current Connect, moved
    sqlite.go            ← opens modernc, applies pragmas
    sqlite_querier.go    ← adapter: implements db.Querier over sqlitegen.Queries
    errors.go            ← was pg_errors.go; knows both engines
```

### 2. sqlc

`sqlc.yaml` gets two `sql` entries. The existing PostgreSQL entry keeps
generating `internal/db` as it does today. The new entry:

```yaml
  - engine: "sqlite"
    schema: "db/sqlite/migrations"
    queries: "db/sqlite/queries"
    gen:
      go:
        package: "sqlitegen"
        out: "internal/db/sqlitegen"
        emit_json_tags: true
        json_tags_case_style: "snake"
        emit_db_tags: true
        emit_interface: true
        emit_empty_slices: true
        emit_pointers_for_null_types: true
        overrides:
          - column: "forms.form_content"
            go_type: "encoding/json.RawMessage"
          - column: "form_submissions.payload"
            go_type: "encoding/json.RawMessage"
```

`make sqlc` regenerates both. CI runs `sqlc diff` so generated code can't fall
behind the SQL.

### 3. Connection: `internal/database.Open`

```go
type DB struct {
    Querier db.Querier
    Driver  config.Driver
    close   func()
}
func Open(ctx context.Context, cfg config.DatabaseConfig) (*DB, error)
func (d *DB) Close()
func (d *DB) LogAttrs() []any // host/port/db for postgres, file path for sqlite; never credentials
```

`main.go` calls `database.Open` and passes `d.Querier` to `db.New`,
`newAuthService` and `handlers.NewServices`. Nothing else in `main.go` changes.

SQLite driver: **`modernc.org/sqlite`** (pure Go). The Dockerfile builds with
`CGO_ENABLED=0` into a distroless *static* image, which rules out
`mattn/go-sqlite3`. The DSN built from the URL always includes:

| Setting | Why |
|---|---|
| `_pragma=foreign_keys(1)` | **Off by default in SQLite.** Without it, FK violations never fire, so `IsForeignKeyViolation` (tenant/form not found, "form has submissions") stops working and orphan rows can appear. |
| `_pragma=journal_mode(WAL)` | Reads don't block the writer. |
| `_pragma=busy_timeout(5000)` | Writers wait instead of failing with `SQLITE_BUSY`. |
| `_txlock=immediate` | Avoids deadlocks when a read transaction is upgraded to a write. |
| `_time_format=sqlite` | Every `time.Time` is written in one text format, so ordering on `created_at` stays correct. |

Pool: `SetMaxOpenConns` stays small (for example 4). `:memory:` uses
`SetMaxOpenConns(1)` because every connection to `:memory:` opens its own
empty database. `Open` pings the database and fails fast, as `Connect` does
today.

### 4. Errors (`internal/database/errors.go`)

- `IsUniqueViolation`, `IsForeignKeyViolation` and `IsCheckViolation` also
  match `*sqlite.Error` extended codes: `SQLITE_CONSTRAINT_UNIQUE` (2067) and
  `SQLITE_CONSTRAINT_PRIMARYKEY` (1555) count as unique violations,
  `SQLITE_CONSTRAINT_FOREIGNKEY` (787) as FK, `SQLITE_CONSTRAINT_CHECK` (275)
  as check.
- New `database.ErrNoRows` and `IsNoRows(err)`. The adapter turns
  `sql.ErrNoRows` into it, and the 4 services that check
  `errors.Is(err, pgx.ErrNoRows)` (forms, files, tenants, auth store) switch to
  `database.IsNoRows`. After that, `internal/pkg` no longer imports pgx.

### 5. Schema: SQLite migrations

dbml2sql cannot produce SQLite output, and SQLite can't replay the PostgreSQL
`ALTER TABLE ... ADD CONSTRAINT` history. So the SQLite set **starts as one
squashed migration** that matches the current PostgreSQL schema. From then
on, both engines get new migrations in pairs.

| PostgreSQL | SQLite |
|---|---|
| `INTEGER GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY` | `INTEGER PRIMARY KEY` |
| `uuid DEFAULT gen_random_uuid()` | `TEXT PRIMARY KEY`, id supplied by the app (see below) |
| `jsonb` | `TEXT NOT NULL CHECK (json_valid(col))` |
| `bytea` | `BLOB` |
| `timestamp DEFAULT (now())` | `DATETIME DEFAULT CURRENT_TIMESTAMP` |
| `boolean DEFAULT true` | `BOOLEAN DEFAULT 1` (stored as 0/1; sqlc maps it to `bool`) |
| `CREATE TYPE user_role AS ENUM (...)` | `TEXT NOT NULL DEFAULT 'BASIC' CHECK (role IN ('ADMIN','FORM_CREATOR','BASIC'))` |
| `varchar(n)` | `TEXT CHECK (length(col) <= n)` where the app relies on the limit |
| table-level `CHECK`s, `UNIQUE`, FKs, indexes | same syntax, declared inline in `CREATE TABLE` |

**Files UUIDs.** Generate them in Go (`crypto/rand`, v4) in `files.Service`
for *both* engines, and pass the id to `CreateFile`. `files_service.go`
already rejects ids that don't match the canonical UUID pattern, so
SQLite-generated `hex(randomblob())` ids won't work. The PostgreSQL column
default can stay as a safety net.

### 6. Queries: SQLite dialect

The query names and result columns match `db/postgres/queries` one-to-one, so
every adapter method is a direct pass-through. Differences:

| PostgreSQL | SQLite | Notes |
|---|---|---|
| `$1` | `?` (or keep `sqlc.arg`/`sqlc.narg`) | |
| `sqlc.narg(x)::boolean IS NULL` | `CAST(sqlc.narg(x) AS BOOLEAN) IS NULL` | Confirm sqlc infers `*bool`. |
| `ILIKE '%' \|\| x \|\| '%'` | `LIKE '%' \|\| x \|\| '%'` | SQLite `LIKE` ignores case for **ASCII only**. Title search is case-sensitive for accented letters on SQLite. Document it. |
| `now()` | `CURRENT_TIMESTAMP` | |
| `f.form_content = $9` (jsonb) | `json(f.form_content) = json(?)` | **Behavior gap:** jsonb `=` ignores key order and whitespace; SQLite text doesn't. Fix it for both engines by storing canonical JSON (re-marshal in `forms.Service` before writing), so an unchanged form never looks edited and hits the "locked" conflict. |
| `RETURNING *`, `ON CONFLICT DO NOTHING`, `UPDATE forms f` | same | SQLite ≥ 3.35, bundled by modernc. |

### 7. The adapter (`sqlite_querier.go`)

```go
var _ db.Querier = (*sqliteQuerier)(nil)

type sqliteQuerier struct{ q *sqlitegen.Queries }

func (s *sqliteQuerier) GetFormByID(ctx context.Context, arg db.GetFormByIDParams) (db.GetFormByIDRow, error) {
    row, err := s.q.GetFormByID(ctx, sqlitegen.GetFormByIDParams{ID: int64(arg.ID), TenantID: int64(arg.TenantID)})
    if err != nil {
        return db.GetFormByIDRow{}, mapErr(err) // sql.ErrNoRows → database.ErrNoRows
    }
    return db.GetFormByIDRow{ID: int32(row.ID), /* ... */}, nil
}
```

About 30 methods. Converting rows means small helpers (`toForm`, `toUser`,
...) that are reused across queries returning the same table. Ids go from
`int64` to `int32` with a bounds check that returns an error instead of
wrapping.

`Queries.WithTx(pgx.Tx)` isn't used today. When transactions are needed, add
an engine-neutral `database.DB.InTx(ctx, func(db.Querier) error)`. Don't
expose `pgx.Tx`.

## Tooling and ops

- **Makefile.** Work out `DB_ENGINE` from `DATABASE_URL`
  (`$(if $(filter sqlite:%,$(DATABASE_URL)),sqlite,postgres)`) and set
  `MIGRATIONS_DIR ?= db/$(DB_ENGINE)/migrations`. `make dev` only runs
  `db-up` (docker) for PostgreSQL. `generate-schema`/`migrate-init` stay
  PostgreSQL-only.
- **dbmate** already supports `sqlite:` URLs, so `migrate-up/down/status/new`
  work unchanged once `MIGRATIONS_DIR` follows the engine.
- **Auto-migrate (phase 5, optional).** For a zero-setup SQLite experience,
  embed `db/<engine>/migrations` with `embed.FS`. Behind
  `DATABASE_AUTO_MIGRATE=true`, apply pending `-- migrate:up` blocks at
  startup and record them in dbmate's own `schema_migrations(version)` table,
  so the CLI and the app can be used together. It's a small in-house runner
  (the dbmate Go library's SQLite driver needs cgo).
- **Docker.** The image is unchanged (pure Go). Document
  `DATABASE_URL=sqlite:/app/data/gogoform.db` with a volume mounted at
  `/app/data` that the `nonroot` user can write to. Add a compose profile
  that runs only the API with SQLite.
- **`.env.example` / `config.toml`.** Show both URLs, with the SQLite one
  commented out.

## Testing

1. **Querier contract suite** (`internal/database/contract_test.go`): one
   table-driven suite exercising every `db.Querier` method and the error
   helpers (unique, FK, check, no rows, the draft lock in `UpdateForm`,
   `CreateUserIfNotExists` idempotency, pagination order). It runs against:
   - SQLite `:memory:` with migrations applied: **always**, in `go test ./...`
     and in CI, with no services. That gives the project its first real-SQL
     tests.
   - PostgreSQL when `TEST_DATABASE_URL` is set. Add a CI job with a
     `postgres:18` service container.
2. **Config tests**: working out the scheme, per-engine validation, and that
   error messages never include credentials.
3. **Error helper tests**: extend `pg_errors_test.go` with `*sqlite.Error`
   cases.
4. **Schema parity**: the contract suite catches most drift. Add a checklist
   item for PRs that touch migrations: "added to both `db/postgres` and
   `db/sqlite`".

## Rollout (each phase is one reviewable PR; nothing changes for users until phase 3)

1. **Engine-neutral refactor, no behavior change.** `git mv` into
   `db/postgres/`. `database.Open`/`DB`. `ErrNoRows`/`IsNoRows` replaces
   `pgx.ErrNoRows` in services. `Driver()` in config. UUIDs generated in Go
   for files. Canonical JSON for `form_content`.
2. **SQLite schema + queries + sqlc entry + adapter**, with the contract
   suite running on SQLite.
3. **Wire it up.** Accept the `sqlite:` scheme in config, build the modernc
   DSN and pragmas in `Open`, update Makefile/.env/config docs.
4. **CI.** `sqlc diff`, and the PostgreSQL contract job.
5. **Optional.** Embedded auto-migrations and the compose SQLite profile.

## Known limits of SQLite (to document for users)

- One writer at a time. Fine for self-hosting and small teams, not for
  high write concurrency. Use PostgreSQL for that.
- Uploaded files are BLOBs inside the database file, so the file grows with
  uploads. Back it up with `sqlite3 app.db ".backup ..."` or `VACUUM INTO`,
  not by copying the file while it's in use.
- Title search is case-insensitive for ASCII only.
- No data migration tool between engines in this scope.

## Adding MySQL later

Same recipe: `db/mysql/{migrations,queries}`, a sqlc `engine: mysql` entry into
`internal/db/mysqlgen`, a `mysql_querier.go` adapter, error codes 1062/1452/3819
in `errors.go`, and the `mysql://` scheme in config. MySQL has no `RETURNING`,
so its adapter will need follow-up `SELECT`s by `LastInsertId` for the
`:one` inserts and updates. That is the main extra work compared to SQLite.
