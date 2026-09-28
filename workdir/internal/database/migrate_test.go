package database

import (
	"context"
	"os"
	"reflect"
	"sync"
	"testing"
	"testing/fstest"

	dbfiles "app/db"
	"app/internal/config"
)

// migrationFS builds an in-memory migrations directory from name -> up SQL,
// wrapping each body in dbmate's markers.
func migrationFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, up := range files {
		fsys[name] = &fstest.MapFile{Data: []byte("-- migrate:up\n" + up + "\n\n-- migrate:down\nSELECT 1;\n")}
	}
	return fsys
}

// newSQLiteMemoryDB opens a private, empty SQLite :memory: database.
func newSQLiteMemoryDB(t *testing.T) *DB {
	t.Helper()
	d, err := openSQLite(context.Background(), sqliteMemoryDSN, sqliteOptions{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(d.Close)
	return d
}

// embeddedVersions returns the versions of engine's embedded migrations.
func embeddedVersions(t *testing.T, engine string) []string {
	t.Helper()
	fsys, err := dbfiles.Migrations(engine)
	if err != nil {
		t.Fatalf("embedded migrations: %v", err)
	}
	migrations, err := loadMigrations(fsys)
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatalf("no embedded %s migrations", engine)
	}
	versions := make([]string, len(migrations))
	for i, m := range migrations {
		versions[i] = m.version
	}
	return versions
}

// recordedVersions reads schema_migrations, ordered by version.
func recordedVersions(t *testing.T, d *DB) []string {
	t.Helper()
	ctx := context.Background()
	const q = `SELECT version FROM schema_migrations ORDER BY version`
	var versions []string
	switch d.Driver {
	case config.DriverSQLite:
		rows, err := d.sqlDB.QueryContext(ctx, q)
		if err != nil {
			t.Fatalf("read schema_migrations: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				t.Fatalf("scan version: %v", err)
			}
			versions = append(versions, v)
		}
	case config.DriverPostgres:
		rows, err := d.pool.Query(ctx, q)
		if err != nil {
			t.Fatalf("read schema_migrations: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				t.Fatalf("scan version: %v", err)
			}
			versions = append(versions, v)
		}
	}
	return versions
}

// sqliteTableExists reports whether table exists in d.
func sqliteTableExists(t *testing.T, d *DB, table string) bool {
	t.Helper()
	var n int
	err := d.sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n)
	if err != nil {
		t.Fatalf("look up table %s: %v", table, err)
	}
	return n > 0
}

func TestParseMigration(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		up          string
		transaction bool
		wantErr     bool
	}{
		{
			name:        "up and down",
			content:     "-- migrate:up\nCREATE TABLE a (id int);\n-- migrate:down\nDROP TABLE a;\n",
			up:          "\nCREATE TABLE a (id int);\n",
			transaction: true,
		},
		{
			name:        "up only",
			content:     "-- migrate:up\nCREATE TABLE a (id int);\n",
			up:          "\nCREATE TABLE a (id int);\n",
			transaction: true,
		},
		{
			name:        "transaction false",
			content:     "-- migrate:up transaction:false\nCREATE INDEX CONCURRENTLY i ON a (id);\n-- migrate:down\n",
			up:          "\nCREATE INDEX CONCURRENTLY i ON a (id);\n",
			transaction: false,
		},
		{
			name:        "explicit transaction true",
			content:     "-- migrate:up transaction:true\nSELECT 1;\n",
			up:          "\nSELECT 1;\n",
			transaction: true,
		},
		{
			name:    "missing up marker",
			content: "CREATE TABLE a (id int);\n-- migrate:down\n",
			wantErr: true,
		},
		{
			name:    "unknown option",
			content: "-- migrate:up foo:bar\nSELECT 1;\n",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			up, transaction, err := parseMigration(tt.content)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMigration: %v", err)
			}
			if up != tt.up {
				t.Errorf("up = %q, want %q", up, tt.up)
			}
			if transaction != tt.transaction {
				t.Errorf("transaction = %v, want %v", transaction, tt.transaction)
			}
		})
	}
}

func TestLoadMigrations(t *testing.T) {
	fsys := migrationFS(map[string]string{
		"002_b.sql": "SELECT 2;",
		"001_a.sql": "SELECT 1;",
	})
	fsys["README.md"] = &fstest.MapFile{Data: []byte("not a migration")}
	fsys["no_version.sql"] = &fstest.MapFile{Data: []byte("-- migrate:up\n")}

	migrations, err := loadMigrations(fsys)
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	var got []string
	for _, m := range migrations {
		got = append(got, m.version)
	}
	if want := []string{"001", "002"}; !reflect.DeepEqual(got, want) {
		t.Errorf("versions = %v, want %v", got, want)
	}

	fsys["001_dup.sql"] = &fstest.MapFile{Data: []byte("-- migrate:up\n")}
	if _, err := loadMigrations(fsys); err == nil {
		t.Error("expected an error for a duplicate version")
	}
}

func TestMigrate_SQLiteAppliesEverythingThenNoOp(t *testing.T) {
	ctx := context.Background()
	d := newSQLiteMemoryDB(t)
	want := embeddedVersions(t, "sqlite")

	applied, err := Migrate(ctx, d)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !reflect.DeepEqual(applied, want) {
		t.Errorf("applied = %v, want %v", applied, want)
	}
	if got := recordedVersions(t, d); !reflect.DeepEqual(got, want) {
		t.Errorf("schema_migrations = %v, want %v", got, want)
	}
	if !sqliteTableExists(t, d, "tenants") {
		t.Error("tenants table missing after Migrate")
	}

	applied, err = Migrate(ctx, d)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("second Migrate applied %v, want nothing", applied)
	}
	if got := recordedVersions(t, d); !reflect.DeepEqual(got, want) {
		t.Errorf("schema_migrations after second Migrate = %v, want %v", got, want)
	}
}

func TestMigrate_SQLiteAppliesOnlyNewVersions(t *testing.T) {
	ctx := context.Background()
	d := newSQLiteMemoryDB(t)
	fsys := migrationFS(map[string]string{
		"001_a.sql": "CREATE TABLE a (id integer);",
		"002_b.sql": "CREATE TABLE b (id integer);",
		"003_c.sql": "CREATE TABLE c (id integer);",
	})

	// 002 was applied earlier, e.g. by the dbmate CLI.
	if _, err := d.sqlDB.Exec(createSchemaMigrations); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if _, err := d.sqlDB.Exec(`INSERT INTO schema_migrations (version) VALUES ('002')`); err != nil {
		t.Fatalf("record 002: %v", err)
	}

	applied, err := migrate(ctx, d, fsys)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if want := []string{"001", "003"}; !reflect.DeepEqual(applied, want) {
		t.Errorf("applied = %v, want %v", applied, want)
	}
	if sqliteTableExists(t, d, "b") {
		t.Error("recorded migration 002 ran again")
	}
	if got, want := recordedVersions(t, d), []string{"001", "002", "003"}; !reflect.DeepEqual(got, want) {
		t.Errorf("schema_migrations = %v, want %v", got, want)
	}
}

func TestMigrate_SQLiteRollsBackBrokenMigration(t *testing.T) {
	ctx := context.Background()
	d := newSQLiteMemoryDB(t)
	files := map[string]string{"001_a.sql": "CREATE TABLE a (id integer);"}
	if _, err := migrate(ctx, d, migrationFS(files)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	files["002_broken.sql"] = "CREATE TABLE b (id integer);\nINSERT INTO missing_table (id) VALUES (1);"
	files["003_c.sql"] = "CREATE TABLE c (id integer);"
	applied, err := migrate(ctx, d, migrationFS(files))
	if err == nil {
		t.Fatal("expected an error from the broken migration")
	}
	if len(applied) != 0 {
		t.Errorf("applied = %v, want nothing", applied)
	}
	if sqliteTableExists(t, d, "b") {
		t.Error("broken migration was not rolled back: table b exists")
	}
	if sqliteTableExists(t, d, "c") {
		t.Error("migration after the broken one ran")
	}
	if got, want := recordedVersions(t, d), []string{"001"}; !reflect.DeepEqual(got, want) {
		t.Errorf("schema_migrations = %v, want %v", got, want)
	}
}

func TestMigrate_SQLiteWithoutTransaction(t *testing.T) {
	ctx := context.Background()
	d := newSQLiteMemoryDB(t)
	fsys := fstest.MapFS{
		"001_a.sql": &fstest.MapFile{Data: []byte("-- migrate:up transaction:false\nCREATE TABLE a (id integer);\n")},
	}
	applied, err := migrate(ctx, d, fsys)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if want := []string{"001"}; !reflect.DeepEqual(applied, want) {
		t.Errorf("applied = %v, want %v", applied, want)
	}
	if !sqliteTableExists(t, d, "a") {
		t.Error("table a missing")
	}
}

func TestMigrate_UnsupportedDriver(t *testing.T) {
	if _, err := Migrate(context.Background(), &DB{Driver: "oracle"}); err == nil {
		t.Error("expected an error for an unsupported driver")
	}
}

// requirePostgres skips the test unless TEST_DATABASE_URL is set (failing
// instead when CI_REQUIRE_POSTGRES=1).
func requirePostgres(t *testing.T) {
	t.Helper()
	if os.Getenv(testDatabaseURLEnv) == "" {
		if os.Getenv(requirePostgresEnv) == "1" {
			t.Fatalf("%s not set but %s=1; PostgreSQL migrate tests are required", testDatabaseURLEnv, requirePostgresEnv)
		}
		t.Skipf("%s not set; skipping PostgreSQL migrate tests", testDatabaseURLEnv)
	}
}

func TestMigrate_PostgresAppliesEverythingThenNoOp(t *testing.T) {
	requirePostgres(t)
	ctx := context.Background()
	d := &DB{Driver: config.DriverPostgres, pool: newPostgresTestPool(t)}
	want := embeddedVersions(t, "postgres")

	applied, err := Migrate(ctx, d)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !reflect.DeepEqual(applied, want) {
		t.Errorf("applied = %v, want %v", applied, want)
	}
	applied, err = Migrate(ctx, d)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("second Migrate applied %v, want nothing", applied)
	}
	if got := recordedVersions(t, d); !reflect.DeepEqual(got, want) {
		t.Errorf("schema_migrations = %v, want %v", got, want)
	}
}

func TestMigrate_PostgresRollsBackBrokenMigration(t *testing.T) {
	requirePostgres(t)
	ctx := context.Background()
	d := &DB{Driver: config.DriverPostgres, pool: newPostgresTestPool(t)}
	files := map[string]string{"001_a.sql": "CREATE TABLE a (id integer);"}
	if _, err := migrate(ctx, d, migrationFS(files)); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	files["002_broken.sql"] = "CREATE TABLE b (id integer);\nINSERT INTO missing_table (id) VALUES (1);"
	if _, err := migrate(ctx, d, migrationFS(files)); err == nil {
		t.Fatal("expected an error from the broken migration")
	}
	var exists bool
	if err := d.pool.QueryRow(ctx, `SELECT to_regclass('b') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("look up table b: %v", err)
	}
	if exists {
		t.Error("broken migration was not rolled back: table b exists")
	}
	if got, want := recordedVersions(t, d), []string{"001"}; !reflect.DeepEqual(got, want) {
		t.Errorf("schema_migrations = %v, want %v", got, want)
	}
}

func TestMigrate_PostgresConcurrentCallsApplyOnce(t *testing.T) {
	requirePostgres(t)
	ctx := context.Background()
	d := &DB{Driver: config.DriverPostgres, pool: newPostgresTestPool(t)}
	want := embeddedVersions(t, "postgres")

	const callers = 2
	var wg sync.WaitGroup
	results := make([][]string, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = Migrate(ctx, d)
		}()
	}
	wg.Wait()

	var all []string
	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("Migrate #%d: %v", i, errs[i])
		}
		all = append(all, results[i]...)
	}
	if !reflect.DeepEqual(all, want) {
		t.Errorf("versions applied across callers = %v, want each of %v exactly once", all, want)
	}
	if got := recordedVersions(t, d); !reflect.DeepEqual(got, want) {
		t.Errorf("schema_migrations = %v, want %v", got, want)
	}
}
