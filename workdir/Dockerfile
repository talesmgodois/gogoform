# syntax=docker/dockerfile:1

# ---- build ----
FROM golang:1.22-alpine AS build
WORKDIR /src

# Download modules first so they're cached across source-only changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=build /out/api /app/api
# Baseline config; override any value with env vars (SERVER_PORT, SERVER_ENV,
# LOG_LEVEL, DATABASE_URL) or mount a different file and pass -config.
COPY config.toml /app/config.toml

ENV SERVER_ENV=production \
    LOG_LEVEL=info

EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/api"]
CMD ["-config", "/app/config.toml"]
