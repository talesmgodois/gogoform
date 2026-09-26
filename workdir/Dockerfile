# syntax=docker/dockerfile:1

# ---- build ----
FROM golang:1.22-alpine AS build
WORKDIR /src

# Download modules first so they're cached across source-only changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# distroless has no shell, so the directory for the SQLite database file is
# created here and copied in with the right ownership.
RUN mkdir -p /out/data

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=build /out/api /app/api
# Baseline config; override any value with env vars (SERVER_PORT, SERVER_ENV,
# LOG_LEVEL, DATABASE_URL) or mount a different file and pass -config.
COPY config.toml /app/config.toml
# Writable directory for a SQLite database file. Mount a volume here and set
# DATABASE_URL=sqlite:/app/data/gogoform.db to use it.
COPY --from=build --chown=nonroot:nonroot /out/data /app/data

ENV SERVER_ENV=production \
    LOG_LEVEL=info

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/api"]
CMD ["-config", "/app/config.toml"]
