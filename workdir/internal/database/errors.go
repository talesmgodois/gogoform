package database

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// IsUniqueViolation reports whether err is a PostgreSQL unique constraint violation.
func IsUniqueViolation(err error) bool {
	return hasSQLState(err, sqlStateUniqueViolation)
}

// IsForeignKeyViolation reports whether err is a PostgreSQL foreign key violation.
func IsForeignKeyViolation(err error) bool {
	return hasSQLState(err, sqlStateForeignKeyViolation)
}

// IsCheckViolation reports whether err is a PostgreSQL check constraint violation.
func IsCheckViolation(err error) bool {
	return hasSQLState(err, sqlStateCheckViolation)
}

func hasSQLState(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
