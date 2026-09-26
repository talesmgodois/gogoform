# 000025 sqlite_support_phase4_ci_checks

Phase 4 of the multi-database plan in `docs/architecture/multi-database.md` (repo root; `../docs/...` from `workdir/`). Read the "Testing" section first. It depends on tasks 000021, 000023 and 000024.

Goal: CI keeps both engines working and catches drift between the SQL and the generated code, or between the two schemas.

1. In `.github/workflows/deploy.yml`, `test` job: add a step that installs sqlc (pinned to the version in the headers of `workdir/internal/db/*.go`) and runs `sqlc diff` in `workdir/`, failing when the generated code is out of date. `go test ./...` already runs the SQLite contract suite; make sure `CGO_ENABLED=0` is set for the test and vet steps so the pure-Go driver is what CI exercises.

2. Add a `test-postgres` job (runs in parallel with `test`, required by `build` like `test` is): a `postgres:18-alpine` service container with a health check, `TEST_DATABASE_URL` pointing at it, and `go test ./internal/database/...` so the contract suite runs against PostgreSQL. Confirm in the test output that the suite ran and was not skipped (e.g. `go test -v -run Contract` and grep the output, or have the suite fail when a `CI_REQUIRE_POSTGRES=1` variable is set but `TEST_DATABASE_URL` is empty).

3. **Schema parity test** (in `internal/database`, runs when both engines are available, i.e. in the `test-postgres` job): apply both migration sets and compare, per table, the set of column names and their nullability (from `information_schema.columns` on PostgreSQL and `pragma_table_info` on SQLite), ignoring dbmate's `schema_migrations`. Fail with a clear diff listing the missing/extra columns per engine.

4. Add a migration checklist to the repo: if `.github/pull_request_template.md` exists, add an item; otherwise create it with a short "Database changes" section: "migration added to both `db/postgres/migrations` and `db/sqlite/migrations`", "queries changed in both `db/*/queries`", "`make sqlc` run".

5. Checks: validate the workflow YAML (e.g. `python3 -c "import yaml,sys; yaml.safe_load(open(sys.argv[1]))" .github/workflows/deploy.yml`), and run locally everything the jobs run that you can (`sqlc diff`, `go test ./...`, the parity test if a PostgreSQL is available).
