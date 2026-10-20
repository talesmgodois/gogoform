package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"app/docs"
)

func TestSetSwaggerHost(t *testing.T) {
	tests := []struct {
		baseURL, wantHost, wantScheme string
	}{
		{"https://forms.example.com", "forms.example.com", "https"},
		{"http://localhost:9000/", "localhost:9000", "http"},
		{"", "localhost:8080", "http"},
		{"not a url", "localhost:8080", "http"},
	}
	for _, tt := range tests {
		setSwaggerHost(tt.baseURL, 8080)
		if docs.SwaggerInfo.Host != tt.wantHost || len(docs.SwaggerInfo.Schemes) != 1 || docs.SwaggerInfo.Schemes[0] != tt.wantScheme {
			t.Errorf("setSwaggerHost(%q): host %q schemes %v", tt.baseURL, docs.SwaggerInfo.Host, docs.SwaggerInfo.Schemes)
		}
	}
}

func TestProbeHealth(t *testing.T) {
	for _, code := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/healthz" {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(code)
		}))
		u, _ := url.Parse(srv.URL)
		port, _ := strconv.Atoi(u.Port())
		err := probeHealth(port)
		srv.Close()
		if (err == nil) != (code == http.StatusOK) {
			t.Errorf("status %d: err = %v", code, err)
		}
	}
}
