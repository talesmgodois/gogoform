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
		{name: "sqlite relative path", uri: "sqlite:./data/app.db", want: DriverSQLite},
		{name: "sqlite absolute path no slashes", uri: "sqlite:/abs/path.db", want: DriverSQLite},
		{name: "sqlite absolute path with slashes", uri: "sqlite:///abs/path.db", want: DriverSQLite},
		{name: "sqlite memory", uri: "sqlite::memory:", want: DriverSQLite},
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

func TestDatabaseConfigSQLitePath(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want string
	}{
		{name: "relative path", uri: "sqlite:./data/app.db", want: "./data/app.db"},
		{name: "absolute path no slashes", uri: "sqlite:/abs/path.db", want: "/abs/path.db"},
		{name: "absolute path with slashes", uri: "sqlite:///abs/path.db", want: "/abs/path.db"},
		{name: "memory", uri: "sqlite::memory:", want: ":memory:"},
		{name: "missing path", uri: "sqlite:", want: ""},
		{name: "non-sqlite scheme", uri: "postgres://user:pass@localhost:5432/db", want: ""},
		{name: "empty uri", uri: "", want: ""},
		{name: "invalid uri", uri: "://bad", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DatabaseConfig{URI: tt.uri}
			if got := cfg.SQLitePath(); got != tt.want {
				t.Fatalf("SQLitePath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateDatabaseURI(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		wantErr bool
	}{
		{name: "postgres with host", uri: "postgres://user:pass@localhost:5432/db", wantErr: false},
		{name: "postgresql with host", uri: "postgresql://user:pass@localhost:5432/db", wantErr: false},
		{name: "postgres without host", uri: "postgres:///db", wantErr: true},
		{name: "sqlite relative path", uri: "sqlite:./data/app.db", wantErr: false},
		{name: "sqlite absolute path no slashes", uri: "sqlite:/abs/path.db", wantErr: false},
		{name: "sqlite absolute path with slashes", uri: "sqlite:///abs/path.db", wantErr: false},
		{name: "sqlite memory", uri: "sqlite::memory:", wantErr: false},
		{name: "sqlite without path", uri: "sqlite:", wantErr: true},
		{name: "unsupported scheme", uri: "mysql://user:pass@localhost/db", wantErr: true},
		{name: "empty uri", uri: "", wantErr: true},
		{name: "invalid uri", uri: "://bad", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDatabaseURI(tt.uri)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateDatabaseURI(%q) error = %v, wantErr %v", tt.uri, err, tt.wantErr)
			}
		})
	}
}

func TestUnsupportedSchemeListsSupportedEngines(t *testing.T) {
	err := validateDatabaseURI("mysql://user:pass@localhost/db")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	for _, want := range []string{"postgres", "postgresql", "sqlite"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

func TestSQLiteTuningDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Database.SQLiteBusyTimeoutMS != 5000 {
		t.Fatalf("SQLiteBusyTimeoutMS = %d, want 5000", cfg.Database.SQLiteBusyTimeoutMS)
	}
	if cfg.Database.SQLiteJournalMode != "WAL" {
		t.Fatalf("SQLiteJournalMode = %q, want WAL", cfg.Database.SQLiteJournalMode)
	}
}

func TestSQLiteTuningEnvOverrides(t *testing.T) {
	t.Setenv("DATABASE_SQLITE_BUSY_TIMEOUT_MS", "1000")
	t.Setenv("DATABASE_SQLITE_JOURNAL_MODE", "DELETE")
	cfg, err := Load(writeConfig(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Database.SQLiteBusyTimeoutMS != 1000 {
		t.Fatalf("SQLiteBusyTimeoutMS = %d, want 1000", cfg.Database.SQLiteBusyTimeoutMS)
	}
	if cfg.Database.SQLiteJournalMode != "DELETE" {
		t.Fatalf("SQLiteJournalMode = %q, want DELETE", cfg.Database.SQLiteJournalMode)
	}
}

func TestSQLiteTuningValidation(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{name: "negative busy timeout", env: map[string]string{"DATABASE_SQLITE_BUSY_TIMEOUT_MS": "-1"}},
		{name: "invalid journal mode", env: map[string]string{"DATABASE_SQLITE_JOURNAL_MODE": "MEMORY"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if _, err := Load(writeConfig(t, sample)); err == nil {
				t.Fatal("expected error, got nil")
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
