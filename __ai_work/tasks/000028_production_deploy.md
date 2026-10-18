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
