package database

import (
	"context"
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
