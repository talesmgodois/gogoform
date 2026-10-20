# 000028 production_deploy

Target: a single VPS running Docker.

- `workdir/docker-compose.prod.yml`: app (image from GHCR), Postgres (no public port, persistent volume), Caddy (automatic HTTPS, HSTS, gzip, reverse proxy to the app). Env comes from an `.env.prod` (document every variable in `.env.example`).
- Migrations run on deploy: add dbmate to the image or a one-shot `migrate` service that runs `dbmate up` before the app starts.
- `/healthz` pings the database (DOWN + 503 when it fails); add a Docker `HEALTHCHECK`.
- Daily Postgres backup (pg_dump service/cron with retention, optionally pushed to S3-compatible storage) and a documented restore procedure.
- Build Tailwind CSS at image build time instead of `cdn.tailwindcss.com`; update the CSP.
- Swagger host from config (`APP_BASE_URL`) instead of hard-coded localhost.
- Request ID + access log middleware (slog).
- CI (`.github/workflows/deploy.yml`): add `govulncheck`; document what `DEPLOY_WEBHOOK_URL` must do on the VPS (pull + `docker compose up -d`) and provide that script in `workdir/scripts/`.
- `README.md` at the repo root: local dev, configuration, deploy, backup/restore.

<!-- RUNNER:PLAN -->
## Plan

1. [x] Health and observability (Go): make `/healthz` (`internal/handler/health.go`) ping the database through a small pinger interface and return DOWN with 503 when the ping fails. Add an `-healthcheck` flag to `cmd/api/main.go` that calls `/healthz` and exits non-zero on failure, for the distroless HEALTHCHECK. Add slog middleware for request IDs (X-Request-ID) and access logs, wrapped around the mux in `handlers.Routes`. Set `docs.SwaggerInfo.Host`/Schemes from `APP_BASE_URL` instead of `localhost`. Add or update tests and run `make swagger`.
2. [x] Build Tailwind at image build time: add a Tailwind config and input CSS that scan `cmd/api/handlers/templates`, plus a `make css` target using the standalone Tailwind CLI. Add a Dockerfile build stage that writes `templates/static/app.css` before the Go build, so the CSS is embedded. Add a handler and route that serve the stylesheet. Replace the `cdn.tailwindcss.com` script tags in `layout.html`, `public_form.html` and `public_message.html` with `<link rel=stylesheet>`. Tighten the CSPs in `app.go`, `public_page.go` and `security_headers.go` (remove the CDN, `style-src 'self'`) and update the CSP tests.
3. [x] Image and production compose: add a Docker HEALTHCHECK that uses `/app/api -healthcheck`. Create `workdir/docker-compose.prod.yml` with: the app image from GHCR (`APP_IMAGE`, `env_file: .env.prod`); Postgres with no public port and a persistent volume; a one-shot `migrate` service (dbmate with `db/migrations`, `dbmate --wait up`) that the app waits on via `service_completed_successfully`; Caddy with `workdir/deploy/Caddyfile` (automatic HTTPS for `APP_DOMAIN`, HSTS, gzip/zstd, `reverse_proxy app:8080`) and volumes for its data and config. Document every production variable in `.env.example` (`APP_DOMAIN`, `APP_IMAGE`, `APP_BASE_URL`, `SECURITY_TRUSTED_PROXIES` for the Caddy network, Postgres credentials, backup/S3 variables).
4. [x] Daily Postgres backups: add a `backup` service to the production compose that runs a daily `pg_dump -Fc` into a backups volume, deletes dumps older than `BACKUP_RETENTION_DAYS` and, optionally, uploads to S3-compatible storage when the S3 variables are set. Add `workdir/scripts/backup.sh` (the service's entrypoint, also runnable by hand) and `workdir/scripts/restore.sh` (fetch a dump locally or from S3, stop the app, `pg_restore --clean`, run migrations, start the app).
5. [x] CI and the deploy hook: add a `govulncheck` step or job to `.github/workflows/deploy.yml`. Add `workdir/scripts/deploy.sh` for the VPS: it checks the `X-Hub-Signature-256` HMAC against `DEPLOY_WEBHOOK_SECRET`, reads `image`/`digest` from the JSON payload, sets `APP_IMAGE`, runs `docker compose -f docker-compose.prod.yml pull` and `up -d`, then waits for the app to be healthy. Add a sample webhook-receiver config (e.g. adnanh/webhook behind Caddy), and document in the workflow and docs what `DEPLOY_WEBHOOK_URL` must do.
6. [x] Documentation: rewrite the repo-root `README.md` to cover local dev (`make dev`, `make css`, tests), configuration (`config.toml` plus env variables, pointing to `.env.example`), deploy (VPS setup, `.env.prod`, Caddy and DNS, GHCR login, webhook and `deploy.sh`, migrations, healthz) and backup/restore. Keep the existing run-tasks section. Update `AGENTS.md` (Tailwind is now built, not CDN; new scripts and compose files).
