// Package database migrator tests against a real PostgreSQL server, run
// only when TEST_DATABASE_URL is set, like the contract suite in
// contract_test.go.
package database

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"app/internal/config"
)

// skipUnlessPostgresTestDatabase skips the calling test unless
// TEST_DATABASE_URL is set, failing instead when CI_REQUIRE_POSTGRES=1 so
// the suite can't silently no-op in CI.
func skipUnlessPostgresTestDatabase(t *testing.T) string {
	t.Helper()

	dbURL := os.Getenv(testDatabaseURLEnv)
	if dbURL == "" {
		if os.Getenv(requirePostgresEnv) == "1" {
			t.Fatalf("%s not set but %s=1; PostgreSQL migrator suite is required", testDatabaseURLEnv, requirePostgresEnv)
		}
		t.Skipf("%s not set; skipping PostgreSQL migrator suite", testDatabaseURLEnv)
	}
	return dbURL
}

// newMigratePostgresTestDB connects to TEST_DATABASE_URL, creates a private,
// empty schema, and wraps a pool scoped to it (via search_path) as a *DB
// with no migrations applied. The schema is dropped when the test finishes.
func newMigratePostgresTestDB(t *testing.T) *DB {
	t.Helper()
	ctx := context.Background()
	dbURL := skipUnlessPostgresTestDatabase(t)

	schema := pgx.Identifier{fmt.Sprintf("migrate_%d", time.Now().UnixNano())}

	setup, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	if _, err := setup.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, schema.Sanitize())); err != nil {
		setup.Close(ctx)
		t.Fatalf("create schema %s: %v", schema, err)
	}
	setup.Close(ctx)

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		conn, err := pgx.Connect(cleanupCtx, dbURL)
		if err != nil {
			t.Logf("drop schema %s: connect: %v", schema, err)
			return
		}
		defer conn.Close(cleanupCtx)
		if _, err := conn.Exec(cleanupCtx, fmt.Sprintf(`DROP SCHEMA %s CASCADE`, schema.Sanitize())); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})

	poolCfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatalf("parse postgres config: %v", err)
	}
	poolCfg.ConnConfig.RuntimeParams["search_path"] = schema.Sanitize()

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatalf("open postgres pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return &DB{Driver: config.DriverPostgres, pgPool: pool}
}

func TestMigrate_Postgres_AppliesEverythingOnEmptyDB(t *testing.T) {
	d := newMigratePostgresTestDB(t)
	ctx := context.Background()

	applied, err := migrate(ctx, d, twoMigrationFixture())
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}

	wantVersions := []string{"20260101000000", "20260102000000"}
	assertStringSliceEqual(t, "applied", applied, wantVersions)
	assertPostgresTableExists(t, ctx, d.pgPool, "widgets")
	assertPostgresTableExists(t, ctx, d.pgPool, "gadgets")
	assertSchemaMigrationsPostgres(t, ctx, d.pgPool, wantVersions)
}

func TestMigrate_Postgres_NoopOnSecondRun(t *testing.T) {
	d := newMigratePostgresTestDB(t)
	ctx := context.Background()
	fsys := twoMigrationFixture()

	if _, err := migrate(ctx, d, fsys); err != nil {
		t.Fatalf("first migrate: %v", err)
	}

	applied, err := migrate(ctx, d, fsys)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("second migrate applied %v, want none", applied)
	}
	assertSchemaMigrationsPostgres(t, ctx, d.pgPool, []string{"20260101000000", "20260102000000"})
}

// TestMigrate_Postgres_ConcurrentMigrateAppliesEachOnce runs two Migrate
// calls concurrently against the same fresh schema. The pg_advisory_lock
// held for each call's duration must serialize them so every migration is
// applied exactly once between the two, never twice and never zero times.
func TestMigrate_Postgres_ConcurrentMigrateAppliesEachOnce(t *testing.T) {
	d := newMigratePostgresTestDB(t)
	fsys := twoMigrationFixture()

	var wg sync.WaitGroup
	results := make([][]string, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = migrate(context.Background(), d, fsys)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("migrate #%d: %v", i, err)
		}
	}

	total := len(results[0]) + len(results[1])
	if total != 2 {
		t.Fatalf("combined applied versions = %d, want exactly 2 (one migration applied by each call, or both by one)", total)
	}
	seen := make(map[string]bool)
	for _, applied := range results {
		for _, version := range applied {
			if seen[version] {
				t.Fatalf("version %s applied by both concurrent Migrate calls", version)
			}
			seen[version] = true
		}
	}

	assertPostgresTableExists(t, context.Background(), d.pgPool, "widgets")
	assertPostgresTableExists(t, context.Background(), d.pgPool, "gadgets")
	assertSchemaMigrationsPostgres(t, context.Background(), d.pgPool, []string{"20260101000000", "20260102000000"})
}

func assertPostgresTableExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) {
	t.Helper()

	var oid *string
	if err := pool.QueryRow(ctx, `SELECT to_regclass($1)::text`, name).Scan(&oid); err != nil {
		t.Fatalf("check table %s: %v", name, err)
	}
	if oid == nil {
		t.Fatalf("expected table %s to exist", name)
	}
}

func assertSchemaMigrationsPostgres(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want []string) {
	t.Helper()

	rows, err := pool.Query(ctx, `SELECT version FROM `+migrationsTable+` ORDER BY version`)
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
