package database

import (
	"context"
	"strings"
	"testing"
)

func TestConnectInvalidURIDoesNotLeakCredentials(t *testing.T) {
	_, err := Connect(context.Background(), "postgres://user:s3cret@localhost:notaport/db")
	if err == nil {
		t.Fatal("expected error for invalid URI")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("error leaks credentials: %v", err)
	}
}
