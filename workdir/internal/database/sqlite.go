package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite"

	"app/internal/config"
)

// sqliteMemoryDSN is the path openSQLite recognizes as "in-memory": every
// connection to it opens its own empty database, so WAL journaling makes no
// sense and the pool must be capped at one connection.
const sqliteMemoryDSN = ":memory:"

// sqliteBusyTimeout is the default SQLITE_BUSY wait applied when
// sqliteOptions.BusyTimeout is zero.
const sqliteBusyTimeout = 5 * time.Second

// sqliteMaxOpenConns caps the connection pool for file-backed databases.
const sqliteMaxOpenConns = 4

// sqliteOptions tunes openSQLite. The zero value is ready to use.
type sqliteOptions struct {
	// BusyTimeout overrides sqliteBusyTimeout when non-zero.
	BusyTimeout time.Duration
}

// openSQLite opens the SQLite database at path (a file path, or ":memory:")
// and verifies it is reachable.
func openSQLite(ctx context.Context, path string, opts sqliteOptions) (*DB, error) {
	busyTimeout := opts.BusyTimeout
	if busyTimeout <= 0 {
		busyTimeout = sqliteBusyTimeout
	}

	sqlDB, err := sql.Open("sqlite", sqliteDSN(path, busyTimeout))
	if err != nil {
		return nil, fmt.Errorf("database: open sqlite %s: %w", path, err)
	}

	if path == sqliteMemoryDSN {
		sqlDB.SetMaxOpenConns(1)
	} else {
		sqlDB.SetMaxOpenConns(sqliteMaxOpenConns)
	}

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("database: ping sqlite %s: %w", path, err)
	}

	return &DB{
		Querier:  newSQLiteQuerier(sqlDB),
		Driver:   config.DriverSQLite,
		close:    func() { sqlDB.Close() },
		logAttrs: []any{"path", path},
	}, nil
}

// sqliteDSN builds the modernc.org/sqlite DSN for path: foreign keys on
// (SQLite leaves them off by default), WAL journaling so readers don't block
// the writer (skipped for in-memory databases, which have no journal file),
// a busy timeout so writers wait instead of failing with SQLITE_BUSY,
// immediate transaction locking to avoid deadlocks when a read transaction
// is upgraded to a write, and a single time.Time text format so ordering on
// timestamp columns stays correct.
func sqliteDSN(path string, busyTimeout time.Duration) string {
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	if path != sqliteMemoryDSN {
		q.Add("_pragma", "journal_mode(WAL)")
	}
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()))
	q.Set("_txlock", "immediate")
	q.Set("_time_format", "sqlite")
	return path + "?" + q.Encode()
}
