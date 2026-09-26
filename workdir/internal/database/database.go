// Package database opens the connection to the application's database,
// abstracting over the concrete engine (PostgreSQL, and later others).
package database

import (
	"context"
	"fmt"
	"time"

	"app/internal/config"
	"app/internal/db"
)

// DB is an open, engine-neutral database connection.
type DB struct {
	// Querier runs the generated SQL queries.
	Querier db.Querier
	// Driver is the engine backing Querier.
	Driver config.Driver

	close    func()
	logAttrs []any
}

// Open connects to the database identified by cfg and verifies it is
// reachable. The caller must call Close on the returned DB.
func Open(ctx context.Context, cfg config.DatabaseConfig) (*DB, error) {
	switch driver := cfg.Driver(); driver {
	case config.DriverPostgres:
		return openPostgres(ctx, cfg.URI)
	case config.DriverSQLite:
		opts := sqliteOptions{
			BusyTimeout: time.Duration(cfg.SQLiteBusyTimeoutMS) * time.Millisecond,
			JournalMode: cfg.SQLiteJournalMode,
		}
		return openSQLite(ctx, cfg.SQLitePath(), opts)
	default:
		return nil, fmt.Errorf("database: unsupported driver %q", driver)
	}
}

// Close releases the underlying connection(s).
func (d *DB) Close() {
	if d.close != nil {
		d.close()
	}
}

// LogAttrs returns slog-style key/value pairs identifying the connected
// database (host, port, database name), safe to log: never credentials.
func (d *DB) LogAttrs() []any {
	return d.logAttrs
}
