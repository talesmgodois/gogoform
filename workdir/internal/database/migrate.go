// Package database migrator applies dbmate-format SQL migrations directly
// from the embedded db.PostgresMigrations/db.SQLiteMigrations filesystems,
// so the application binary needs no external migration tool. It shares
// the schema_migrations bookkeeping table and version format with the
// dbmate CLI, so the two can be mixed: migrations applied by one show up as
// already applied to the other.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"app/db"
	"app/internal/config"
)

// migrationsTable is dbmate's own bookkeeping table name.
const migrationsTable = "schema_migrations"

// advisoryLockKey is the pg_advisory_lock key Migrate holds for its
// duration on PostgreSQL, so several replicas starting at once apply
// migrations one at a time instead of racing. Its value is arbitrary; it
// only needs to be a constant unique to this application's migrator.
const advisoryLockKey int64 = 8_378_411_923_579



// migration is one parsed dbmate migration file.
type migration struct {
	// version is the numeric filename prefix, dbmate's identifier for the
	// migration (e.g. "20260924013247").
	version string
	// name is the migration's filename, used only for error messages.
	name string
	// upSQL is the "-- migrate:up" block, up to "-- migrate:down".
	upSQL string
	// transaction is false when the up block declares
	// "-- migrate:up transaction:false", meaning it must run outside a
	// transaction (e.g. statements PostgreSQL rejects inside one).
	transaction bool
}

// Migrate applies every pending migration for d.Driver, in version order,
// and returns the versions it applied. It does not implement down
// migrations; use the dbmate CLI for those.
func Migrate(ctx context.Context, d *DB) ([]string, error) {
	switch d.Driver {
	case config.DriverSQLite:
		fsys, err := fs.Sub(db.SQLiteMigrations, "sqlite/migrations")
		if err != nil {
			return nil, fmt.Errorf("database: sqlite migrations: %w", err)
		}
		return migrate(ctx, d, fsys)
	case config.DriverPostgres:
		fsys, err := fs.Sub(db.PostgresMigrations, "postgres/migrations")
		if err != nil {
			return nil, fmt.Errorf("database: postgres migrations: %w", err)
		}
		return migrate(ctx, d, fsys)
	default:
		return nil, fmt.Errorf("database: unsupported driver %q", d.Driver)
	}
}

// migrate applies every pending migration found in fsys against d. It is
// the driver-dispatching core Migrate calls with the real embedded
// filesystem; tests substitute fsys with an in-memory fixture instead.
func migrate(ctx context.Context, d *DB, fsys fs.FS) ([]string, error) {
	migrations, err := loadMigrations(fsys)
	if err != nil {
		return nil, err
	}

	switch d.Driver {
	case config.DriverSQLite:
		return migrateSQLite(ctx, d.sqlDB, migrations)
	case config.DriverPostgres:
		return migratePostgres(ctx, d.pgPool, migrations)
	default:
		return nil, fmt.Errorf("database: migrate not implemented for driver %q", d.Driver)
	}
}

// loadMigrations reads every "*.sql" file at the root of fsys, parses it,
// and returns the migrations sorted by version, ascending.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("database: read migrations: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		content, err := fs.ReadFile(fsys, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("database: read migration %s: %w", entry.Name(), err)
		}
		m, err := parseMigration(entry.Name(), content)
		if err != nil {
			return nil, err
		}
		migrations = append(migrations, m)
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	return migrations, nil
}

// parseMigration extracts name's version and "-- migrate:up" block.
func parseMigration(name string, content []byte) (migration, error) {
	version, err := migrationVersion(name)
	if err != nil {
		return migration{}, err
	}

	const upMarker = "-- migrate:up"
	const downMarker = "-- migrate:down"

	text := string(content)
	upIdx := strings.Index(text, upMarker)
	if upIdx < 0 {
		return migration{}, fmt.Errorf("database: migration %s missing %q marker", name, upMarker)
	}
	rest := text[upIdx+len(upMarker):]

	// The rest of the marker's line may carry dbmate options
	// (e.g. "transaction:false"); the SQL body starts on the next line.
	optionsLine := rest
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		optionsLine = rest[:nl]
		rest = rest[nl+1:]
	} else {
		rest = ""
	}
	transactionEnabled := !strings.Contains(optionsLine, "transaction:false")

	if downIdx := strings.Index(rest, downMarker); downIdx >= 0 {
		rest = rest[:downIdx]
	}

	return migration{version: version, name: name, upSQL: rest, transaction: transactionEnabled}, nil
}

// migrationVersion returns name's numeric filename prefix, up to the first
// underscore (dbmate's version format, e.g. "20260924013247" from
// "20260924013247_initial_schema.sql").
func migrationVersion(name string) (string, error) {
	base := strings.TrimSuffix(name, ".sql")
	idx := strings.IndexByte(base, '_')
	if idx <= 0 {
		return "", fmt.Errorf("database: migration filename %q missing a version prefix", name)
	}
	version := base[:idx]
	for _, r := range version {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("database: migration filename %q has a non-numeric version prefix", name)
		}
	}
	return version, nil
}

// migrateSQLite applies every migration in migrations not yet recorded in
// schema_migrations, in order, each in its own transaction alongside the
// insert of its version. Concurrent Migrate calls are serialized by
// sqlDB's _txlock=immediate DSN option rather than an explicit lock.
func migrateSQLite(ctx context.Context, sqlDB *sql.DB, migrations []migration) ([]string, error) {
	if err := ensureSchemaMigrationsTableSQLite(ctx, sqlDB); err != nil {
		return nil, err
	}

	applied, err := appliedVersionsSQLite(ctx, sqlDB)
	if err != nil {
		return nil, err
	}

	var newlyApplied []string
	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		if err := applySQLiteMigration(ctx, sqlDB, m); err != nil {
			return newlyApplied, fmt.Errorf("database: apply migration %s: %w", m.name, err)
		}
		newlyApplied = append(newlyApplied, m.version)
	}
	return newlyApplied, nil
}

// ensureSchemaMigrationsTableSQLite creates dbmate's bookkeeping table if it
// doesn't already exist.
func ensureSchemaMigrationsTableSQLite(ctx context.Context, sqlDB *sql.DB) error {
	_, err := sqlDB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+migrationsTable+` (version varchar(128) PRIMARY KEY)`)
	if err != nil {
		return fmt.Errorf("database: create %s table: %w", migrationsTable, err)
	}
	return nil
}

// appliedVersionsSQLite returns the set of versions already recorded in
// schema_migrations.
func appliedVersionsSQLite(ctx context.Context, sqlDB *sql.DB) (map[string]bool, error) {
	rows, err := sqlDB.QueryContext(ctx, `SELECT version FROM `+migrationsTable)
	if err != nil {
		return nil, fmt.Errorf("database: read %s: %w", migrationsTable, err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("database: scan %s: %w", migrationsTable, err)
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

// applySQLiteMigration runs m's up SQL and records its version. Both run in
// one transaction unless m declares transaction:false, in which case both
// run directly on sqlDB instead.
func applySQLiteMigration(ctx context.Context, sqlDB *sql.DB, m migration) error {
	if !m.transaction {
		if _, err := sqlDB.ExecContext(ctx, m.upSQL); err != nil {
			return fmt.Errorf("run migration sql: %w", err)
		}
		if _, err := sqlDB.ExecContext(ctx, `INSERT INTO `+migrationsTable+` (version) VALUES (?)`, m.version); err != nil {
			return fmt.Errorf("record migration version: %w", err)
		}
		return nil
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, m.upSQL); err != nil {
		return fmt.Errorf("run migration sql: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+migrationsTable+` (version) VALUES (?)`, m.version); err != nil {
		return fmt.Errorf("record migration version: %w", err)
	}
	return tx.Commit()
}

// migratePostgres applies every migration in migrations not yet recorded in
// schema_migrations, in order, each in its own transaction alongside the
// insert of its version (unless it declares transaction:false). A
// pg_advisory_lock held on a dedicated connection for the whole call
// serializes concurrent Migrate calls against the same database, so several
// replicas starting at once apply each migration exactly once.
func migratePostgres(ctx context.Context, pool *pgxpool.Pool, migrations []migration) ([]string, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("database: acquire migration lock connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockKey); err != nil {
		return nil, fmt.Errorf("database: acquire advisory lock: %w", err)
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryLockKey)

	if err := ensureSchemaMigrationsTablePostgres(ctx, pool); err != nil {
		return nil, err
	}

	applied, err := appliedVersionsPostgres(ctx, pool)
	if err != nil {
		return nil, err
	}

	var newlyApplied []string
	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		if err := applyPostgresMigration(ctx, pool, m); err != nil {
			return newlyApplied, fmt.Errorf("database: apply migration %s: %w", m.name, err)
		}
		newlyApplied = append(newlyApplied, m.version)
	}
	return newlyApplied, nil
}

// ensureSchemaMigrationsTablePostgres creates dbmate's bookkeeping table if
// it doesn't already exist.
func ensureSchemaMigrationsTablePostgres(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+migrationsTable+` (version varchar(128) PRIMARY KEY)`)
	if err != nil {
		return fmt.Errorf("database: create %s table: %w", migrationsTable, err)
	}
	return nil
}

// appliedVersionsPostgres returns the set of versions already recorded in
// schema_migrations.
func appliedVersionsPostgres(ctx context.Context, pool *pgxpool.Pool) (map[string]bool, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM `+migrationsTable)
	if err != nil {
		return nil, fmt.Errorf("database: read %s: %w", migrationsTable, err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("database: scan %s: %w", migrationsTable, err)
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

// applyPostgresMigration runs m's up SQL and records its version. Both run
// in one transaction unless m declares transaction:false, in which case
// both run directly on pool instead (dbmate's escape hatch for statements
// PostgreSQL rejects inside a transaction, e.g. CREATE INDEX CONCURRENTLY).
func applyPostgresMigration(ctx context.Context, pool *pgxpool.Pool, m migration) error {
	if !m.transaction {
		if _, err := pool.Exec(ctx, m.upSQL); err != nil {
			return fmt.Errorf("run migration sql: %w", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO `+migrationsTable+` (version) VALUES ($1)`, m.version); err != nil {
			return fmt.Errorf("record migration version: %w", err)
		}
		return nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, m.upSQL); err != nil {
		return fmt.Errorf("run migration sql: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO `+migrationsTable+` (version) VALUES ($1)`, m.version); err != nil {
		return fmt.Errorf("record migration version: %w", err)
	}
	return tx.Commit(ctx)
}
