package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	dbfiles "app/db"
	"app/internal/config"
)

// Migrate applies the pending embedded migrations of d.Driver and returns
// the versions it applied, in order. It is compatible with the dbmate CLI:
// applied versions are recorded in dbmate's schema_migrations table, using
// the numeric prefix of the migration file name, so the app and the CLI can
// be used on the same database. Only "-- migrate:up" blocks are run; rolling
// back stays the CLI's job.
//
// Each migration and the insert of its version run in one transaction,
// unless the migration opts out with "-- migrate:up transaction:false". On
// PostgreSQL a session advisory lock is held for the whole run, so replicas
// starting at once apply each migration once; on SQLite the immediate
// transaction locking of openSQLite serializes writers instead.
func Migrate(ctx context.Context, d *DB) ([]string, error) {
	fsys, err := dbfiles.Migrations(string(d.Driver))
	if err != nil {
		return nil, fmt.Errorf("database: migrate: %w", err)
	}
	return migrate(ctx, d, fsys)
}

// migrate is Migrate with the migrations read from fsys, whose top-level
// *.sql files are the dbmate migrations.
func migrate(ctx context.Context, d *DB, fsys fs.FS) ([]string, error) {
	migrations, err := loadMigrations(fsys)
	if err != nil {
		return nil, fmt.Errorf("database: migrate: %w", err)
	}

	var applied []string
	switch {
	case d.Driver == config.DriverPostgres && d.pool != nil:
		applied, err = migratePostgres(ctx, d.pool, migrations)
	case d.Driver == config.DriverSQLite && d.sqlDB != nil:
		applied, err = migrateSQLite(ctx, d.sqlDB, migrations)
	default:
		return nil, fmt.Errorf("database: migrate: unsupported driver %q", d.Driver)
	}
	if err != nil {
		return applied, fmt.Errorf("database: migrate: %w", err)
	}
	return applied, nil
}

// migration is the "-- migrate:up" block of one dbmate migration file.
type migration struct {
	version string
	name    string
	up      string
	// transaction is false when the file opts out with
	// "-- migrate:up transaction:false".
	transaction bool
}

// migrationFileRe matches dbmate migration file names; the numeric prefix
// is the version. Other files are ignored, as dbmate does.
var migrationFileRe = regexp.MustCompile(`^(\d+)_.*\.sql$`)

// upMarkerRe and downMarkerRe match dbmate's block markers. The rest of the
// up marker's line holds its options.
var (
	upMarkerRe   = regexp.MustCompile(`(?m)^--\s*migrate:up(.*)$`)
	downMarkerRe = regexp.MustCompile(`(?m)^--\s*migrate:down.*$`)
)

// loadMigrations reads and parses every migration file at fsys's top
// level, sorted by file name like dbmate.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	var migrations []migration
	seen := map[string]string{}
	for _, e := range entries {
		match := migrationFileRe.FindStringSubmatch(e.Name())
		if e.IsDir() || match == nil {
			continue
		}
		version := match[1]
		if other, ok := seen[version]; ok {
			return nil, fmt.Errorf("migrations %s and %s share version %s", other, e.Name(), version)
		}
		seen[version] = e.Name()

		data, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		up, transaction, err := parseMigration(string(data))
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", e.Name(), err)
		}
		migrations = append(migrations, migration{version: version, name: e.Name(), up: up, transaction: transaction})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].name < migrations[j].name })
	return migrations, nil
}

// parseMigration returns the "-- migrate:up" block of a dbmate migration
// (up to "-- migrate:down", if any) and whether it runs in a transaction.
func parseMigration(content string) (up string, transaction bool, err error) {
	loc := upMarkerRe.FindStringSubmatchIndex(content)
	if loc == nil {
		return "", false, errors.New(`missing "-- migrate:up" marker`)
	}

	transaction = true
	for _, opt := range strings.Fields(content[loc[2]:loc[3]]) {
		key, value, ok := strings.Cut(opt, ":")
		if !ok || key != "transaction" || (value != "true" && value != "false") {
			return "", false, fmt.Errorf("unsupported migrate:up option %q", opt)
		}
		transaction = value == "true"
	}

	up = content[loc[1]:]
	if down := downMarkerRe.FindStringIndex(up); down != nil {
		up = up[:down[0]]
	}
	return up, transaction, nil
}

// createSchemaMigrations creates dbmate's bookkeeping table, with dbmate's
// own definition, when it is missing.
const createSchemaMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (version varchar(128) PRIMARY KEY)`

// migrateAdvisoryLockKey identifies the PostgreSQL advisory lock held while
// migrating. Any constant works as long as every replica uses the same one.
const migrateAdvisoryLockKey int64 = 0x676f676f666f726d // "gogoform"

// migratePostgres applies the pending migrations on one pooled connection
// holding a session advisory lock, so concurrent callers take turns.
func migratePostgres(ctx context.Context, pool *pgxpool.Pool, migrations []migration) ([]string, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrateAdvisoryLockKey); err != nil {
		return nil, fmt.Errorf("take advisory lock: %w", err)
	}
	defer func() {
		if _, unlockErr := conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrateAdvisoryLockKey); unlockErr != nil {
			// Closing the connection releases the lock; the pool then discards it.
			conn.Conn().Close(context.Background())
		}
	}()

	if _, err := conn.Exec(ctx, createSchemaMigrations); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	done := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read schema_migrations: %w", err)
		}
		done[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}

	const record = `INSERT INTO schema_migrations (version) VALUES ($1)`
	var applied []string
	for _, m := range migrations {
		if done[m.version] {
			continue
		}
		if m.transaction {
			tx, err := conn.Begin(ctx)
			if err != nil {
				return applied, fmt.Errorf("apply %s: begin: %w", m.name, err)
			}
			if _, err := tx.Exec(ctx, m.up); err != nil {
				tx.Rollback(ctx)
				return applied, fmt.Errorf("apply %s: %w", m.name, err)
			}
			if _, err := tx.Exec(ctx, record, m.version); err != nil {
				tx.Rollback(ctx)
				return applied, fmt.Errorf("apply %s: record version: %w", m.name, err)
			}
			if err := tx.Commit(ctx); err != nil {
				return applied, fmt.Errorf("apply %s: commit: %w", m.name, err)
			}
		} else {
			if _, err := conn.Exec(ctx, m.up); err != nil {
				return applied, fmt.Errorf("apply %s: %w", m.name, err)
			}
			if _, err := conn.Exec(ctx, record, m.version); err != nil {
				return applied, fmt.Errorf("apply %s: record version: %w", m.name, err)
			}
		}
		applied = append(applied, m.version)
	}
	return applied, nil
}

// migrateSQLite applies the pending migrations. Each transaction takes the
// write lock when it begins (_txlock=immediate), and the version is checked
// again inside it, so another process migrating the same file can't make a
// migration run twice.
func migrateSQLite(ctx context.Context, sqlDB *sql.DB, migrations []migration) ([]string, error) {
	if _, err := sqlDB.ExecContext(ctx, createSchemaMigrations); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	var applied []string
	for _, m := range migrations {
		ran, err := sqliteApplyMigration(ctx, sqlDB, m)
		if err != nil {
			return applied, fmt.Errorf("apply %s: %w", m.name, err)
		}
		if ran {
			applied = append(applied, m.version)
		}
	}
	return applied, nil
}

// sqliteApplyMigration applies m unless its version is already recorded,
// and reports whether it ran.
func sqliteApplyMigration(ctx context.Context, sqlDB *sql.DB, m migration) (bool, error) {
	const (
		check  = `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`
		record = `INSERT INTO schema_migrations (version) VALUES (?)`
	)
	if !m.transaction {
		var n int
		if err := sqlDB.QueryRowContext(ctx, check, m.version).Scan(&n); err != nil {
			return false, fmt.Errorf("read schema_migrations: %w", err)
		}
		if n > 0 {
			return false, nil
		}
		if _, err := sqlDB.ExecContext(ctx, m.up); err != nil {
			return false, err
		}
		if _, err := sqlDB.ExecContext(ctx, record, m.version); err != nil {
			return false, fmt.Errorf("record version: %w", err)
		}
		return true, nil
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	var n int
	if err := tx.QueryRowContext(ctx, check, m.version).Scan(&n); err != nil {
		return false, fmt.Errorf("read schema_migrations: %w", err)
	}
	if n > 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, m.up); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, record, m.version); err != nil {
		return false, fmt.Errorf("record version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}
