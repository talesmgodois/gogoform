package database

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL SQLSTATE codes the application reacts to.
const (
	sqlStateForeignKeyViolation = "23503"
	sqlStateUniqueViolation     = "23505"
)

// IsUniqueViolation reports whether err is a PostgreSQL unique constraint violation.
func IsUniqueViolation(err error) bool {
	return hasSQLState(err, sqlStateUniqueViolation)
}

// IsForeignKeyViolation reports whether err is a PostgreSQL foreign key violation.
func IsForeignKeyViolation(err error) bool {
	return hasSQLState(err, sqlStateForeignKeyViolation)
}

func hasSQLState(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
