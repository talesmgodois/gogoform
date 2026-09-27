package database

import (
	"context"
	"database/sql"
	"testing"
	"testing/fstest"

	"app/internal/config"
)

// newMigrateTestDB opens a private SQLite :memory: database with no
// migrations applied, wrapped as a *DB for the migrator.
func newMigrateTestDB(t *testing.T) *DB {
	t.Helper()

	sqlDB, err := sql.Open("sqlite", sqliteDSN(sqliteMemoryDSN, sqliteBusyTimeout, sqliteJournalMode))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })

	return &DB{Driver: config.DriverSQLite, sqlDB: sqlDB}
}

// twoMigrationFixture is a small, valid two-migration set used by most
// tests below.
func twoMigrationFixture() fstest.MapFS {
	return fstest.MapFS{
		"20260101000000_create_widgets.sql": &fstest.MapFile{Data: []byte(
			"-- migrate:up\n" +
				"CREATE TABLE widgets (id INTEGER PRIMARY KEY);\n" +
				"-- migrate:down\n" +
				"DROP TABLE widgets;\n",
		)},
		"20260102000000_create_gadgets.sql": &fstest.MapFile{Data: []byte(
			"-- migrate:up\n" +
				"CREATE TABLE gadgets (id INTEGER PRIMARY KEY);\n" +
				"-- migrate:down\n" +
				"DROP TABLE gadgets;\n",
		)},
	}
}

func TestMigrate_SQLite_AppliesEverythingOnEmptyDB(t *testing.T) {
	d := newMigrateTestDB(t)

	applied, err := migrate(context.Background(), d, twoMigrationFixture())
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	wantVersions := []string{"20260101000000", "20260102000000"}
	assertStringSliceEqual(t, "applied", applied, wantVersions)
	assertSQLiteTableExists(t, d.sqlDB, "widgets")
	assertSQLiteTableExists(t, d.sqlDB, "gadgets")
	assertSchemaMigrationsSQLite(t, d.sqlDB, wantVersions)
}

func TestMigrate_SQLite_NoopOnSecondRun(t *testing.T) {
	d := newMigrateTestDB(t)
	fsys := twoMigrationFixture()

	if _, err := migrate(context.Background(), d, fsys); err != nil {
		t.Fatalf("first migrate: %v", err)
	}

	applied, err := migrate(context.Background(), d, fsys)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("second migrate applied %v, want none", applied)
	}
	assertSchemaMigrationsSQLite(t, d.sqlDB, []string{"20260101000000", "20260102000000"})
}

func TestMigrate_SQLite_AppliesOnlyNewVersions(t *testing.T) {
	d := newMigrateTestDB(t)
	fsys := twoMigrationFixture()

	if err := ensureSchemaMigrationsTableSQLite(context.Background(), d.sqlDB); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if _, err := d.sqlDB.Exec(`INSERT INTO `+migrationsTable+` (version) VALUES (?)`, "20260101000000"); err != nil {
		t.Fatalf("seed applied version: %v", err)
	}

	applied, err := migrate(context.Background(), d, fsys)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	assertStringSliceEqual(t, "applied", applied, []string{"20260102000000"})
	assertSQLiteTableExists(t, d.sqlDB, "gadgets")
	assertSQLiteTableMissing(t, d.sqlDB, "widgets")
	assertSchemaMigrationsSQLite(t, d.sqlDB, []string{"20260101000000", "20260102000000"})
}

func TestMigrate_SQLite_RollsBackBrokenMigration(t *testing.T) {
	d := newMigrateTestDB(t)
	fsys := fstest.MapFS{
		"20260101000000_create_widgets.sql": &fstest.MapFile{Data: []byte(
			"-- migrate:up\n" +
				"CREATE TABLE widgets (id INTEGER PRIMARY KEY);\n" +
				"-- migrate:down\n" +
				"DROP TABLE widgets;\n",
		)},
		"20260102000000_broken.sql": &fstest.MapFile{Data: []byte(
			"-- migrate:up\n" +
				"CREATE TABLE gadgets (id INTEGER PRIMARY KEY);\n" +
				"THIS IS NOT VALID SQL;\n" +
				"-- migrate:down\n" +
				"DROP TABLE gadgets;\n",
		)},
	}

	applied, err := migrate(context.Background(), d, fsys)
	if err == nil {
		t.Fatal("migrate: expected error from broken migration")
	}
	if len(applied) != 1 || applied[0] != "20260101000000" {
		t.Fatalf("applied = %v, want only the good migration reported before the failure", applied)
	}

	assertSQLiteTableExists(t, d.sqlDB, "widgets")
	assertSQLiteTableMissing(t, d.sqlDB, "gadgets")
	assertSchemaMigrationsSQLite(t, d.sqlDB, []string{"20260101000000"})
}

func assertStringSliceEqual(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}
}

func assertSQLiteTableExists(t *testing.T, sqlDB *sql.DB, name string) {
	t.Helper()
	if !sqliteTableExists(t, sqlDB, name) {
		t.Fatalf("expected table %s to exist", name)
	}
}

func assertSQLiteTableMissing(t *testing.T, sqlDB *sql.DB, name string) {
	t.Helper()
	if sqliteTableExists(t, sqlDB, name) {
		t.Fatalf("expected table %s to not exist", name)
	}
}

func sqliteTableExists(t *testing.T, sqlDB *sql.DB, name string) bool {
	t.Helper()
	var count int
	if err := sqlDB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count); err != nil {
		t.Fatalf("check table %s: %v", name, err)
	}
	return count > 0
}

func assertSchemaMigrationsSQLite(t *testing.T, sqlDB *sql.DB, want []string) {
	t.Helper()

	rows, err := sqlDB.Query(`SELECT version FROM ` + migrationsTable + ` ORDER BY version`)
	if err != nil {
		t.Fatalf("query %s: %v", migrationsTable, err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan %s: %v", migrationsTable, err)
		}
		got = append(got, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s: %v", migrationsTable, err)
	}

	assertStringSliceEqual(t, migrationsTable, got, want)
}
