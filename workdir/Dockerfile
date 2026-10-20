# syntax=docker/dockerfile:1

# Tool versions; keep TAILWIND_VERSION in sync with the Makefile.
ARG TAILWIND_VERSION=v3.4.17
ARG DBMATE_VERSION=v2.37.0

# ---- css ----
# Compiles the Tailwind stylesheet the server embeds. The output does not
# depend on the target platform, so this stage runs on the build host.
FROM --platform=$BUILDPLATFORM debian:bookworm-slim AS css
ARG TAILWIND_VERSION
ARG BUILDARCH
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
RUN arch=$([ "$BUILDARCH" = "arm64" ] && echo arm64 || echo x64) \
    && curl -fsSL -o /usr/local/bin/tailwindcss \
       "https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/tailwindcss-linux-${arch}" \
    && chmod +x /usr/local/bin/tailwindcss
WORKDIR /src
COPY tailwind.config.js ./
COPY tailwind ./tailwind
COPY cmd/api/handlers ./cmd/api/handlers
RUN tailwindcss -c tailwind.config.js -i tailwind/input.css -o /out/app.css --minify

# ---- dbmate ----
# Static dbmate binary, run by the `migrate` service of
# docker-compose.prod.yml so migrations ship with the image they belong to.
FROM --platform=$BUILDPLATFORM alpine:3.20 AS dbmate
ARG DBMATE_VERSION
ARG TARGETARCH
RUN wget -qO /dbmate \
       "https://github.com/amacneil/dbmate/releases/download/${DBMATE_VERSION}/dbmate-linux-${TARGETARCH}" \
    && chmod +x /dbmate

# ---- build ----
FROM golang:1.22-alpine AS build
WORKDIR /src

# Download modules first so they're cached across source-only changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=css /out/app.css ./cmd/api/handlers/templates/static/app.css
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=build /out/api /app/api
COPY --from=dbmate /dbmate /app/dbmate
COPY db/migrations /app/db/migrations
# Baseline config; override any value with env vars (SERVER_PORT, SERVER_ENV,
# LOG_LEVEL, DATABASE_URL, ...) or mount a different file and pass -config.
COPY config.toml /app/config.toml

ENV SERVER_ENV=production \
    LOG_LEVEL=info

EXPOSE 8080
USER nonroot:nonroot
# The image has no shell or curl: the binary probes its own /healthz, which
# also pings the database.
HEALTHCHECK --interval=30s --timeout=10s --start-period=20s --retries=3 \
    CMD ["/app/api", "-config", "/app/config.toml", "-healthcheck"]
ENTRYPOINT ["/app/api"]
CMD ["-config", "/app/config.toml"]
