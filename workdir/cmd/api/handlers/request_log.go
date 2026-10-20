package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"app/internal/logger"
)

// requestIDHeader carries the request ID, accepted from the reverse proxy
// and echoed on every response so a user-reported failure can be found in
// the logs.
const requestIDHeader = "X-Request-ID"

// maxRequestIDLen bounds an incoming request ID; longer ones are replaced.
const maxRequestIDLen = 128

// healthPath is logged at debug level only: Docker probes it every few
// seconds and it would drown the access log.
const healthPath = "/healthz"

// requestLog assigns every request an ID (the incoming X-Request-ID when it
// is well-formed, a random one otherwise), stores it in the request context
// (logger.WithRequestID) so every record logged while serving it carries it,
// echoes it in the response and writes one access log record per request
// once it has been served. ip resolves the client address behind trusted
// proxies.
func requestLog(next http.Handler, ip clientIPResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get(requestIDHeader)
		if !validRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set(requestIDHeader, id)
		ctx := logger.WithRequestID(r.Context(), id)

		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r.WithContext(ctx))

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		level := slog.LevelInfo
		switch {
		case status >= http.StatusInternalServerError:
			level = slog.LevelError
		case r.URL.Path == healthPath:
			level = slog.LevelDebug
		}
		// The query string is left out: it may carry tokens (password reset,
		// email verification links).
		slog.Default().Log(ctx, level, "http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", ip.resolve(r),
			"user_agent", r.UserAgent(),
		)
	})
}

// validRequestID reports whether id is a non-empty, bounded string of
// letters, digits, '-', '_' and '.', safe to echo and to log.
func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

// newRequestID returns 16 random bytes, hex-encoded.
func newRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// statusRecorder captures the status code and body size of a response.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

// WriteHeader records the first status code written.
func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

// Write records the body size, implying a 200 status when none was written.
func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Unwrap exposes the underlying writer to http.ResponseController (Flush,
// deadlines).
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}
