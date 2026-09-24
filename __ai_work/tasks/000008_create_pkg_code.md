Create the domain packages that sit on top of the sqlc-generated code in `workdir/internal/db`. Put one package per table under `workdir/internal/pkg/<domain>/` (not a top-level `workdir/pkg/`); `internal/pkg/tenants/` and `internal/pkg/submissions/` already exist as empty directories.

Each package contains:

- `<domain>_domain.go` — structs that define the domain data (decoupled from the sqlc models in `internal/db/models.go`).
- `<domain>_ports.go` — the interface used to manipulate that domain on the database.
- `<domain>_service.go` — a service implementing that interface by wrapping the sqlc `db.Querier`, mapping db models to domain structs and returning `internal/errors` `AppError`s (e.g. `NewNotFound` for `pgx.ErrNoRows`).

Order: build `forms` first (`internal/pkg/forms/`) — it sets the pattern the others follow — then `submissions` (`form_submissions` + `submission_metadata`), `tenants`, and finally `webhooks` (`form_webhooks`).

* Write godoc for every exported type and method.
* Add unit tests for each service using a fake `db.Querier`.
