# internal/pkg — domain packages

Loaded only when working here. Root rules in `/AGENTS.md` still apply.

Packages: auth (users, JWT, sessions, user tokens, notifier), billing (Stripe gateway + webhook events),
files, forms (content, validation, availability windows), mailer (SMTP/log/async + templates),
plans (limits `Enforcer`), submissions, tenants, webhooks.

## File roles in each `<domain>/` (copy the pattern)
- `<d>_domain.go` — types (`Form`, `CreateFormInput`, …) and pure rules.
- `<d>_ports.go` — the `Repository` interface the handlers depend on; doc comment lists the apperrors codes each method returns.
- `<d>_service.go` — `Service{q db.Querier}` implementing it (`var _ Repository = (*Service)(nil)`), `NewService(q)`; maps sqlc rows → domain types; client messages as `msg*` consts.
- `<d>_store.go` (auth) — persistence helpers over `db.Querier`.
- Extra files per concern (`forms_validation.go`, `billing_stripe.go`, …), each with its `_test.go`.

## Rules
- Return `apperrors.New/Wrap(...)`; map `pgx.ErrNoRows` → `CodeNotFound`, unique violations via `internal/database` helpers → `CodeConflict`.
- Tenant-facing methods are always scoped by `tenantID`.
- New query: edit `db/queries/<d>.sql` → `make sqlc` → use the generated method (check its signature in `internal/db/querier.go`, not the big `*.sql.go`).

## Tests
- `fakeQuerier` in `<d>_service_test.go` embeds `db.Querier` and stubs only the methods used (`createForm func(...)`); an unstubbed call panics — add a stub field.
- Run one package: `make test PKGS=./internal/pkg/<d>/...`.
