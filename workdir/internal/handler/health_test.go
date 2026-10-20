package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// pingFunc adapts a function to Pinger.
type pingFunc func(ctx context.Context) error

func (f pingFunc) Ping(ctx context.Context) error { return f(ctx) }

func serveHealth(t *testing.T, db Pinger) (*httptest.ResponseRecorder, HealthResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	NewHealth(db)(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	var body HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, body.Timestamp); err != nil {
		t.Fatalf("timestamp %q is not ISO-8601: %v", body.Timestamp, err)
	}
	return rec, body
}

func TestHealth(t *testing.T) {
	tests := []struct {
		name       string
		db         Pinger
		wantCode   int
		wantStatus string
	}{
		{"no database", nil, http.StatusOK, "UP"},
		{"database reachable", pingFunc(func(context.Context) error { return nil }), http.StatusOK, "UP"},
		{"database down", pingFunc(func(context.Context) error { return errors.New("connection refused") }),
			http.StatusServiceUnavailable, "DOWN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, body := serveHealth(t, tt.db)
			if rec.Code != tt.wantCode {
				t.Fatalf("code = %d, want %d", rec.Code, tt.wantCode)
			}
			if body.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", body.Status, tt.wantStatus)
			}
		})
	}
}

func TestHealthPingHasDeadline(t *testing.T) {
	var hasDeadline bool
	serveHealth(t, pingFunc(func(ctx context.Context) error {
		_, hasDeadline = ctx.Deadline()
		return nil
	}))
	if !hasDeadline {
		t.Fatal("ping context has no deadline")
	}
}
