// Package database contract tests exercise every db.Querier method against a
// real, migrated database, so the same suite proves both engines behave
// alike. It always runs against SQLite :memory:, and additionally against
// PostgreSQL when TEST_DATABASE_URL is set.
package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"app/internal/db"
)

// testDatabaseURLEnv names the environment variable that, when set, points
// the PostgreSQL contract suite at a reachable server.
const testDatabaseURLEnv = "TEST_DATABASE_URL"

// querierFactory returns a fresh, empty, migrated db.Querier, isolated from
// every other call (each subtest gets its own database).
type querierFactory func(t *testing.T) db.Querier

// TestQuerierContract_SQLite runs the engine-neutral contract suite against
// a SQLite :memory: database with db/sqlite/migrations applied.
func TestQuerierContract_SQLite(t *testing.T) {
	runQuerierContract(t, newSQLiteTestQuerier)
}

// TestQuerierContract_Postgres runs the same engine-neutral contract suite
// against a PostgreSQL server reachable at TEST_DATABASE_URL, with
// db/postgres/migrations applied to a fresh, private schema. It is skipped
// when TEST_DATABASE_URL is unset.
func TestQuerierContract_Postgres(t *testing.T) {
	if os.Getenv(testDatabaseURLEnv) == "" {
		t.Skipf("%s not set; skipping PostgreSQL contract suite", testDatabaseURLEnv)
	}
	runQuerierContract(t, newPostgresTestQuerier)
}

// runQuerierContract exercises every db.Querier method through newQuerier.
// Subtests call newQuerier themselves so each gets its own isolated
// database.
func runQuerierContract(t *testing.T, newQuerier querierFactory) {
	t.Run("TenantCRUD", func(t *testing.T) { testTenantCRUD(t, newQuerier(t)) })
	t.Run("UserCRUD", func(t *testing.T) { testUserCRUD(t, newQuerier(t)) })
	t.Run("FileCRUDAndTenantScoping", func(t *testing.T) { testFileCRUDAndTenantScoping(t, newQuerier(t)) })
	t.Run("FormCRUDAndTenantScoping", func(t *testing.T) { testFormCRUDAndTenantScoping(t, newQuerier(t)) })
	t.Run("FormPaginationOrder", func(t *testing.T) { testFormPaginationOrder(t, newQuerier(t)) })
	t.Run("SubmissionCRUDAndTenantScoping", func(t *testing.T) { testSubmissionCRUDAndTenantScoping(t, newQuerier(t)) })
	t.Run("WebhookCRUD", func(t *testing.T) { testWebhookCRUD(t, newQuerier(t)) })
	t.Run("ListAndCountFormsByTenantFilters", func(t *testing.T) { testListAndCountFormsByTenantFilters(t, newQuerier(t)) })
	t.Run("UpdateFormDraftLock", func(t *testing.T) { testUpdateFormDraftLock(t, newQuerier(t)) })
	t.Run("CreateUserIfNotExistsIdempotency", func(t *testing.T) { testCreateUserIfNotExistsIdempotency(t, newQuerier(t)) })
	t.Run("Violations", func(t *testing.T) { testViolations(t, newQuerier(t)) })
}

// newSQLiteTestQuerier opens a private SQLite :memory: database, applies
// db/sqlite/migrations, and wraps it as a db.Querier. Capping the pool at
// one connection is required for :memory: databases: every new connection
// otherwise opens its own empty database.
func newSQLiteTestQuerier(t *testing.T) db.Querier {
	t.Helper()

	sqlDB, err := sql.Open("sqlite", sqliteDSN(sqliteMemoryDSN, sqliteBusyTimeout, sqliteJournalMode))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })

	applySQLiteMigrations(t, sqlDB)

	return newSQLiteQuerier(sqlDB)
}

// applySQLiteMigrations runs the "-- migrate:up" block of every migration in
// db/sqlite/migrations, in filename order, against sqlDB.
func applySQLiteMigrations(t *testing.T, sqlDB *sql.DB) {
	t.Helper()

	dir := filepath.Join("..", "..", "db", "sqlite", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations dir %s: %v", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatalf("no migrations found in %s", dir)
	}

	for _, name := range names {
		up := readMigrationUp(t, filepath.Join(dir, name))
		if _, err := sqlDB.Exec(up); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
}

// readMigrationUp extracts the dbmate "-- migrate:up" block from path,
// stopping before "-- migrate:down" if present.
func readMigrationUp(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", path, err)
	}

	const upMarker = "-- migrate:up"
	const downMarker = "-- migrate:down"

	content := string(data)
	upStart := strings.Index(content, upMarker)
	if upStart < 0 {
		t.Fatalf("migration %s missing %q marker", path, upMarker)
	}
	content = content[upStart+len(upMarker):]
	if downStart := strings.Index(content, downMarker); downStart >= 0 {
		content = content[:downStart]
	}
	return content
}

// newPostgresTestQuerier connects to TEST_DATABASE_URL, creates a private
// schema, applies db/postgres/migrations into it, and wraps the resulting
// pool as a db.Querier. The schema is dropped when the test finishes.
func newPostgresTestQuerier(t *testing.T) db.Querier {
	t.Helper()
	ctx := context.Background()
	dbURL := os.Getenv(testDatabaseURLEnv)

	schema := pgx.Identifier{fmt.Sprintf("contract_%d", time.Now().UnixNano())}

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

	applyPostgresMigrations(t, ctx, pool)

	return db.New(pool)
}

// applyPostgresMigrations runs the "-- migrate:up" block of every migration
// in db/postgres/migrations, in filename order, against pool.
func applyPostgresMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	dir := filepath.Join("..", "..", "db", "postgres", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations dir %s: %v", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatalf("no migrations found in %s", dir)
	}

	for _, name := range names {
		up := readMigrationUp(t, filepath.Join(dir, name))
		if _, err := pool.Exec(ctx, up); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
}

// Small helpers to build pointer-typed params without repeating &v literals.

func boolPtr(v bool) *bool       { return &v }
func stringPtr(v string) *string { return &v }

func rawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return b
}

// mustBool dereferences a *bool, failing the test if it is nil.
func mustBool(t *testing.T, name string, v *bool) bool {
	t.Helper()
	if v == nil {
		t.Fatalf("%s: expected non-nil bool", name)
	}
	return *v
}

func testTenantCRUD(t *testing.T, q db.Querier) {
	ctx := context.Background()

	tenant, err := q.CreateTenant(ctx, db.CreateTenantParams{Name: "Acme", ApiKey: "key-tenant-crud"})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if tenant.ID == 0 {
		t.Fatal("CreateTenant: expected a generated id")
	}
	if tenant.Name != "Acme" || tenant.ApiKey != "key-tenant-crud" {
		t.Fatalf("CreateTenant: unexpected tenant %+v", tenant)
	}
	if !mustBool(t, "tenant.IsActive", tenant.IsActive) {
		t.Fatal("CreateTenant: expected tenant to be active by default")
	}

	got, err := q.GetTenantByAPIKey(ctx, "key-tenant-crud")
	if err != nil {
		t.Fatalf("GetTenantByAPIKey: %v", err)
	}
	if got.ID != tenant.ID || got.Name != tenant.Name {
		t.Fatalf("GetTenantByAPIKey: got %+v, want id/name matching %+v", got, tenant)
	}

	if _, err := q.GetTenantByAPIKey(ctx, "does-not-exist"); !IsNoRows(err) {
		t.Fatalf("GetTenantByAPIKey: expected IsNoRows, got %v", err)
	}
}

func testUserCRUD(t *testing.T, q db.Querier) {
	ctx := context.Background()

	created, err := q.CreateUser(ctx, db.CreateUserParams{
		Username:     "alice",
		PasswordHash: "hash-1",
		Role:         db.UserRoleBASIC,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.Role != db.UserRoleBASIC {
		t.Fatalf("CreateUser: got role %q, want BASIC", created.Role)
	}

	byID, err := q.GetUserByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if byID.Username != "alice" {
		t.Fatalf("GetUserByID: got username %q, want alice", byID.Username)
	}

	byUsername, err := q.GetUserByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if byUsername.ID != created.ID {
		t.Fatalf("GetUserByUsername: got id %d, want %d", byUsername.ID, created.ID)
	}

	if _, err := q.GetUserByID(ctx, created.ID+1_000_000); !IsNoRows(err) {
		t.Fatalf("GetUserByID: expected IsNoRows for missing id, got %v", err)
	}

	updated, err := q.UpdateUserRole(ctx, db.UpdateUserRoleParams{ID: created.ID, Role: db.UserRoleADMIN})
	if err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}
	if updated.Role != db.UserRoleADMIN {
		t.Fatalf("UpdateUserRole: got role %q, want ADMIN", updated.Role)
	}

	// ListUsers is ordered by id, ascending; verify pagination walks that
	// order without gaps or repeats.
	var ids []int32
	for i := 0; i < 3; i++ {
		u, err := q.CreateUser(ctx, db.CreateUserParams{
			Username:     "user" + string(rune('a'+i)),
			PasswordHash: "hash",
			Role:         db.UserRoleBASIC,
		})
		if err != nil {
			t.Fatalf("CreateUser %d: %v", i, err)
		}
		ids = append(ids, u.ID)
	}

	page1, err := q.ListUsers(ctx, db.ListUsersParams{PageOffset: 0, PageLimit: 2})
	if err != nil {
		t.Fatalf("ListUsers page1: %v", err)
	}
	page2, err := q.ListUsers(ctx, db.ListUsersParams{PageOffset: 2, PageLimit: 2})
	if err != nil {
		t.Fatalf("ListUsers page2: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("ListUsers page1: got %d rows, want 2", len(page1))
	}
	allByID := make([]int32, 0, len(page1)+len(page2))
	for _, u := range page1 {
		allByID = append(allByID, u.ID)
	}
	for _, u := range page2 {
		allByID = append(allByID, u.ID)
	}
	for i := 1; i < len(allByID); i++ {
		if allByID[i] <= allByID[i-1] {
			t.Fatalf("ListUsers: rows not in ascending id order: %v", allByID)
		}
	}
}

func testFileCRUDAndTenantScoping(t *testing.T, q db.Querier) {
	ctx := context.Background()

	tenantA, err := q.CreateTenant(ctx, db.CreateTenantParams{Name: "Tenant A", ApiKey: "key-file-a"})
	if err != nil {
		t.Fatalf("CreateTenant A: %v", err)
	}
	tenantB, err := q.CreateTenant(ctx, db.CreateTenantParams{Name: "Tenant B", ApiKey: "key-file-b"})
	if err != nil {
		t.Fatalf("CreateTenant B: %v", err)
	}

	file, err := q.CreateFile(ctx, db.CreateFileParams{
		ID:             "11111111-1111-4111-8111-111111111111",
		TenantID:       tenantA.ID,
		Name:           "report.pdf",
		ContentType:    "application/pdf",
		SizeBytes:      3,
		ChecksumSha256: "039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81",
		Data:           []byte{1, 2, 3},
	})
	if err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	if file.ID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("CreateFile: got id %q, want the app-supplied id", file.ID)
	}

	got, err := q.GetFileByID(ctx, file.ID)
	if err != nil {
		t.Fatalf("GetFileByID: %v", err)
	}
	if string(got.Data) != "\x01\x02\x03" {
		t.Fatalf("GetFileByID: unexpected data %v", got.Data)
	}

	count, err := q.CountAllFiles(ctx)
	if err != nil {
		t.Fatalf("CountAllFiles: %v", err)
	}
	if count != 1 {
		t.Fatalf("CountAllFiles: got %d, want 1", count)
	}

	listed, err := q.ListAllFiles(ctx, db.ListAllFilesParams{PageOffset: 0, PageLimit: 10})
	if err != nil {
		t.Fatalf("ListAllFiles: %v", err)
	}
	if len(listed) != 1 || listed[0].TenantName != "Tenant A" {
		t.Fatalf("ListAllFiles: unexpected rows %+v", listed)
	}

	// Tenant scoping: tenant B cannot delete tenant A's file.
	rows, err := q.DeleteFile(ctx, db.DeleteFileParams{ID: file.ID, TenantID: tenantB.ID})
	if err != nil {
		t.Fatalf("DeleteFile (wrong tenant): %v", err)
	}
	if rows != 0 {
		t.Fatalf("DeleteFile (wrong tenant): affected %d rows, want 0", rows)
	}
	if _, err := q.GetFileByID(ctx, file.ID); err != nil {
		t.Fatalf("GetFileByID after no-op delete: expected file to still exist, got %v", err)
	}

	rows, err = q.DeleteFile(ctx, db.DeleteFileParams{ID: file.ID, TenantID: tenantA.ID})
	if err != nil {
		t.Fatalf("DeleteFile (correct tenant): %v", err)
	}
	if rows != 1 {
		t.Fatalf("DeleteFile (correct tenant): affected %d rows, want 1", rows)
	}
	if _, err := q.GetFileByID(ctx, file.ID); !IsNoRows(err) {
		t.Fatalf("GetFileByID after delete: expected IsNoRows, got %v", err)
	}
}

func testFormCRUDAndTenantScoping(t *testing.T, q db.Querier) {
	ctx := context.Background()

	tenantA, err := q.CreateTenant(ctx, db.CreateTenantParams{Name: "Tenant A", ApiKey: "key-form-a"})
	if err != nil {
		t.Fatalf("CreateTenant A: %v", err)
	}
	tenantB, err := q.CreateTenant(ctx, db.CreateTenantParams{Name: "Tenant B", ApiKey: "key-form-b"})
	if err != nil {
		t.Fatalf("CreateTenant B: %v", err)
	}

	content := rawJSON(t, map[string]any{"fields": []string{}})
	form, err := q.CreateForm(ctx, db.CreateFormParams{
		TenantID:        tenantA.ID,
		Title:           "Feedback",
		Slug:            "feedback-form",
		IsActive:        boolPtr(true),
		FormContent:     content,
		PublicAvailable: true,
		AcceptAnonymous: true,
		IsDraft:         false,
	})
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}

	// Tenant scoping: GetFormByID under the wrong tenant sees nothing.
	if _, err := q.GetFormByID(ctx, db.GetFormByIDParams{ID: form.ID, TenantID: tenantB.ID}); !IsNoRows(err) {
		t.Fatalf("GetFormByID (wrong tenant): expected IsNoRows, got %v", err)
	}
	byID, err := q.GetFormByID(ctx, db.GetFormByIDParams{ID: form.ID, TenantID: tenantA.ID})
	if err != nil {
		t.Fatalf("GetFormByID (correct tenant): %v", err)
	}
	if byID.TenantName != "Tenant A" {
		t.Fatalf("GetFormByID: got tenant name %q, want Tenant A", byID.TenantName)
	}

	anyForm, err := q.GetAnyFormByID(ctx, form.ID)
	if err != nil {
		t.Fatalf("GetAnyFormByID: %v", err)
	}
	if anyForm.SubmissionCount != 0 {
		t.Fatalf("GetAnyFormByID: got submission count %d, want 0", anyForm.SubmissionCount)
	}

	public, err := q.GetPublicFormBySlug(ctx, "feedback-form")
	if err != nil {
		t.Fatalf("GetPublicFormBySlug: %v", err)
	}
	if public.ID != form.ID {
		t.Fatalf("GetPublicFormBySlug: got id %d, want %d", public.ID, form.ID)
	}

	updated, err := q.UpdateForm(ctx, db.UpdateFormParams{
		ID:              form.ID,
		TenantID:        tenantA.ID,
		Title:           "Feedback v2",
		Slug:            form.Slug,
		IsActive:        boolPtr(true),
		FormContent:     content,
		PublicAvailable: true,
		AcceptAnonymous: true,
		IsDraft:         false,
	})
	if err != nil {
		t.Fatalf("UpdateForm: %v", err)
	}
	if updated.Title != "Feedback v2" {
		t.Fatalf("UpdateForm: got title %q, want Feedback v2", updated.Title)
	}

	listAll, err := q.ListAllForms(ctx, db.ListAllFormsParams{PageOffset: 0, PageLimit: 10})
	if err != nil {
		t.Fatalf("ListAllForms: %v", err)
	}
	if len(listAll) != 1 {
		t.Fatalf("ListAllForms: got %d rows, want 1", len(listAll))
	}

	listByTenant, err := q.ListFormsByTenant(ctx, db.ListFormsByTenantParams{TenantID: tenantA.ID, PageOffset: 0, PageLimit: 10})
	if err != nil {
		t.Fatalf("ListFormsByTenant: %v", err)
	}
	if len(listByTenant) != 1 {
		t.Fatalf("ListFormsByTenant (tenant A): got %d rows, want 1", len(listByTenant))
	}
	listByOtherTenant, err := q.ListFormsByTenant(ctx, db.ListFormsByTenantParams{TenantID: tenantB.ID, PageOffset: 0, PageLimit: 10})
	if err != nil {
		t.Fatalf("ListFormsByTenant (tenant B): %v", err)
	}
	if len(listByOtherTenant) != 0 {
		t.Fatalf("ListFormsByTenant (tenant B): got %d rows, want 0 (tenant scoping leak)", len(listByOtherTenant))
	}

	// Tenant scoping: tenant B cannot delete tenant A's form.
	rows, err := q.DeleteForm(ctx, db.DeleteFormParams{ID: form.ID, TenantID: tenantB.ID})
	if err != nil {
		t.Fatalf("DeleteForm (wrong tenant): %v", err)
	}
	if rows != 0 {
		t.Fatalf("DeleteForm (wrong tenant): affected %d rows, want 0", rows)
	}

	rows, err = q.DeleteForm(ctx, db.DeleteFormParams{ID: form.ID, TenantID: tenantA.ID})
	if err != nil {
		t.Fatalf("DeleteForm (correct tenant): %v", err)
	}
	if rows != 1 {
		t.Fatalf("DeleteForm (correct tenant): affected %d rows, want 1", rows)
	}
}

func testFormPaginationOrder(t *testing.T, q db.Querier) {
	ctx := context.Background()

	tenant, err := q.CreateTenant(ctx, db.CreateTenantParams{Name: "Tenant", ApiKey: "key-pagination"})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	content := rawJSON(t, map[string]any{})
	var createdIDs []int32
	for i := 0; i < 5; i++ {
		f, err := q.CreateForm(ctx, db.CreateFormParams{
			TenantID:        tenant.ID,
			Title:           "Form",
			Slug:            "form-" + string(rune('a'+i)),
			IsActive:        boolPtr(true),
			FormContent:     content,
			PublicAvailable: true,
			AcceptAnonymous: true,
			IsDraft:         false,
		})
		if err != nil {
			t.Fatalf("CreateForm %d: %v", i, err)
		}
		createdIDs = append(createdIDs, f.ID)
	}

	// ListFormsByTenant orders by created_at DESC, id DESC; forms created in
	// the same instant (as here) still sort deterministically by id.
	page1, err := q.ListFormsByTenant(ctx, db.ListFormsByTenantParams{TenantID: tenant.ID, PageOffset: 0, PageLimit: 2})
	if err != nil {
		t.Fatalf("ListFormsByTenant page1: %v", err)
	}
	page2, err := q.ListFormsByTenant(ctx, db.ListFormsByTenantParams{TenantID: tenant.ID, PageOffset: 2, PageLimit: 3})
	if err != nil {
		t.Fatalf("ListFormsByTenant page2: %v", err)
	}
	if len(page1) != 2 || len(page2) != 3 {
		t.Fatalf("ListFormsByTenant: got page sizes %d/%d, want 2/3", len(page1), len(page2))
	}

	var gotIDs []int32
	for _, f := range page1 {
		gotIDs = append(gotIDs, f.ID)
	}
	for _, f := range page2 {
		gotIDs = append(gotIDs, f.ID)
	}

	wantIDs := make([]int32, len(createdIDs))
	copy(wantIDs, createdIDs)
	sort.Slice(wantIDs, func(i, j int) bool { return wantIDs[i] > wantIDs[j] })

	if len(gotIDs) != len(wantIDs) {
		t.Fatalf("ListFormsByTenant: got %d total ids, want %d", len(gotIDs), len(wantIDs))
	}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Fatalf("ListFormsByTenant: order mismatch at %d: got %v, want %v", i, gotIDs, wantIDs)
		}
	}

	count, err := q.CountFormsByTenant(ctx, db.CountFormsByTenantParams{TenantID: tenant.ID})
	if err != nil {
		t.Fatalf("CountFormsByTenant: %v", err)
	}
	if int(count) != len(createdIDs) {
		t.Fatalf("CountFormsByTenant: got %d, want %d", count, len(createdIDs))
	}
}

func testSubmissionCRUDAndTenantScoping(t *testing.T, q db.Querier) {
	ctx := context.Background()

	tenantA, err := q.CreateTenant(ctx, db.CreateTenantParams{Name: "Tenant A", ApiKey: "key-sub-a"})
	if err != nil {
		t.Fatalf("CreateTenant A: %v", err)
	}
	tenantB, err := q.CreateTenant(ctx, db.CreateTenantParams{Name: "Tenant B", ApiKey: "key-sub-b"})
	if err != nil {
		t.Fatalf("CreateTenant B: %v", err)
	}

	content := rawJSON(t, map[string]any{})
	form, err := q.CreateForm(ctx, db.CreateFormParams{
		TenantID:        tenantA.ID,
		Title:           "Survey",
		Slug:            "survey",
		IsActive:        boolPtr(true),
		FormContent:     content,
		PublicAvailable: true,
		AcceptAnonymous: true,
		IsDraft:         false,
	})
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}

	var submissionIDs []int32
	for i := 0; i < 3; i++ {
		payload := rawJSON(t, map[string]any{"answer": i})
		s, err := q.CreateFormSubmission(ctx, db.CreateFormSubmissionParams{FormID: form.ID, Payload: payload})
		if err != nil {
			t.Fatalf("CreateFormSubmission %d: %v", i, err)
		}
		submissionIDs = append(submissionIDs, s.ID)
	}

	_, err = q.CreateSubmissionMetadata(ctx, db.CreateSubmissionMetadataParams{
		SubmissionID: submissionIDs[0],
		IpAddress:    stringPtr("127.0.0.1"),
	})
	if err != nil {
		t.Fatalf("CreateSubmissionMetadata: %v", err)
	}

	all, err := q.ListAllSubmissionsByForm(ctx, db.ListAllSubmissionsByFormParams{FormID: form.ID, TenantID: tenantA.ID})
	if err != nil {
		t.Fatalf("ListAllSubmissionsByForm: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("ListAllSubmissionsByForm: got %d rows, want 3", len(all))
	}
	// Ordered by submitted_at DESC, id DESC: the most recently created
	// submission (highest id) comes first.
	if all[0].ID != submissionIDs[len(submissionIDs)-1] {
		t.Fatalf("ListAllSubmissionsByForm: got first id %d, want %d", all[0].ID, submissionIDs[len(submissionIDs)-1])
	}

	// Tenant scoping: tenant B's submissions list for the same form is empty.
	scoped, err := q.ListAllSubmissionsByForm(ctx, db.ListAllSubmissionsByFormParams{FormID: form.ID, TenantID: tenantB.ID})
	if err != nil {
		t.Fatalf("ListAllSubmissionsByForm (wrong tenant): %v", err)
	}
	if len(scoped) != 0 {
		t.Fatalf("ListAllSubmissionsByForm (wrong tenant): got %d rows, want 0", len(scoped))
	}

	page, err := q.ListSubmissionsByForm(ctx, db.ListSubmissionsByFormParams{FormID: form.ID, TenantID: tenantA.ID, PageOffset: 0, PageLimit: 2})
	if err != nil {
		t.Fatalf("ListSubmissionsByForm: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("ListSubmissionsByForm: got %d rows, want 2", len(page))
	}
}

func testWebhookCRUD(t *testing.T, q db.Querier) {
	ctx := context.Background()

	tenant, err := q.CreateTenant(ctx, db.CreateTenantParams{Name: "Tenant", ApiKey: "key-webhook"})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	content := rawJSON(t, map[string]any{})
	form, err := q.CreateForm(ctx, db.CreateFormParams{
		TenantID:        tenant.ID,
		Title:           "Form",
		Slug:            "form-webhook",
		IsActive:        boolPtr(true),
		FormContent:     content,
		PublicAvailable: true,
		AcceptAnonymous: true,
		IsDraft:         false,
	})
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}

	webhook, err := q.CreateFormWebhook(ctx, db.CreateFormWebhookParams{
		FormID:      form.ID,
		TargetUrl:   "https://example.com/hook",
		SecretToken: stringPtr("secret"),
	})
	if err != nil {
		t.Fatalf("CreateFormWebhook: %v", err)
	}
	if !mustBool(t, "webhook.IsActive", webhook.IsActive) {
		t.Fatal("CreateFormWebhook: expected webhook to be active by default")
	}

	active, err := q.ListActiveWebhooksByForm(ctx, form.ID)
	if err != nil {
		t.Fatalf("ListActiveWebhooksByForm: %v", err)
	}
	if len(active) != 1 || active[0].ID != webhook.ID {
		t.Fatalf("ListActiveWebhooksByForm: unexpected rows %+v", active)
	}
}

// testListAndCountFormsByTenantFilters covers the is_active, is_draft and
// search filters shared by ListFormsByTenant and CountFormsByTenant: nil
// means "any value", non-nil narrows to an exact match, and search does a
// case-insensitive (ASCII-only on SQLite) substring match on the title.
func testListAndCountFormsByTenantFilters(t *testing.T, q db.Querier) {
	ctx := context.Background()

	tenant, err := q.CreateTenant(ctx, db.CreateTenantParams{Name: "Tenant", ApiKey: "key-filters"})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	content := rawJSON(t, map[string]any{})
	type formSpec struct {
		title    string
		slug     string
		isActive bool
		isDraft  bool
	}
	specs := []formSpec{
		{title: "Active Survey", slug: "filters-active-survey", isActive: true, isDraft: false},
		{title: "Inactive Survey", slug: "filters-inactive-survey", isActive: false, isDraft: false},
		{title: "Draft Feedback", slug: "filters-draft-feedback", isActive: true, isDraft: true},
	}
	for _, s := range specs {
		if _, err := q.CreateForm(ctx, db.CreateFormParams{
			TenantID:        tenant.ID,
			Title:           s.title,
			Slug:            s.slug,
			IsActive:        boolPtr(s.isActive),
			FormContent:     content,
			PublicAvailable: true,
			AcceptAnonymous: true,
			IsDraft:         s.isDraft,
		}); err != nil {
			t.Fatalf("CreateForm %q: %v", s.title, err)
		}
	}

	list := func(arg db.ListFormsByTenantParams) []db.ListFormsByTenantRow {
		t.Helper()
		arg.TenantID = tenant.ID
		arg.PageLimit = 10
		rows, err := q.ListFormsByTenant(ctx, arg)
		if err != nil {
			t.Fatalf("ListFormsByTenant %+v: %v", arg, err)
		}
		return rows
	}
	count := func(arg db.CountFormsByTenantParams) int64 {
		t.Helper()
		arg.TenantID = tenant.ID
		n, err := q.CountFormsByTenant(ctx, arg)
		if err != nil {
			t.Fatalf("CountFormsByTenant %+v: %v", arg, err)
		}
		return n
	}

	if got := list(db.ListFormsByTenantParams{}); len(got) != len(specs) {
		t.Fatalf("no filters: got %d rows, want %d", len(got), len(specs))
	}
	if got := count(db.CountFormsByTenantParams{}); got != int64(len(specs)) {
		t.Fatalf("no filters: got count %d, want %d", got, len(specs))
	}

	if got := list(db.ListFormsByTenantParams{IsActive: boolPtr(true)}); len(got) != 2 {
		t.Fatalf("is_active=true: got %d rows, want 2", len(got))
	}
	if got := count(db.CountFormsByTenantParams{IsActive: boolPtr(true)}); got != 2 {
		t.Fatalf("is_active=true: got count %d, want 2", got)
	}
	if got := list(db.ListFormsByTenantParams{IsActive: boolPtr(false)}); len(got) != 1 || got[0].Title != "Inactive Survey" {
		t.Fatalf("is_active=false: unexpected rows %+v", got)
	}

	if got := list(db.ListFormsByTenantParams{IsDraft: boolPtr(true)}); len(got) != 1 || got[0].Title != "Draft Feedback" {
		t.Fatalf("is_draft=true: unexpected rows %+v", got)
	}
	if got := count(db.CountFormsByTenantParams{IsDraft: boolPtr(false)}); got != 2 {
		t.Fatalf("is_draft=false: got count %d, want 2", got)
	}

	if got := list(db.ListFormsByTenantParams{Search: stringPtr("survey")}); len(got) != 2 {
		t.Fatalf("search=survey (lowercase): got %d rows, want 2", len(got))
	}
	if got := list(db.ListFormsByTenantParams{Search: stringPtr("SURVEY")}); len(got) != 2 {
		t.Fatalf("search=SURVEY (uppercase, ASCII case-insensitive): got %d rows, want 2", len(got))
	}
	if got := count(db.CountFormsByTenantParams{Search: stringPtr("Feedback")}); got != 1 {
		t.Fatalf("search=Feedback: got count %d, want 1", got)
	}

	if got := list(db.ListFormsByTenantParams{IsActive: boolPtr(true), IsDraft: boolPtr(false), Search: stringPtr("survey")}); len(got) != 1 || got[0].Title != "Active Survey" {
		t.Fatalf("combined filters: unexpected rows %+v", got)
	}
}

// testUpdateFormDraftLock verifies UpdateForm's draft lock: once a form has
// a submission its form_content can no longer change and it can no longer
// return to draft, but re-sending the same content (with other fields
// changed) is still accepted.
func testUpdateFormDraftLock(t *testing.T, q db.Querier) {
	ctx := context.Background()

	tenant, err := q.CreateTenant(ctx, db.CreateTenantParams{Name: "Tenant", ApiKey: "key-draft-lock"})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	content := rawJSON(t, map[string]any{"fields": []string{"name"}})
	form, err := q.CreateForm(ctx, db.CreateFormParams{
		TenantID:        tenant.ID,
		Title:           "Locked Form",
		Slug:            "draft-lock-form",
		IsActive:        boolPtr(true),
		FormContent:     content,
		PublicAvailable: true,
		AcceptAnonymous: true,
		IsDraft:         false,
	})
	if err != nil {
		t.Fatalf("CreateForm: %v", err)
	}

	// Before any submission exists, content and draft status are free to
	// change.
	preSubmission, err := q.UpdateForm(ctx, db.UpdateFormParams{
		ID:              form.ID,
		TenantID:        tenant.ID,
		Title:           "Locked Form (still editable)",
		Slug:            form.Slug,
		IsActive:        boolPtr(true),
		FormContent:     rawJSON(t, map[string]any{"fields": []string{"name", "email"}}),
		PublicAvailable: true,
		AcceptAnonymous: true,
		IsDraft:         true,
	})
	if err != nil {
		t.Fatalf("UpdateForm before submission: %v", err)
	}
	if !preSubmission.IsDraft {
		t.Fatal("UpdateForm before submission: expected is_draft to become true")
	}

	// Bring it back out of draft with the content it will be locked to.
	lockedContent := rawJSON(t, map[string]any{"fields": []string{"name", "email"}})
	locked, err := q.UpdateForm(ctx, db.UpdateFormParams{
		ID:              form.ID,
		TenantID:        tenant.ID,
		Title:           preSubmission.Title,
		Slug:            form.Slug,
		IsActive:        boolPtr(true),
		FormContent:     lockedContent,
		PublicAvailable: true,
		AcceptAnonymous: true,
		IsDraft:         false,
	})
	if err != nil {
		t.Fatalf("UpdateForm to publish: %v", err)
	}

	if _, err := q.CreateFormSubmission(ctx, db.CreateFormSubmissionParams{FormID: locked.ID, Payload: rawJSON(t, map[string]any{"name": "Alice", "email": "a@example.com"})}); err != nil {
		t.Fatalf("CreateFormSubmission: %v", err)
	}

	// Changing form_content is now rejected: no row is updated.
	if _, err := q.UpdateForm(ctx, db.UpdateFormParams{
		ID:              locked.ID,
		TenantID:        tenant.ID,
		Title:           locked.Title,
		Slug:            locked.Slug,
		IsActive:        boolPtr(true),
		FormContent:     rawJSON(t, map[string]any{"fields": []string{"name", "email", "phone"}}),
		PublicAvailable: true,
		AcceptAnonymous: true,
		IsDraft:         false,
	}); !IsNoRows(err) {
		t.Fatalf("UpdateForm changing content after submission: expected IsNoRows, got %v", err)
	}

	// Returning to draft is rejected too, even with unchanged content.
	if _, err := q.UpdateForm(ctx, db.UpdateFormParams{
		ID:              locked.ID,
		TenantID:        tenant.ID,
		Title:           locked.Title,
		Slug:            locked.Slug,
		IsActive:        boolPtr(true),
		FormContent:     lockedContent,
		PublicAvailable: true,
		AcceptAnonymous: true,
		IsDraft:         true,
	}); !IsNoRows(err) {
		t.Fatalf("UpdateForm returning to draft after submission: expected IsNoRows, got %v", err)
	}

	// Re-sending the same content, with an unrelated field (title) changed,
	// is accepted.
	renamed, err := q.UpdateForm(ctx, db.UpdateFormParams{
		ID:              locked.ID,
		TenantID:        tenant.ID,
		Title:           "Locked Form (renamed)",
		Slug:            locked.Slug,
		IsActive:        boolPtr(true),
		FormContent:     lockedContent,
		PublicAvailable: true,
		AcceptAnonymous: true,
		IsDraft:         false,
	})
	if err != nil {
		t.Fatalf("UpdateForm re-sending same content: %v", err)
	}
	if renamed.Title != "Locked Form (renamed)" {
		t.Fatalf("UpdateForm re-sending same content: got title %q, want %q", renamed.Title, "Locked Form (renamed)")
	}
}

// testCreateUserIfNotExistsIdempotency verifies CreateUserIfNotExists only
// inserts once: a second call for the same username reports zero rows
// affected and leaves the original row untouched.
func testCreateUserIfNotExistsIdempotency(t *testing.T, q db.Querier) {
	ctx := context.Background()

	n, err := q.CreateUserIfNotExists(ctx, db.CreateUserIfNotExistsParams{
		Username:     "bootstrap",
		PasswordHash: "hash-first",
		Role:         db.UserRoleADMIN,
	})
	if err != nil {
		t.Fatalf("CreateUserIfNotExists (first): %v", err)
	}
	if n != 1 {
		t.Fatalf("CreateUserIfNotExists (first): got %d rows affected, want 1", n)
	}

	n, err = q.CreateUserIfNotExists(ctx, db.CreateUserIfNotExistsParams{
		Username:     "bootstrap",
		PasswordHash: "hash-second",
		Role:         db.UserRoleBASIC,
	})
	if err != nil {
		t.Fatalf("CreateUserIfNotExists (second): %v", err)
	}
	if n != 0 {
		t.Fatalf("CreateUserIfNotExists (second): got %d rows affected, want 0", n)
	}

	got, err := q.GetUserByUsername(ctx, "bootstrap")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if got.PasswordHash != "hash-first" || got.Role != db.UserRoleADMIN {
		t.Fatalf("CreateUserIfNotExists (second) overwrote the existing row: got %+v", got)
	}
}

// testViolations exercises the unique, foreign key and check constraint
// scenarios the errors.go helpers must recognize on every engine.
func testViolations(t *testing.T, q db.Querier) {
	ctx := context.Background()

	t.Run("UniqueSlug", func(t *testing.T) {
		tenant, err := q.CreateTenant(ctx, db.CreateTenantParams{Name: "Tenant", ApiKey: "key-violation-slug"})
		if err != nil {
			t.Fatalf("CreateTenant: %v", err)
		}
		content := rawJSON(t, map[string]any{})
		if _, err := q.CreateForm(ctx, db.CreateFormParams{
			TenantID: tenant.ID, Title: "First", Slug: "duplicate-slug",
			IsActive: boolPtr(true), FormContent: content,
			PublicAvailable: true, AcceptAnonymous: true, IsDraft: false,
		}); err != nil {
			t.Fatalf("CreateForm (first): %v", err)
		}
		_, err = q.CreateForm(ctx, db.CreateFormParams{
			TenantID: tenant.ID, Title: "Second", Slug: "duplicate-slug",
			IsActive: boolPtr(true), FormContent: content,
			PublicAvailable: true, AcceptAnonymous: true, IsDraft: false,
		})
		if err == nil {
			t.Fatal("CreateForm (duplicate slug): expected an error")
		}
		if !IsUniqueViolation(err) {
			t.Fatalf("CreateForm (duplicate slug): expected IsUniqueViolation, got %v", err)
		}
	})

	t.Run("ForeignKeyMissingTenant", func(t *testing.T) {
		content := rawJSON(t, map[string]any{})
		_, err := q.CreateForm(ctx, db.CreateFormParams{
			TenantID: 999_999_999, Title: "Orphan", Slug: "orphan-form-fk",
			IsActive: boolPtr(true), FormContent: content,
			PublicAvailable: true, AcceptAnonymous: true, IsDraft: false,
		})
		if err == nil {
			t.Fatal("CreateForm (missing tenant): expected an error")
		}
		if !IsForeignKeyViolation(err) {
			t.Fatalf("CreateForm (missing tenant): expected IsForeignKeyViolation, got %v", err)
		}
	})

	t.Run("CheckPublicFormMustAcceptAnonymous", func(t *testing.T) {
		tenant, err := q.CreateTenant(ctx, db.CreateTenantParams{Name: "Tenant", ApiKey: "key-violation-check-public"})
		if err != nil {
			t.Fatalf("CreateTenant: %v", err)
		}
		content := rawJSON(t, map[string]any{})
		_, err = q.CreateForm(ctx, db.CreateFormParams{
			TenantID: tenant.ID, Title: "Bad Public Form", Slug: "bad-public-form",
			IsActive: boolPtr(true), FormContent: content,
			PublicAvailable: true, AcceptAnonymous: false, IsDraft: false,
		})
		if err == nil {
			t.Fatal("CreateForm (public without anonymous): expected an error")
		}
		if !IsCheckViolation(err) {
			t.Fatalf("CreateForm (public without anonymous): expected IsCheckViolation, got %v", err)
		}
	})

	t.Run("CheckUsernameMustBeLowercase", func(t *testing.T) {
		_, err := q.CreateUser(ctx, db.CreateUserParams{
			Username:     "Uppercase",
			PasswordHash: "hash",
			Role:         db.UserRoleBASIC,
		})
		if err == nil {
			t.Fatal("CreateUser (uppercase username): expected an error")
		}
		if !IsCheckViolation(err) {
			t.Fatalf("CreateUser (uppercase username): expected IsCheckViolation, got %v", err)
		}
	})
}
