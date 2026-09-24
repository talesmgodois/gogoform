// Package handler contains the HTTP handlers of the API.
package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// HealthResponse is the body returned by the health check endpoint.
type HealthResponse struct {
	Status    string `json:"status" example:"UP"`
	Timestamp string `json:"timestamp" example:"2026-01-01T00:00:00Z"`
}

// Health reports that the service is up.
//
//	@Summary		Health check
//	@Description	Reports whether the service is up and running.
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	HealthResponse	"Service is up"
//	@Router			/healthz [get]
func Health(w http.ResponseWriter, r *http.Request) {
	resp := HealthResponse{
		Status:    "UP",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.ErrorContext(r.Context(), "encode health response", "error", err)
	}
}
