# gogoform

A self-hosted, multi-tenant form backend written in Go. Tenants get an API
key to create forms, collect submissions, register webhooks and store
uploaded files; a small `/app` operator dashboard and a JWT/HTTP-Basic user
system sit on top for managing accounts.

## Features

- **Multi-tenant** — each tenant is scoped by its own API key (`X-API-Key`).
- **Forms** — create, update, publish/draft, and bound availability with a
  start/end date; forms lock once they have submissions.
- **Public forms** — can be filled in anonymously without signing in.
- **Submissions & webhooks** — list submissions per form and notify external
  URLs when one comes in.
- **File uploads** tied to forms and submissions.
- **Auth** — JWT-based sign-in plus role-based guards (admin, form creator,
  signed-in, public), with an HTTP Basic-protected `/app` dashboard.
- **Swagger UI** served at `/swagger/`.

## Requirements

- Go 1.22+
- Docker (for PostgreSQL via `docker compose`)
- [dbmate](https://github.com/amacneil/dbmate) and [sqlc](https://sqlc.dev/)
  for the database pipeline (installed by `make setup-tools`)

## Getting started

```bash
cp workdir/.env.example workdir/.env
make dev
```

`make dev` starts PostgreSQL (waiting for its healthcheck) and runs the API
locally on `SERVER_PORT` (default `8080`). Configuration is read from
`workdir/config.toml`, with `workdir/.env` values taking precedence — see
`workdir/.env.example` for every available setting.

For hot reload during development, use `make watch` instead — it starts
PostgreSQL and runs the API through [air](https://github.com/air-verse/air),
rebuilding and restarting the server whenever a `.go`, `.html` or `.toml`
file changes (config in `workdir/.air.toml`). Install it with `make setup-tools`.

Run `make help` to list every available target, including the database
migration pipeline (`generate-schema`, `migrate-up`, `sqlc`, ...) and
`make adminer` for a web UI to browse the database.

## Testing

```bash
make test
```

## API documentation

With the server running, Swagger UI is available at
`http://localhost:8080/swagger/`.

## Contributing

Contributions are welcome.

1. Fork the repository and create a branch for your change.
2. Run `make test` and make sure the project builds (`make build`) before
   opening a pull request.
3. Keep pull requests focused — prefer several small PRs over one large one.
4. Describe the *why* behind the change in the PR description, not just the
   *what*.

Please open an issue first to discuss significant changes before doing the
implementation work.

## License

This project is licensed under the [MIT License](workdir/LICENSE).
