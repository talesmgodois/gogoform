package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoutes(t *testing.T) {
	srv := httptest.NewServer(routes(testServices()))
	defer srv.Close()

	tests := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/healthz", http.StatusOK},
		{http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
		{http.MethodGet, "/swagger/index.html", http.StatusOK},
		{http.MethodGet, "/swagger/doc.json", http.StatusOK},
		{http.MethodGet, "/unknown", http.StatusNotFound},
		// Tenant-scoped endpoints require an API key.
		{http.MethodGet, "/tenants/me", http.StatusUnauthorized},
		{http.MethodPost, "/forms", http.StatusUnauthorized},
		{http.MethodGet, "/forms", http.StatusUnauthorized},
		{http.MethodGet, "/forms/1", http.StatusUnauthorized},
		{http.MethodPut, "/forms/1", http.StatusUnauthorized},
		{http.MethodDelete, "/forms/1", http.StatusUnauthorized},
		{http.MethodGet, "/forms/1/submissions", http.StatusUnauthorized},
		{http.MethodGet, "/forms/1/webhooks", http.StatusUnauthorized},
		{http.MethodPost, "/forms/1/webhooks", http.StatusUnauthorized},
		// Public endpoints do not.
		{http.MethodGet, "/public/forms/missing", http.StatusNotFound},
		{http.MethodPatch, "/forms/1", http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		req, _ := http.NewRequest(tt.method, srv.URL+tt.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tt.method, tt.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != tt.want {
			t.Errorf("%s %s = %d, want %d", tt.method, tt.path, resp.StatusCode, tt.want)
		}
	}
}
