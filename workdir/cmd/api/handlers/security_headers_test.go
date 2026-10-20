package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	svc := testServices(publicSurvey())
	for _, tt := range []struct {
		path  string
		embed bool
	}{
		{"/app/signin", false},
		{"/f/feedback", false},
		{"/f/feedback/thanks", false},
		{"/f/feedback?embed=1", true},
		{"/f/feedback/thanks?embed=1", true},
		{"/f/missing", false},
		{"/healthz", false},
	} {
		t.Run(tt.path, func(t *testing.T) {
			rec := serve(svc, pageRequest(t, svc, tt.path, ""))
			h := rec.Header()
			for name, want := range map[string]string{
				"X-Content-Type-Options": "nosniff",
				"Referrer-Policy":        "no-referrer",
				"Permissions-Policy":     permissionsPolicy,
			} {
				if got := h.Get(name); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			assertFrameAncestors(t, rec, tt.embed)
			if got := len(h.Values("Content-Security-Policy")); got != 1 {
				t.Errorf("%d Content-Security-Policy headers, want 1", got)
			}
		})
	}
}

func TestPublicCORS(t *testing.T) {
	svc := testServices(publicSurvey())
	for _, tt := range []struct {
		method, path string
		cors         bool
	}{
		{http.MethodGet, "/public/forms/feedback", true},
		{http.MethodPost, "/public/forms/feedback/submissions", true},
		{http.MethodPost, "/public/forms/feedback/files", true},
		{http.MethodGet, "/forms", false},
		{http.MethodPost, "/auth/signin", false},
		{http.MethodGet, "/auth/me", false},
		{http.MethodGet, "/app/signin", false},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader("{}"))
			req.Header.Set("Origin", "https://example.com")
			req.Header.Set("Content-Type", "application/json")
			rec := serve(svc, req)
			got := rec.Header().Get("Access-Control-Allow-Origin")
			if tt.cors && got != "*" {
				t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
			}
			if !tt.cors && got != "" {
				t.Errorf("Access-Control-Allow-Origin = %q, want none", got)
			}
		})
	}
}

func TestPublicCORSPreflight(t *testing.T) {
	svc := testServices(publicSurvey())
	for _, tt := range []struct {
		path, methods string
	}{
		{"/public/forms/feedback", "GET"},
		{"/public/forms/feedback/submissions", "POST"},
		{"/public/forms/feedback/files", "POST"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, tt.path, nil)
			req.Header.Set("Origin", "https://example.com")
			req.Header.Set("Access-Control-Request-Method", tt.methods)
			rec := serve(svc, req)
			assertStatus(t, rec, http.StatusNoContent)
			h := rec.Header()
			if got := h.Get("Access-Control-Allow-Origin"); got != "*" {
				t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
			}
			if got := h.Get("Access-Control-Allow-Methods"); got != tt.methods {
				t.Errorf("Access-Control-Allow-Methods = %q, want %q", got, tt.methods)
			}
			if got := h.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Content-Type") {
				t.Errorf("Access-Control-Allow-Headers = %q", got)
			}
		})
	}

	// Routes other than the public form endpoints answer no preflight.
	req := httptest.NewRequest(http.MethodOptions, "/forms", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := serve(svc, req)
	if rec.Code == http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("OPTIONS /forms = %d with CORS headers %v", rec.Code, rec.Header())
	}
}
