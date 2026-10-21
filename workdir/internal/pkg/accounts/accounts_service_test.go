package accounts

import (
	"context"
	"testing"

	"app/internal/database"
	"app/internal/db"
	apperrors "app/internal/errors"
	"app/internal/pkg/auth"
	"app/internal/pkg/tenants"
)

// recorder logs the calls of every fake in order, to check that the
// subscription is cancelled before anything is deleted.
type recorder struct {
	calls []string
}

// fakeWorkspaces returns tenant for its owner, NotFound otherwise.
type fakeWorkspaces struct {
	tenant *tenants.Tenant
}

func (f fakeWorkspaces) GetByOwner(_ context.Context, userID int32) (tenants.Tenant, error) {
	if f.tenant == nil || f.tenant.OwnerUserID == nil || *f.tenant.OwnerUserID != userID {
		return tenants.Tenant{}, apperrors.NewNotFound("tenant not found")
	}
	return *f.tenant, nil
}

type fakeSubscriptions struct {
	rec *recorder
	err error
}

func (f fakeSubscriptions) CancelSubscription(_ context.Context, t tenants.Tenant) error {
	if f.err != nil {
		return f.err
	}
	if t.StripeSubscriptionID != "" {
		f.rec.calls = append(f.rec.calls, "cancel:"+t.StripeSubscriptionID)
	}
	return nil
}

type fakeStore struct {
	rec *recorder
}

func (f fakeStore) DeleteTenantData(_ context.Context, tenantID int32) error {
	f.rec.calls = append(f.rec.calls, "delete-tenant")
	return nil
}

func (f fakeStore) DeleteUser(_ context.Context, userID int32) error {
	f.rec.calls = append(f.rec.calls, "delete-user")
	return nil
}

var testUser = auth.User{ID: 7, Username: "ana@example.com", Email: "ana@example.com"}

func newTestService(t *testing.T, tenant *tenants.Tenant, subs Subscriptions, rec *recorder) (*Service, *database.FakeTxRunner) {
	t.Helper()
	tx := &database.FakeTxRunner{}
	svc, err := NewService(fakeWorkspaces{tenant: tenant}, Options{
		Tx:            tx,
		StoreFor:      func(db.Querier) Store { return fakeStore{rec: rec} },
		Subscriptions: subs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, tx
}

func proTenant() *tenants.Tenant {
	owner := testUser.ID
	return &tenants.Tenant{ID: 3, OwnerUserID: &owner, Plan: tenants.PlanPro, StripeSubscriptionID: "sub_1", SubscriptionStatus: "active"}
}

func assertCode(t *testing.T, err error, want apperrors.Code) {
	t.Helper()
	appErr, ok := apperrors.As(err)
	if !ok || appErr.Code != want {
		t.Fatalf("err = %v, want code %s", err, want)
	}
}

func assertCalls(t *testing.T, rec *recorder, want ...string) {
	t.Helper()
	if len(rec.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", rec.calls, want)
	}
	for i := range want {
		if rec.calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", rec.calls, want)
		}
	}
}

func TestDeleteAccountCancelsThenDeletes(t *testing.T) {
	rec := &recorder{}
	svc, tx := newTestService(t, proTenant(), fakeSubscriptions{rec: rec}, rec)
	if err := svc.DeleteAccount(context.Background(), testUser, "  Ana@Example.com "); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	assertCalls(t, rec, "cancel:sub_1", "delete-tenant", "delete-user")
	if tx.Commits != 1 {
		t.Fatalf("commits = %d, want 1", tx.Commits)
	}
}

func TestDeleteAccountWrongConfirmation(t *testing.T) {
	rec := &recorder{}
	svc, tx := newTestService(t, proTenant(), fakeSubscriptions{rec: rec}, rec)
	assertCode(t, svc.DeleteAccount(context.Background(), testUser, "someone@example.com"), apperrors.CodeInvalidArgument)
	assertCalls(t, rec)
	if tx.Calls != 0 {
		t.Fatalf("tx calls = %d, want 0", tx.Calls)
	}
}

func TestDeleteAccountCancelFailureDeletesNothing(t *testing.T) {
	rec := &recorder{}
	subs := fakeSubscriptions{rec: rec, err: apperrors.New(apperrors.CodeInternal, "stripe down")}
	svc, tx := newTestService(t, proTenant(), subs, rec)
	assertCode(t, svc.DeleteAccount(context.Background(), testUser, testUser.Email), apperrors.CodeInternal)
	assertCalls(t, rec)
	if tx.Calls != 0 {
		t.Fatalf("tx calls = %d, want 0", tx.Calls)
	}
}

func TestDeleteAccountFreeTenant(t *testing.T) {
	owner := testUser.ID
	free := &tenants.Tenant{ID: 3, OwnerUserID: &owner, Plan: tenants.PlanFree}
	rec := &recorder{}
	// Without billing configured, a tenant that never subscribed is deleted.
	svc, _ := newTestService(t, free, nil, rec)
	if err := svc.DeleteAccount(context.Background(), testUser, testUser.Email); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	assertCalls(t, rec, "delete-tenant", "delete-user")
}

func TestDeleteAccountWithoutBillingRefusesOngoingSubscription(t *testing.T) {
	rec := &recorder{}
	svc, _ := newTestService(t, proTenant(), nil, rec)
	assertCode(t, svc.DeleteAccount(context.Background(), testUser, testUser.Email), apperrors.CodeConflict)
	assertCalls(t, rec)
}

func TestDeleteAccountWithoutWorkspace(t *testing.T) {
	rec := &recorder{}
	svc, _ := newTestService(t, nil, fakeSubscriptions{rec: rec}, rec)
	if err := svc.DeleteAccount(context.Background(), testUser, testUser.Email); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	assertCalls(t, rec, "delete-user")
}

func TestDeleteAccountUsernameConfirmation(t *testing.T) {
	rec := &recorder{}
	svc, _ := newTestService(t, nil, nil, rec)
	admin := auth.User{ID: 1, Username: "admin"}
	if err := svc.DeleteAccount(context.Background(), admin, "admin"); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	assertCalls(t, rec, "delete-user")
}

// fakeQuerier records the deletion queries run by SQLStore.
type fakeQuerier struct {
	db.Querier
	rec *recorder
}

func (f fakeQuerier) log(name string) error { f.rec.calls = append(f.rec.calls, name); return nil }

func (f fakeQuerier) DeleteSubmissionMetadataByTenant(context.Context, int32) error {
	return f.log("metadata")
}
func (f fakeQuerier) DeleteSubmissionsByTenant(context.Context, int32) error {
	return f.log("submissions")
}
func (f fakeQuerier) DeleteWebhooksByTenant(context.Context, int32) error { return f.log("webhooks") }
func (f fakeQuerier) DeleteFormsByTenant(context.Context, int32) error    { return f.log("forms") }
func (f fakeQuerier) DeleteFilesByTenant(context.Context, int32) error    { return f.log("files") }
func (f fakeQuerier) DeleteTenant(context.Context, int32) error           { return f.log("tenant") }
func (f fakeQuerier) ClearSubmissionsUser(context.Context, *int32) error  { return f.log("clear-user") }
func (f fakeQuerier) DeleteUser(context.Context, int32) error             { return f.log("user") }

func TestSQLStoreDeletesChildFirst(t *testing.T) {
	rec := &recorder{}
	store := NewStore(fakeQuerier{rec: rec})
	if err := store.DeleteTenantData(context.Background(), 3); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteUser(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	assertCalls(t, rec, "metadata", "submissions", "webhooks", "forms", "files", "tenant", "clear-user", "user")
}
