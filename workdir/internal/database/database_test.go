package database

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"app/internal/config"
)

func TestOpenInvalidURIDoesNotLeakCredentials(t *testing.T) {
	cfg := config.DatabaseConfig{URI: "postgres://user:s3cret@localhost:notaport/db"}
	_, err := Open(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for invalid URI")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("error leaks credentials: %v", err)
	}
}

func TestOpenUnsupportedDriver(t *testing.T) {
	cfg := config.DatabaseConfig{URI: "mysql://user:pass@localhost:3306/db"}
	_, err := Open(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for unsupported driver")
	}
}

func TestOpenSQLiteMemory(t *testing.T) {
	cfg := config.DatabaseConfig{URI: "sqlite::memory:"}
	d, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer d.Close()
	if d.Driver != config.DriverSQLite {
		t.Fatalf("Driver = %q, want %q", d.Driver, config.DriverSQLite)
	}
}

func TestOpenSQLiteCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nested", "app.db")
	cfg := config.DatabaseConfig{URI: "sqlite:" + dbPath}

	d, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer d.Close()

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected database file to be created: %v", err)
	}
}

func TestOpenSQLiteUnwritableDirFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission checks do not apply")
	}

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o750) })

	dbPath := filepath.Join(dir, "nested", "app.db")
	cfg := config.DatabaseConfig{URI: "sqlite:" + dbPath}

	_, err := Open(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for unwritable parent directory")
	}
}
