package database

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	_ "modernc.org/sqlite"
)

func TestIsNoRows(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("boom"), false},
		{"pgx.ErrNoRows", pgx.ErrNoRows, true},
		{"wrapped pgx.ErrNoRows", fmt.Errorf("query: %w", pgx.ErrNoRows), true},
		{"database.ErrNoRows", ErrNoRows, true},
		{"wrapped database.ErrNoRows", fmt.Errorf("query: %w", ErrNoRows), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNoRows(tt.err); got != tt.want {
				t.Fatalf("IsNoRows(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

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

// openSQLiteViolationsDB builds an in-memory SQLite database with a UNIQUE,
// a PRIMARY KEY, a FOREIGN KEY and a CHECK constraint so the tests can
// trigger real *sqlite.Error values instead of hand-rolling them (its code
// and msg fields are unexported).
func openSQLiteViolationsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE parent (id INTEGER PRIMARY KEY, name TEXT UNIQUE)`,
		`CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES parent(id), val INTEGER CHECK (val > 0))`,
		`INSERT INTO parent (id, name) VALUES (1, 'a')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}
	return db
}

func TestSqliteErrorClassification(t *testing.T) {
	db := openSQLiteViolationsDB(t)

	_, uniqueErr := db.Exec(`INSERT INTO parent (id, name) VALUES (2, 'a')`)
	_, pkErr := db.Exec(`INSERT INTO parent (id, name) VALUES (1, 'b')`)
	_, fkErr := db.Exec(`INSERT INTO child (id, parent_id, val) VALUES (1, 999, 1)`)
	_, checkErr := db.Exec(`INSERT INTO child (id, parent_id, val) VALUES (2, 1, -1)`)

	for _, err := range []error{uniqueErr, pkErr, fkErr, checkErr} {
		if err == nil {
			t.Fatalf("expected a constraint violation error, got nil")
		}
	}

	tests := []struct {
		name       string
		err        error
		wantUnique bool
		wantFK     bool
		wantCheck  bool
	}{
		{"nil", nil, false, false, false},
		{"plain error", errors.New("boom"), false, false, false},
		{"unique", uniqueErr, true, false, false},
		{"wrapped unique", fmt.Errorf("insert: %w", uniqueErr), true, false, false},
		{"primary key", pkErr, true, false, false},
		{"wrapped primary key", fmt.Errorf("insert: %w", pkErr), true, false, false},
		{"foreign key", fkErr, false, true, false},
		{"wrapped foreign key", fmt.Errorf("insert: %w", fkErr), false, true, false},
		{"check", checkErr, false, false, true},
		{"wrapped check", fmt.Errorf("insert: %w", checkErr), false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsUniqueViolation(tt.err); got != tt.wantUnique {
				t.Fatalf("IsUniqueViolation = %v, want %v", got, tt.wantUnique)
			}
			if got := IsForeignKeyViolation(tt.err); got != tt.wantFK {
				t.Fatalf("IsForeignKeyViolation = %v, want %v", got, tt.wantFK)
			}
			if got := IsCheckViolation(tt.err); got != tt.wantCheck {
				t.Fatalf("IsCheckViolation = %v, want %v", got, tt.wantCheck)
			}
		})
	}
}
