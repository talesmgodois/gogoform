package handlers

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"app/internal/logger"
)

// captureLog routes slog.Default to a JSON buffer at debug level for the
// duration of the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(logger.New(&buf, "debug", logger.EnvProduction))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// logRecords decodes the JSON records written to buf.
func logRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func TestRequestLogAssignsIDAndLogsRequest(t *testing.T) {
	buf := captureLog(t)
	var ctxID string
	h := requestLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxID = logger.RequestID(r.Context())
		slog.InfoContext(r.Context(), "inside handler")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hello"))
	}), clientIPResolver{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/forms?token=secret", nil))

	id := rec.Header().Get(requestIDHeader)
	if len(id) != 32 || id != ctxID {
		t.Fatalf("response id %q, context id %q", id, ctxID)
	}
	recs := logRecords(t, buf)
	if len(recs) != 2 {
		t.Fatalf("got %d records: %s", len(recs), buf)
	}
	if recs[0]["msg"] != "inside handler" || recs[0]["request_id"] != id {
		t.Fatalf("handler record = %v", recs[0])
	}
	access := recs[1]
	if access["msg"] != "http request" || access["request_id"] != id || access["method"] != "GET" ||
		access["path"] != "/forms" || access["status"] != float64(http.StatusTeapot) ||
		access["bytes"] != float64(5) || access["ip"] != "192.0.2.1" || access["level"] != "INFO" {
		t.Fatalf("access record = %v", access)
	}
	if strings.Contains(buf.String(), "secret") {
		t.Fatalf("query string leaked into the log: %s", buf)
	}
}

func TestRequestLogRequestIDHeader(t *testing.T) {
	tests := []struct {
		name, in string
		keep     bool
	}{
		{"well-formed", "abc-123_X.y", true},
		{"invalid chars", "abc 123\n", false},
		{"too long", strings.Repeat("a", maxRequestIDLen+1), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			captureLog(t)
			h := requestLog(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), clientIPResolver{})
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set(requestIDHeader, tt.in)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			got := rec.Header().Get(requestIDHeader)
			if (got == tt.in) != tt.keep || got == "" {
				t.Fatalf("X-Request-ID = %q, in %q, keep %v", got, tt.in, tt.keep)
			}
		})
	}
}

func TestRequestLogLevels(t *testing.T) {
	tests := []struct {
		path  string
		code  int
		level string
	}{
		{healthPath, http.StatusOK, "DEBUG"},
		{healthPath, http.StatusServiceUnavailable, "ERROR"},
		{"/forms", http.StatusInternalServerError, "ERROR"},
		{"/forms", http.StatusNotFound, "INFO"},
	}
	for _, tt := range tests {
		buf := captureLog(t)
		h := requestLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tt.code)
		}), clientIPResolver{})
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tt.path, nil))

		recs := logRecords(t, buf)
		if len(recs) != 1 || recs[0]["level"] != tt.level {
			t.Errorf("%s %d: records = %v, want level %s", tt.path, tt.code, recs, tt.level)
		}
	}
}

func TestRoutesSetRequestID(t *testing.T) {
	captureLog(t)
	rec := httptest.NewRecorder()
	Routes(testServices(), testFeatures, testStripe, testApp, testSecurity).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Header().Get(requestIDHeader) == "" {
		t.Fatalf("code = %d, X-Request-ID = %q", rec.Code, rec.Header().Get(requestIDHeader))
	}
}
