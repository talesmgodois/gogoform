package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"app/internal/config"
)

func TestRoutes(t *testing.T) {
	srv := httptest.NewServer(Routes(testServices(), testFeatures, testStripe, testApp))
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
		{http.MethodGet, "/static/form-render.js", http.StatusOK},
		{http.MethodGet, "/static/missing.js", http.StatusNotFound},
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
		// The dashboard sends anonymous visitors to the sign-in page.
		{http.MethodGet, "/app", http.StatusSeeOther},
		{http.MethodPost, "/app", http.StatusMethodNotAllowed},
		// Public endpoints do not.
		{http.MethodGet, "/public/forms/missing", http.StatusNotFound},
		{http.MethodGet, "/f/missing", http.StatusNotFound},
		{http.MethodPatch, "/forms/1", http.StatusMethodNotAllowed},
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, tt := range tests {
		req, _ := http.NewRequest(tt.method, srv.URL+tt.path, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tt.method, tt.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != tt.want {
			t.Errorf("%s %s = %d, want %d", tt.method, tt.path, resp.StatusCode, tt.want)
		}
	}
}

func TestDevOnlyRoutes(t *testing.T) {
	devRoutes := []struct{ method, path string }{
		{http.MethodPost, "/tenants"},
		{http.MethodGet, "/samples/public"},
		{http.MethodGet, "/samples/signed-in"},
		{http.MethodGet, "/samples/form-creator"},
		{http.MethodGet, "/samples/admin"},
	}
	tests := []struct {
		name     string
		features config.FeaturesConfig
		mounted  bool
	}{
		{"production", featureFlags(false), false},
		{"unset", config.FeaturesConfig{}, false},
		{"development", featureFlags(true), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := Routes(testServices(), tt.features, testStripe, testApp)
			for _, r := range devRoutes {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(r.method, r.path, nil))
				if got := rec.Code == http.StatusNotFound; got == tt.mounted {
					t.Errorf("%s %s = %d, mounted = %v", r.method, r.path, rec.Code, tt.mounted)
				}
			}
		})
	}
}
