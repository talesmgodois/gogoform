// Package database schema parity test applies both migration sets to fresh
// databases and compares, per table, the set of column names and their
// nullability, so the PostgreSQL and SQLite schemas can't silently drift
// apart. It requires TEST_DATABASE_URL, like TestQuerierContract_Postgres.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// schemaMigrationsTable is dbmate's own bookkeeping table. It exists in both
// engines but records applied migration versions, not application schema, so
// it is excluded from the comparison.
const schemaMigrationsTable = "schema_migrations"

// tableSchema maps table name to its columns, keyed by column name so the
// two engines can be diffed regardless of column order. The value is
// whether the column accepts NULL.
type tableSchema map[string]map[string]bool

// TestSchemaParity applies db/postgres/migrations and db/sqlite/migrations
// to fresh databases and checks that every table has the same columns, with
// the same nullability, on both engines.
func TestSchemaParity(t *testing.T) {
	if os.Getenv(testDatabaseURLEnv) == "" {
		if os.Getenv(requirePostgresEnv) == "1" {
			t.Fatalf("%s not set but %s=1; schema parity test is required", testDatabaseURLEnv, requirePostgresEnv)
		}
		t.Skipf("%s not set; skipping schema parity test", testDatabaseURLEnv)
	}

	sqliteSchema := sqliteSchemaFor(t)
	postgresSchema := postgresSchemaFor(t)

	assertSchemaParity(t, postgresSchema, sqliteSchema)
}

// sqliteSchemaFor applies db/sqlite/migrations to a private :memory:
// database and reads back its schema via pragma_table_info.
func sqliteSchemaFor(t *testing.T) tableSchema {
	t.Helper()

	sqlDB, err := sql.Open("sqlite", sqliteDSN(sqliteMemoryDSN, sqliteBusyTimeout, sqliteJournalMode))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })

	applySQLiteMigrations(t, sqlDB)

	names, err := sqliteTableNames(sqlDB)
	if err != nil {
		t.Fatalf("list sqlite tables: %v", err)
	}

	schema := make(tableSchema, len(names))
	for _, name := range names {
		cols, err := sqliteTableColumns(sqlDB, name)
		if err != nil {
			t.Fatalf("read sqlite columns for %s: %v", name, err)
		}
		schema[name] = cols
	}
	return schema
}

// sqliteTableNames lists user tables, excluding sqlite's own internal
// tables and dbmate's schemaMigrationsTable.
func sqliteTableNames(sqlDB *sql.DB) ([]string, error) {
	rows, err := sqlDB.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if name == schemaMigrationsTable {
			continue
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// sqliteTableColumns reads table's columns via pragma_table_info, keyed by
// column name. In pragma_table_info, notnull = 1 means the column rejects
// NULL, so Nullable is its negation, except for a single-column INTEGER
// PRIMARY KEY: SQLite reports notnull = 0 for it because the declaration
// doesn't say NOT NULL, but the column is really a rowid alias, so an
// explicit NULL is silently replaced by the next rowid and the column can
// never actually store one. Treating it as non-nullable matches both actual
// behavior and PostgreSQL's IDENTITY PRIMARY KEY.
func sqliteTableColumns(sqlDB *sql.DB, table string) (map[string]bool, error) {
	rows, err := sqlDB.Query(fmt.Sprintf(`SELECT name, type, "notnull", pk FROM pragma_table_info(%q)`, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := make(map[string]bool)
	for rows.Next() {
		var name, colType string
		var notNull, pk int
		if err := rows.Scan(&name, &colType, &notNull, &pk); err != nil {
			return nil, err
		}
		rowidAlias := pk == 1 && strings.EqualFold(colType, "INTEGER")
		cols[name] = notNull == 0 && !rowidAlias
	}
	return cols, rows.Err()
}

// postgresSchemaFor applies db/postgres/migrations to a fresh, private
// schema and reads back its schema via information_schema.columns. The
// schema is dropped when the test finishes.
func postgresSchemaFor(t *testing.T) tableSchema {
	t.Helper()
	ctx := context.Background()
	dbURL := os.Getenv(testDatabaseURLEnv)

	schemaName := pgx.Identifier{fmt.Sprintf("parity_%d", time.Now().UnixNano())}

	setup, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	if _, err := setup.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, schemaName.Sanitize())); err != nil {
		setup.Close(ctx)
		t.Fatalf("create schema %s: %v", schemaName, err)
	}
	setup.Close(ctx)

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		conn, err := pgx.Connect(cleanupCtx, dbURL)
		if err != nil {
			t.Logf("drop schema %s: connect: %v", schemaName, err)
			return
		}
		defer conn.Close(cleanupCtx)
		if _, err := conn.Exec(cleanupCtx, fmt.Sprintf(`DROP SCHEMA %s CASCADE`, schemaName.Sanitize())); err != nil {
			t.Logf("drop schema %s: %v", schemaName, err)
		}
	})

	poolCfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatalf("parse postgres config: %v", err)
	}
	poolCfg.ConnConfig.RuntimeParams["search_path"] = schemaName.Sanitize()

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatalf("open postgres pool: %v", err)
	}
	t.Cleanup(pool.Close)

	applyPostgresMigrations(t, ctx, pool)

	names, err := postgresTableNames(ctx, pool, schemaName.Sanitize())
	if err != nil {
		t.Fatalf("list postgres tables: %v", err)
	}

	result := make(tableSchema, len(names))
	for _, name := range names {
		cols, err := postgresTableColumns(ctx, pool, schemaName.Sanitize(), name)
		if err != nil {
			t.Fatalf("read postgres columns for %s: %v", name, err)
		}
		result[name] = cols
	}
	return result
}

// postgresTableNames lists base tables in schemaName, excluding dbmate's
// schemaMigrationsTable. schemaName is a sanitized identifier, safe to embed
// directly.
func postgresTableNames(ctx context.Context, pool *pgxpool.Pool, schemaName string) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = $1 AND table_type = 'BASE TABLE'`, strings.Trim(schemaName, `"`))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if name == schemaMigrationsTable {
			continue
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// postgresTableColumns reads table's columns via information_schema.columns,
// keyed by column name.
func postgresTableColumns(ctx context.Context, pool *pgxpool.Pool, schemaName, table string) (map[string]bool, error) {
	rows, err := pool.Query(ctx, `
		SELECT column_name, is_nullable FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2`, strings.Trim(schemaName, `"`), table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := make(map[string]bool)
	for rows.Next() {
		var name, isNullable string
		if err := rows.Scan(&name, &isNullable); err != nil {
			return nil, err
		}
		cols[name] = isNullable == "YES"
	}
	return cols, rows.Err()
}

// assertSchemaParity compares postgres and sqlite table-by-table, failing
// with a clear diff of every missing/extra table or column and every
// nullability mismatch.
func assertSchemaParity(t *testing.T, postgres, sqlite tableSchema) {
	t.Helper()

	tableNames := make(map[string]struct{}, len(postgres)+len(sqlite))
	for name := range postgres {
		tableNames[name] = struct{}{}
	}
	for name := range sqlite {
		tableNames[name] = struct{}{}
	}

	sorted := make([]string, 0, len(tableNames))
	for name := range tableNames {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	var diffs []string
	for _, table := range sorted {
		pgCols, inPostgres := postgres[table]
		sqliteCols, inSQLite := sqlite[table]

		switch {
		case !inPostgres:
			diffs = append(diffs, fmt.Sprintf("table %s: only in sqlite", table))
			continue
		case !inSQLite:
			diffs = append(diffs, fmt.Sprintf("table %s: only in postgres", table))
			continue
		}

		if d := diffColumns(table, pgCols, sqliteCols); d != "" {
			diffs = append(diffs, d)
		}
	}

	if len(diffs) > 0 {
		t.Fatalf("schema parity mismatch between db/postgres/migrations and db/sqlite/migrations:\n%s", strings.Join(diffs, "\n"))
	}
}

// diffColumns compares one table's columns between engines and returns a
// human-readable diff, or "" when they match.
func diffColumns(table string, postgres, sqlite map[string]bool) string {
	colNames := make(map[string]struct{}, len(postgres)+len(sqlite))
	for name := range postgres {
		colNames[name] = struct{}{}
	}
	for name := range sqlite {
		colNames[name] = struct{}{}
	}

	sorted := make([]string, 0, len(colNames))
	for name := range colNames {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	var lines []string
	for _, col := range sorted {
		pgNullable, inPostgres := postgres[col]
		sqliteNullable, inSQLite := sqlite[col]

		switch {
		case !inPostgres:
			lines = append(lines, fmt.Sprintf("  %s: only in sqlite", col))
		case !inSQLite:
			lines = append(lines, fmt.Sprintf("  %s: only in postgres", col))
		case pgNullable != sqliteNullable:
			lines = append(lines, fmt.Sprintf("  %s: nullable mismatch (postgres=%t, sqlite=%t)", col, pgNullable, sqliteNullable))
		}
	}

	if len(lines) == 0 {
		return ""
	}
	return fmt.Sprintf("table %s:\n%s", table, strings.Join(lines, "\n"))
}
