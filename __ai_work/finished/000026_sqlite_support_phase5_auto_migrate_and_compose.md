# 000026 sqlite_support_phase5_auto_migrate_and_compose

Phase 5 of the multi-database plan in `docs/architecture/multi-database.md` (repo root; `../docs/...` from `workdir/`). Read "Tooling and ops" first. It depends on tasks 000021, 000023, 000024 and 000025.

Goal: a zero-setup experience. Someone can run the single binary (or one container) with SQLite and no external tools, and the schema is created on startup.

1. **Embedded migrations**: embed `db/postgres/migrations` and `db/sqlite/migrations` with `embed.FS` (an `embed.go` next to them in a small `db` package, or wherever the embed paths allow).

2. **Migrator** (`internal/database/migrate.go`): `Migrate(ctx, d *DB) (applied []string, err error)` applies pending migrations for `d.Driver`, compatible with dbmate so the CLI and the app can be mixed:
   - uses dbmate's table `schema_migrations(version varchar(128) PRIMARY KEY)` with the same version format (the numeric prefix of the file name), creating it if missing;
   - parses the `-- migrate:up` block (up to `-- migrate:down`), honoring dbmate's `-- migrate:up transaction:false` option;
   - applies each pending migration and inserts its version in one transaction, in version order;
   - on PostgreSQL takes a `pg_advisory_lock` for the duration so several replicas starting at once don't race; on SQLite relies on `_txlock=immediate`.
   - Does not implement down migrations (the dbmate CLI keeps that role).

3. **Config**: `DATABASE_AUTO_MIGRATE` (bool, TOML `[database] auto_migrate`). Default: `true` for SQLite, `false` for PostgreSQL. Explain the defaults in a comment: operators of PostgreSQL usually run migrations as a separate deploy step. `main.go` runs `Migrate` right after `Open` when enabled and logs each applied version.

4. **Docker Compose**: add a `sqlite` profile with an `api-sqlite` service built from the local Dockerfile, `DATABASE_URL=sqlite:/app/data/gogoform.db`, `DATABASE_AUTO_MIGRATE=true`, a named volume on `/app/data`, and port 8080. The default `docker compose up` behavior (PostgreSQL + Adminer) is unchanged. Add `make up-sqlite` (`docker compose --profile sqlite up -d --build api-sqlite`) with a help comment.

5. **Docs**: in the user-facing "Choosing a database" section from task 000024, add a "Quick start with SQLite" (binary and compose) and document `DATABASE_AUTO_MIGRATE`.

6. **Tests**: the migrator on SQLite `:memory:` (applies everything on an empty DB, is a no-op the second time, applies only new versions when some are already recorded, fails and rolls back on a broken migration and leaves `schema_migrations` untouched), and on PostgreSQL when `TEST_DATABASE_URL` is set (including two concurrent `Migrate` calls applying each migration once). Make the contract suite from task 000023 use `Migrate` instead of its own migration loader.

7. Checks: `go vet ./...`, `go test ./...`, `CGO_ENABLED=0 go build ./...` in `workdir/`; `docker compose --profile sqlite config` is valid; if Docker is available, `make up-sqlite` then `curl localhost:8080/healthz` works on a fresh volume.

<!-- RUNNER:PLAN -->
## Plan

1. [x] Add `workdir/db/embed.go` (package `db`, distinct from `internal/db`) with `//go:embed postgres/migrations/*.sql` and `//go:embed sqlite/migrations/*.sql` vars (e.g. `PostgresMigrations`, `SQLiteMigrations` of type `embed.FS`); confirm `go build ./...` and `sqlc generate` still work unaffected.
2. [x] Extend `internal/database.DB` (database.go/postgres.go/sqlite.go) with unexported raw-connection fields the migrator needs (`*pgxpool.Pool` for postgres, `*sql.DB` for sqlite), populated by `openPostgres`/`openSQLite`; no public API change.
3. [x] Implement `internal/database/migrate.go` with the dbmate-compatible core: parse a migration file's numeric-prefix version and `-- migrate:up` block (through `-- migrate:down`), honor `transaction:false`, create `schema_migrations(version varchar(128) PRIMARY KEY)` if missing, compute pending versions in order, and apply the SQLite path (each pending migration + its `schema_migrations` insert in one transaction, relying on the existing `_txlock=immediate` DSN option). Structure it so the migration source (embed.FS) is swappable in-package for tests (e.g. an unexported `migrate(ctx, d, fsys fs.FS)` that `Migrate` calls with the real embedded FS). Add `internal/database/migrate_test.go` covering SQLite `:memory:`: applies everything on empty DB, no-op on second run, applies only new versions when some are already recorded, and fails/rolls back on a broken migration (via an `fstest.MapFS` fixture) leaving `schema_migrations` untouched.
4. [x] Extend `migrate.go` with the PostgreSQL path: acquire a `pg_advisory_lock` on a dedicated pooled connection for the duration of `Migrate`, apply pending migrations the same way, release the lock/connection after. Add tests in `migrate_test.go` (or a new `migrate_postgres_test.go`) gated on `TEST_DATABASE_URL` (skip like the existing contract suite): full apply, no-op rerun, and two concurrent `Migrate` calls against the same fresh schema each applying every migration exactly once.
5. [x] Add `DatabaseConfig.AutoMigrate *bool` (`toml:"auto_migrate" env:"DATABASE_AUTO_MIGRATE"`) to `internal/config/config.go`, a `reflect.Ptr`-to-bool case in `applyEnv` (via `strconv.ParseBool`), and an `AutoMigrateEnabled() bool` method resolving the pointer or defaulting to `true` for `DriverSQLite`/`false` otherwise, with a comment explaining PostgreSQL operators usually run migrations as a separate deploy step. Update `workdir/.env.example` and `workdir/config.toml` with the new commented-out setting, and add/update config tests for the default and both explicit values.
6. [x] Wire `cmd/api/main.go`: after the existing `database.Open`/"database connected" log, call `database.Migrate` when `cfg.Database.AutoMigrateEnabled()`, returning an error on failure and logging one `slog` line per applied version.
7. [x] Update `internal/database/contract_test.go` and `internal/database/schema_parity_test.go` to build a `*DB` and call `Migrate` instead of their own `applySQLiteMigrations`/`applyPostgresMigrations`/`readMigrationUp` helpers; delete the now-dead duplicate loader functions from both files.
8. [x] Add a `sqlite` Compose profile to `workdir/docker-compose.yml`: an `api-sqlite` service built from the local `Dockerfile` (context `.`), env `DATABASE_URL=sqlite:/app/data/gogoform.db` and `DATABASE_AUTO_MIGRATE=true`, a new named volume mounted at `/app/data`, and port 8080 published — without changing the default `docker compose up` (postgres+adminer) behavior. Verify with `docker compose --profile sqlite config`.
9. [x] Add an `up-sqlite` target to `workdir/Makefile` (`docker compose --profile sqlite up -d --build api-sqlite`) with a `## ` help comment, and add it to the `.PHONY` list.
10. [x] Update `README.md`'s "Choosing a database" section with a "Quick start with SQLite" subsection (binary run and `make up-sqlite` compose flow) and document `DATABASE_AUTO_MIGRATE`/`auto_migrate` and its per-engine default.
11. [x] Run final checks in `workdir/`: `go vet ./...`, `go test ./...`, `CGO_ENABLED=0 go build ./...`; `docker compose --profile sqlite config`; and, if Docker is available, `make up-sqlite` followed by `curl localhost:8080/healthz` against a fresh volume, fixing any issues surfaced.
