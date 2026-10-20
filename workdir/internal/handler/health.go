// Package handler contains the HTTP handlers of the API.
package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// healthPingTimeout bounds the database ping of a health check, so a hung
// database fails the check instead of stalling the orchestrator probing it.
const healthPingTimeout = 2 * time.Second

// Pinger checks that a dependency (the database pool) is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// HealthResponse is the body returned by the health check endpoint.
type HealthResponse struct {
	Status    string `json:"status" example:"UP"`
	Timestamp string `json:"timestamp" example:"2026-01-01T00:00:00Z"`
}

// NewHealth returns the health check handler. It pings db and reports DOWN
// with 503 when the ping fails, so Docker and the reverse proxy stop routing
// to an instance that lost its database. A nil db always reports UP.
//
//	@Summary		Health check
//	@Description	Reports whether the service is up and its database is reachable.
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	HealthResponse	"Service is up"
//	@Failure		503	{object}	HealthResponse	"Database unreachable"
//	@Router			/healthz [get]
func NewHealth(db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp := HealthResponse{Status: "UP", Timestamp: time.Now().UTC().Format(time.RFC3339)}
		code := http.StatusOK
		if db != nil {
			ctx, cancel := context.WithTimeout(r.Context(), healthPingTimeout)
			err := db.Ping(ctx)
			cancel()
			if err != nil {
				slog.ErrorContext(r.Context(), "health: database ping failed", "error", err)
				resp.Status, code = "DOWN", http.StatusServiceUnavailable
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			slog.ErrorContext(r.Context(), "encode health response", "error", err)
		}
	}
}
