// Package logger builds a structured log/slog logger from configuration.
package logger

import (
	"io"
	"log/slog"
	"strings"
)

// EnvProduction selects JSON output; any other environment uses text output.
const EnvProduction = "production"

// New returns a logger writing to w at the given level. Output is JSON when
// env is "production" and human-readable text otherwise.
func New(w io.Writer, level, env string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: ParseLevel(level)}

	var h slog.Handler
	if strings.EqualFold(env, EnvProduction) {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(h)
}

// ParseLevel maps "debug", "info", "warn" and "error" (case-insensitive) to
// their slog levels, defaulting to info.
func ParseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
