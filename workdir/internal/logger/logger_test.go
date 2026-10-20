package logger

import (
	"bytes"
	"context"
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

func TestNewAddsRequestIDFromContext(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, "info", "production").With("component", "test")
	log.InfoContext(WithRequestID(context.Background(), "abc123"), "hello")
	log.InfoContext(context.Background(), "no id")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines: %q", len(lines), buf.String())
	}
	var first, second map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if first["request_id"] != "abc123" || first["component"] != "test" {
		t.Fatalf("first record = %v", first)
	}
	if _, ok := second["request_id"]; ok {
		t.Fatalf("second record has a request_id: %v", second)
	}
}
