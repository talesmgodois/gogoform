package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeDotEnv(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// unsetEnv clears key for the test and restores its previous value afterwards.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	os.Unsetenv(key)
}

func TestLoadDotEnvOverridesFile(t *testing.T) {
	unsetEnv(t, "SERVER_PORT")
	unsetEnv(t, "LOG_LEVEL")
	path := writeDotEnv(t, "# comment\n\nSERVER_PORT=8081\nexport LOG_LEVEL=\"error\"\n")

	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	cfg, err := Load(writeConfig(t, sample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 8081 || cfg.Logger.Level != "error" {
		t.Fatalf("dotenv values not applied: %+v", cfg)
	}
}

func TestLoadDotEnvKeepsExistingEnv(t *testing.T) {
	t.Setenv("SERVER_PORT", "9090")

	if err := LoadDotEnv(writeDotEnv(t, "SERVER_PORT=8081\n")); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	if got := os.Getenv("SERVER_PORT"); got != "9090" {
		t.Fatalf("SERVER_PORT = %q, want existing value 9090", got)
	}
}

func TestLoadDotEnvMissingFile(t *testing.T) {
	if err := LoadDotEnv(filepath.Join(t.TempDir(), ".env")); err != nil {
		t.Fatalf("missing file should be ignored, got %v", err)
	}
}

func TestLoadDotEnvInvalidLine(t *testing.T) {
	if err := LoadDotEnv(writeDotEnv(t, "NOT_A_PAIR\n")); err == nil {
		t.Fatal("expected error for line without '='")
	}
}
