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

1. [x] Embed `db/postgres/migrations` and `db/sqlite/migrations` with `embed.FS` in a small `app/db` package (`workdir/db/embed.go`) exposing `Migrations(engine)`.
2. [x] Add `internal/database/migrate.go`: `Migrate(ctx, d)` parses dbmate `-- migrate:up` blocks (honoring `transaction:false`), records versions in dbmate's `schema_migrations(version varchar(128) PRIMARY KEY)`, applies each pending migration plus its version in one transaction in version order, holds a `pg_advisory_lock` on PostgreSQL and relies on `_txlock=immediate` (with an in-transaction re-check) on SQLite; keep the pool / `*sql.DB` on `DB` for it.
3. [x] Add `DATABASE_AUTO_MIGRATE` / `[database] auto_migrate` as a `*bool` (env loader gains `*bool` support) with `AutoMigrateEnabled()` defaulting to true on SQLite and false on PostgreSQL; run `Migrate` in `main.go` right after `Open` and log each applied version.
4. [x] Add the `sqlite` compose profile with the `api-sqlite` service (local build, SQLite URL, auto-migrate, named volume on `/app/data`, port 8080) and `make up-sqlite`.
5. [x] Document "Quick start with SQLite" and `DATABASE_AUTO_MIGRATE` in the README's "Choosing a database" section, `config.toml` and `.env.example`.
6. [x] Tests: parser and loader, SQLite `:memory:` migrator (all/no-op/only new/rollback of a broken migration/transaction:false), PostgreSQL migrator incl. two concurrent calls when `TEST_DATABASE_URL` is set, config defaults/overrides; switch the contract and parity suites to `Migrate`.
7. [x] Checks: `go vet ./...`, `go test ./...` (with a local PostgreSQL), `CGO_ENABLED=0 go build ./...`, `docker compose --profile sqlite config`, and the `api-sqlite` container answering `/healthz` on a fresh volume.
