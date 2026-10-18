package database

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPgErrorClassification(t *testing.T) {
	unique := &pgconn.PgError{Code: sqlStateUniqueViolation}
	fk := &pgconn.PgError{Code: sqlStateForeignKeyViolation}

	tests := []struct {
		name       string
		err        error
		wantUnique bool
		wantFK     bool
	}{
		{"nil", nil, false, false},
		{"plain error", errors.New("boom"), false, false},
		{"other sqlstate", &pgconn.PgError{Code: "23502"}, false, false},
		{"unique", unique, true, false},
		{"wrapped unique", fmt.Errorf("insert: %w", unique), true, false},
		{"foreign key", fk, false, true},
		{"wrapped foreign key", fmt.Errorf("insert: %w", fk), false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsUniqueViolation(tt.err); got != tt.wantUnique {
				t.Fatalf("IsUniqueViolation = %v, want %v", got, tt.wantUnique)
			}
			if got := IsForeignKeyViolation(tt.err); got != tt.wantFK {
				t.Fatalf("IsForeignKeyViolation = %v, want %v", got, tt.wantFK)
			}
		})
	}
}

func TestIsCheckViolation(t *testing.T) {
	check := &pgconn.PgError{Code: sqlStateCheckViolation}
	if !IsCheckViolation(check) || !IsCheckViolation(fmt.Errorf("insert: %w", check)) {
		t.Fatal("IsCheckViolation = false for a check violation")
	}
	if IsCheckViolation(&pgconn.PgError{Code: sqlStateUniqueViolation}) || IsCheckViolation(nil) {
		t.Fatal("IsCheckViolation = true for another error")
	}
}

func TestIsUniqueViolationOf(t *testing.T) {
	owner := &pgconn.PgError{Code: sqlStateUniqueViolation, ConstraintName: "owner_key"}
	if !IsUniqueViolationOf(owner, "owner_key") || !IsUniqueViolationOf(fmt.Errorf("insert: %w", owner), "owner_key") {
		t.Fatal("IsUniqueViolationOf = false for a violation of the constraint")
	}
	if IsUniqueViolationOf(owner, "other_key") || IsUniqueViolationOf(nil, "owner_key") ||
		IsUniqueViolationOf(&pgconn.PgError{Code: sqlStateCheckViolation, ConstraintName: "owner_key"}, "owner_key") {
		t.Fatal("IsUniqueViolationOf = true for another error")
	}
}
