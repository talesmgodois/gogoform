package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestAppCredentials(t *testing.T) {
	cfg, err := Load(writeConfig(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.App.Enabled() {
		t.Fatal("app dashboard enabled without credentials")
	}

	t.Setenv("APP_USERNAME", "admin")
	t.Setenv("APP_PASSWORD", "s3cret")
	cfg, err = Load(writeConfig(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.App.Enabled() || cfg.App.Username != "admin" || cfg.App.Password != "s3cret" {
		t.Fatalf("app credentials not applied: %+v", cfg.App)
	}
}

func TestAuthConfig(t *testing.T) {
	cfg, err := Load(writeConfig(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.JWTSecret != "" || cfg.Auth.TokenTTL() != time.Hour {
		t.Fatalf("unexpected auth defaults: %+v", cfg.Auth)
	}

	secret := strings.Repeat("s", minJWTSecretLen)
	t.Setenv("AUTH_JWT_SECRET", secret)
	t.Setenv("AUTH_TOKEN_TTL_MINUTES", "15")
	t.Setenv("AUTH_ADMIN_USERNAME", "root")
	t.Setenv("AUTH_ADMIN_PASSWORD", "change-me-now")
	cfg, err = Load(writeConfig(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.JWTSecret != secret || cfg.Auth.TokenTTL() != 15*time.Minute ||
		cfg.Auth.AdminUsername != "root" || cfg.Auth.AdminPassword != "change-me-now" {
		t.Fatalf("auth overrides not applied: %+v", cfg.Auth)
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
		{name: "app username without password", content: sample, env: map[string]string{"APP_USERNAME": "admin"}},
		{name: "app password without username", content: sample, env: map[string]string{"APP_PASSWORD": "s3cret"}},
		{name: "app username with colon", content: sample, env: map[string]string{"APP_USERNAME": "a:b", "APP_PASSWORD": "s3cret"}},
		{name: "short jwt secret", content: sample, env: map[string]string{"AUTH_JWT_SECRET": "too-short"}},
		{name: "zero token ttl", content: sample, env: map[string]string{"AUTH_TOKEN_TTL_MINUTES": "0"}},
		{name: "admin username without password", content: sample, env: map[string]string{"AUTH_ADMIN_USERNAME": "admin"}},
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

func TestDatabaseConfigDriver(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want Driver
	}{
		{name: "postgres scheme", uri: "postgres://user:pass@localhost:5432/db", want: DriverPostgres},
		{name: "postgresql scheme", uri: "postgresql://user:pass@localhost:5432/db", want: DriverPostgres},
		{name: "unsupported scheme", uri: "mysql://user:pass@localhost/db", want: ""},
		{name: "empty uri", uri: "", want: ""},
		{name: "invalid uri", uri: "://bad", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DatabaseConfig{URI: tt.uri}
			if got := cfg.Driver(); got != tt.want {
				t.Fatalf("Driver() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateDatabaseURIErrorsDoNotLeakCredentials(t *testing.T) {
	const secret = "super-secret-password"
	tests := []struct {
		name string
		uri  string
	}{
		{name: "unsupported scheme", uri: "mysql://user:" + secret + "@localhost:5432/db"},
		{name: "missing host", uri: "postgres://user:" + secret + "@/db"},
		{name: "invalid uri", uri: "postgres://user:" + secret + "@%zz/db"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDatabaseURI(tt.uri)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaked credentials: %v", err)
			}
			if strings.Contains(err.Error(), tt.uri) {
				t.Fatalf("error leaked URI: %v", err)
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
