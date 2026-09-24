package logger

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"INFO":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"error":   slog.LevelError,
		"unknown": slog.LevelInfo,
	}
	for in, want := range tests {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNewProductionUsesJSON(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, "info", "production").Info("hello")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", buf.String(), err)
	}
	if entry["msg"] != "hello" {
		t.Fatalf("unexpected msg: %v", entry["msg"])
	}
}

func TestNewDevelopmentUsesText(t *testing.T) {
	var buf bytes.Buffer
	New(&buf, "info", "development").Info("hello")

	if !strings.Contains(buf.String(), "msg=hello") {
		t.Fatalf("expected text output, got %q", buf.String())
	}
}

func TestNewRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, "error", "development")
	log.Info("dropped")
	log.Error("kept")

	out := buf.String()
	if strings.Contains(out, "dropped") || !strings.Contains(out, "kept") {
		t.Fatalf("level filtering failed: %q", out)
	}
}
