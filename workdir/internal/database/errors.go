package database

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"modernc.org/sqlite"
)

// ErrNoRows is the engine-neutral sentinel for "query returned no rows".
var ErrNoRows = errors.New("database: no rows in result set")

// IsNoRows reports whether err represents a "no rows" result, regardless of
// which engine produced it.
func IsNoRows(err error) bool {
	return errors.Is(err, ErrNoRows) || errors.Is(err, pgx.ErrNoRows)
}

// PostgreSQL SQLSTATE codes the application reacts to.
const (
	sqlStateForeignKeyViolation = "23503"
	sqlStateUniqueViolation     = "23505"
	sqlStateCheckViolation      = "23514"
)

// modernc.org/sqlite extended result codes the application reacts to.
const (
	sqliteExtendedUnique     = 2067 // SQLITE_CONSTRAINT_UNIQUE
	sqliteExtendedPrimaryKey = 1555 // SQLITE_CONSTRAINT_PRIMARYKEY
	sqliteExtendedForeignKey = 787  // SQLITE_CONSTRAINT_FOREIGNKEY
	sqliteExtendedCheck      = 275  // SQLITE_CONSTRAINT_CHECK
)

// IsUniqueViolation reports whether err is a PostgreSQL unique constraint
// violation or a SQLite UNIQUE/PRIMARY KEY constraint violation.
func IsUniqueViolation(err error) bool {
	return hasSQLState(err, sqlStateUniqueViolation) ||
		hasSQLiteCode(err, sqliteExtendedUnique) ||
		hasSQLiteCode(err, sqliteExtendedPrimaryKey)
}

// IsForeignKeyViolation reports whether err is a PostgreSQL foreign key
// violation or a SQLite foreign key violation.
func IsForeignKeyViolation(err error) bool {
	return hasSQLState(err, sqlStateForeignKeyViolation) ||
		hasSQLiteCode(err, sqliteExtendedForeignKey)
}

// IsCheckViolation reports whether err is a PostgreSQL check constraint
// violation or a SQLite check constraint violation.
func IsCheckViolation(err error) bool {
	return hasSQLState(err, sqlStateCheckViolation) ||
		hasSQLiteCode(err, sqliteExtendedCheck)
}

func hasSQLState(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

func hasSQLiteCode(err error, code int) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == code
}
