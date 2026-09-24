package config

import (
	"os"
	"path/filepath"
	"testing"
)

const sample = `
[server]
port = 8080
env = "development"

[logger]
level = "debug"

[database]
uri = "postgres://postgres:postgres@localhost:5432/app_db?sslmode=disable"
`

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFromFile(t *testing.T) {
	cfg, err := Load(writeConfig(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 8080 || cfg.Server.Env != "development" || cfg.Logger.Level != "debug" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if want := "postgres://postgres:postgres@localhost:5432/app_db?sslmode=disable"; cfg.Database.URI != want {
		t.Fatalf("database.uri = %q, want %q", cfg.Database.URI, want)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	t.Setenv("SERVER_PORT", "9090")
	t.Setenv("SERVER_ENV", "production")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("DATABASE_URL", "postgresql://app:secret@db:5433/other")

	cfg, err := Load(writeConfig(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 9090 || cfg.Server.Env != "production" || cfg.Logger.Level != "error" {
		t.Fatalf("env overrides not applied: %+v", cfg)
	}
	if cfg.Database.URI != "postgresql://app:secret@db:5433/other" {
		t.Fatalf("DATABASE_URL override not applied: %q", cfg.Database.URI)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		env     map[string]string
	}{
		{name: "invalid toml", content: "[server\nport = "},
		{name: "invalid port env", content: sample, env: map[string]string{"SERVER_PORT": "abc"}},
		{name: "port out of range", content: sample, env: map[string]string{"SERVER_PORT": "70000"}},
		{name: "invalid level", content: sample, env: map[string]string{"LOG_LEVEL": "verbose"}},
		{name: "missing database uri", content: "[server]\nport = 8080\n"},
		{name: "unsupported database scheme", content: sample, env: map[string]string{"DATABASE_URL": "mysql://u:p@localhost/db"}},
		{name: "database uri without host", content: sample, env: map[string]string{"DATABASE_URL": "postgres:///app_db"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if _, err := Load(writeConfig(t, tt.content)); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.toml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestSingleton(t *testing.T) {
	path := writeConfig(t, sample)

	first, err := Init(path)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if second := GetConfig(); second != first {
		t.Fatal("GetConfig returned a different instance")
	}
}
