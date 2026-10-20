package tenants

import (
	"context"
	stderrors "errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"app/internal/db"
	apperrors "app/internal/errors"
)

// fakeQuerier implements db.Querier with per-method stubs. Calling a method
// without a stub panics through the nil embedded interface.
type fakeQuerier struct {
	db.Querier
	createTenant      func(db.CreateTenantParams) (db.Tenant, error)
	getTenantByAPIKey func(string) (db.Tenant, error)
	getTenantByID     func(int32) (db.Tenant, error)
	getTenantByOwner  func(int32) (db.Tenant, error)
	rotateAPIKey      func(db.RotateTenantAPIKeyParams) (db.Tenant, error)
	getByCustomer     func(string) (db.Tenant, error)
	setCustomer       func(db.SetTenantStripeCustomerParams) (db.Tenant, error)
	updateSub         func(db.UpdateTenantSubscriptionByCustomerParams) (db.Tenant, error)
	linkSub           func(db.LinkTenantSubscriptionParams) (db.Tenant, error)
}

func (f *fakeQuerier) CreateTenant(_ context.Context, arg db.CreateTenantParams) (db.Tenant, error) {
	return f.createTenant(arg)
}

func (f *fakeQuerier) GetTenantByAPIKey(_ context.Context, apiKey string) (db.Tenant, error) {
	return f.getTenantByAPIKey(apiKey)
}

func (f *fakeQuerier) GetTenantByID(_ context.Context, id int32) (db.Tenant, error) {
	return f.getTenantByID(id)
}

func (f *fakeQuerier) GetTenantByOwner(_ context.Context, ownerUserID int32) (db.Tenant, error) {
	return f.getTenantByOwner(ownerUserID)
}

func (f *fakeQuerier) RotateTenantAPIKey(_ context.Context, arg db.RotateTenantAPIKeyParams) (db.Tenant, error) {
	return f.rotateAPIKey(arg)
}

func (f *fakeQuerier) GetTenantByStripeCustomerID(_ context.Context, customerID string) (db.Tenant, error) {
	return f.getByCustomer(customerID)
}

func (f *fakeQuerier) SetTenantStripeCustomer(_ context.Context, arg db.SetTenantStripeCustomerParams) (db.Tenant, error) {
	return f.setCustomer(arg)
}

func (f *fakeQuerier) LinkTenantSubscription(_ context.Context, arg db.LinkTenantSubscriptionParams) (db.Tenant, error) {
	return f.linkSub(arg)
}

func (f *fakeQuerier) UpdateTenantSubscriptionByCustomer(_ context.Context, arg db.UpdateTenantSubscriptionByCustomerParams) (db.Tenant, error) {
	return f.updateSub(arg)
}

var (
	errDB     = stderrors.New("connection reset")
	errUnique = &pgconn.PgError{Code: "23505"}
	errOwner  = &pgconn.PgError{Code: "23505", ConstraintName: "tenants_owner_user_id_key"}

	now      = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	active   = true
	dbTenant = db.Tenant{ID: 3, Name: "Acme", ApiKey: "key-1", IsActive: &active, CreatedAt: &now}
	tenant   = Tenant{ID: 3, Name: "Acme", APIKey: "key-1", IsActive: true, CreatedAt: now}

	ownerID       int32 = 9
	dbOwnedTenant       = db.Tenant{ID: 3, Name: "Acme", ApiKey: "key-1", IsActive: &active, CreatedAt: &now, OwnerUserID: &ownerID}
	ownedTenant         = Tenant{ID: 3, Name: "Acme", APIKey: "key-1", IsActive: true, CreatedAt: now, OwnerUserID: &ownerID}

	customerID     = "cus_123"
	subscriptionID = "sub_456"
	statusActive   = "active"
	periodEnd      = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	dbBilledTenant = db.Tenant{
		ID: 3, Name: "Acme", ApiKey: "key-1", IsActive: &active, CreatedAt: &now, OwnerUserID: &ownerID,
		Plan: db.TenantPlanPro, StripeCustomerID: &customerID, StripeSubscriptionID: &subscriptionID,
		SubscriptionStatus: &statusActive, CurrentPeriodEnd: &periodEnd,
	}
	billedTenant = Tenant{
		ID: 3, Name: "Acme", APIKey: "key-1", IsActive: true, CreatedAt: now, OwnerUserID: &ownerID,
		Plan: PlanPro, StripeCustomerID: customerID, StripeSubscriptionID: subscriptionID,
		SubscriptionStatus: statusActive, CurrentPeriodEnd: &periodEnd,
	}
)

// assertCode fails unless err is an AppError with the given code; a zero code
// asserts err is nil.
func assertCode(t *testing.T, err error, want apperrors.Code) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		return
	}
	appErr, ok := apperrors.As(err)
	if !ok {
		t.Fatalf("err = %v, want AppError %q", err, want)
	}
	if appErr.Code != want {
		t.Fatalf("code = %q, want %q (err: %v)", appErr.Code, want, err)
	}
}

func TestCreate(t *testing.T) {
	tests := []struct {
		name     string
		owner    *int32
		row      db.Tenant
		err      error
		wantCode apperrors.Code
		wantMsg  string
		want     Tenant
	}{
		{"ok", nil, dbTenant, nil, "", "", tenant},
		{"ok with owner", &ownerID, dbOwnedTenant, nil, "", "", ownedTenant},
		{"api key taken", nil, db.Tenant{}, errUnique, apperrors.CodeConflict, msgAPIKeyUsed, Tenant{}},
		{"owner has a tenant", &ownerID, db.Tenant{}, errOwner, apperrors.CodeConflict, msgOwnerTaken, Tenant{}},
		{"db failure", nil, db.Tenant{}, errDB, apperrors.CodeInternal, "", Tenant{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{createTenant: func(arg db.CreateTenantParams) (db.Tenant, error) {
				if want := (db.CreateTenantParams{Name: "Acme", ApiKey: "key-1", OwnerUserID: tt.owner}); !reflect.DeepEqual(arg, want) {
					t.Fatalf("params = %+v, want %+v", arg, want)
				}
				return tt.row, tt.err
			}})

			got, err := svc.Create(context.Background(), CreateTenantInput{Name: "Acme", APIKey: "key-1", OwnerUserID: tt.owner})

			assertCode(t, err, tt.wantCode)
			if tt.wantMsg != "" {
				if appErr, _ := apperrors.As(err); appErr.Message != tt.wantMsg {
					t.Fatalf("message = %q, want %q", appErr.Message, tt.wantMsg)
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("tenant = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestGetByAPIKey(t *testing.T) {
	tests := []struct {
		name     string
		row      db.Tenant
		err      error
		wantCode apperrors.Code
		want     Tenant
	}{
		{"ok", dbTenant, nil, "", tenant},
		{"null columns", db.Tenant{ID: 3, Name: "Acme", ApiKey: "key-1"}, nil, "", Tenant{ID: 3, Name: "Acme", APIKey: "key-1"}},
		{"unknown or inactive", db.Tenant{}, pgx.ErrNoRows, apperrors.CodeNotFound, Tenant{}},
		{"key mismatch", db.Tenant{ID: 3, Name: "Acme", ApiKey: "KEY-1"}, nil, apperrors.CodeNotFound, Tenant{}},
		{"db failure", db.Tenant{}, errDB, apperrors.CodeInternal, Tenant{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{getTenantByAPIKey: func(apiKey string) (db.Tenant, error) {
				if apiKey != "key-1" {
					t.Fatalf("apiKey = %q", apiKey)
				}
				return tt.row, tt.err
			}})

			got, err := svc.GetByAPIKey(context.Background(), "key-1")

			assertCode(t, err, tt.wantCode)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("tenant = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// lookupCases are the shared outcomes of the single-tenant lookups.
var lookupCases = []struct {
	name     string
	row      db.Tenant
	err      error
	wantCode apperrors.Code
	want     Tenant
}{
	{"ok", dbOwnedTenant, nil, "", ownedTenant},
	{"unknown or inactive", db.Tenant{}, pgx.ErrNoRows, apperrors.CodeNotFound, Tenant{}},
	{"db failure", db.Tenant{}, errDB, apperrors.CodeInternal, Tenant{}},
}

func TestGetByID(t *testing.T) {
	for _, tt := range lookupCases {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{getTenantByID: func(id int32) (db.Tenant, error) {
				if id != 3 {
					t.Fatalf("id = %d", id)
				}
				return tt.row, tt.err
			}})

			got, err := svc.GetByID(context.Background(), 3)

			assertCode(t, err, tt.wantCode)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("tenant = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestGetByOwner(t *testing.T) {
	for _, tt := range lookupCases {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{getTenantByOwner: func(userID int32) (db.Tenant, error) {
				if userID != ownerID {
					t.Fatalf("userID = %d", userID)
				}
				return tt.row, tt.err
			}})

			got, err := svc.GetByOwner(context.Background(), ownerID)

			assertCode(t, err, tt.wantCode)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("tenant = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRotateAPIKey(t *testing.T) {
	rotatedRow := dbOwnedTenant
	rotatedRow.ApiKey = "key-2"
	rotated := ownedTenant
	rotated.APIKey = "key-2"

	tests := []struct {
		name     string
		row      db.Tenant
		err      error
		wantCode apperrors.Code
		want     Tenant
	}{
		{"ok", rotatedRow, nil, "", rotated},
		{"unknown or inactive", db.Tenant{}, pgx.ErrNoRows, apperrors.CodeNotFound, Tenant{}},
		{"key taken", db.Tenant{}, errUnique, apperrors.CodeConflict, Tenant{}},
		{"db failure", db.Tenant{}, errDB, apperrors.CodeInternal, Tenant{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{rotateAPIKey: func(arg db.RotateTenantAPIKeyParams) (db.Tenant, error) {
				if want := (db.RotateTenantAPIKeyParams{ID: 3, NewKey: "key-2"}); arg != want {
					t.Fatalf("params = %+v, want %+v", arg, want)
				}
				return tt.row, tt.err
			}})

			got, err := svc.RotateAPIKey(context.Background(), 3, "key-2")

			assertCode(t, err, tt.wantCode)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("tenant = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestGetByStripeCustomerID(t *testing.T) {
	tests := []struct {
		name     string
		row      db.Tenant
		err      error
		wantCode apperrors.Code
		want     Tenant
	}{
		{"ok", dbBilledTenant, nil, "", billedTenant},
		{"free tenant", db.Tenant{ID: 3, Name: "Acme", ApiKey: "key-1", Plan: db.TenantPlanFree, StripeCustomerID: &customerID}, nil, "",
			Tenant{ID: 3, Name: "Acme", APIKey: "key-1", Plan: PlanFree, StripeCustomerID: customerID}},
		{"unknown or inactive", db.Tenant{}, pgx.ErrNoRows, apperrors.CodeNotFound, Tenant{}},
		{"db failure", db.Tenant{}, errDB, apperrors.CodeInternal, Tenant{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{getByCustomer: func(id string) (db.Tenant, error) {
				if id != customerID {
					t.Fatalf("customerID = %q", id)
				}
				return tt.row, tt.err
			}})

			got, err := svc.GetByStripeCustomerID(context.Background(), customerID)

			assertCode(t, err, tt.wantCode)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("tenant = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSetStripeCustomer(t *testing.T) {
	linkedRow := dbOwnedTenant
	linkedRow.Plan = db.TenantPlanFree
	linkedRow.StripeCustomerID = &customerID
	linked := ownedTenant
	linked.Plan = PlanFree
	linked.StripeCustomerID = customerID

	tests := []struct {
		name     string
		row      db.Tenant
		err      error
		wantCode apperrors.Code
		wantMsg  string
		want     Tenant
	}{
		{"ok", linkedRow, nil, "", "", linked},
		{"unknown or inactive", db.Tenant{}, pgx.ErrNoRows, apperrors.CodeNotFound, "", Tenant{}},
		{"customer taken", db.Tenant{}, errUnique, apperrors.CodeConflict, msgCustomer, Tenant{}},
		{"db failure", db.Tenant{}, errDB, apperrors.CodeInternal, "", Tenant{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{setCustomer: func(arg db.SetTenantStripeCustomerParams) (db.Tenant, error) {
				if want := (db.SetTenantStripeCustomerParams{ID: 3, StripeCustomerID: customerID}); arg != want {
					t.Fatalf("params = %+v, want %+v", arg, want)
				}
				return tt.row, tt.err
			}})

			got, err := svc.SetStripeCustomer(context.Background(), 3, customerID)

			assertCode(t, err, tt.wantCode)
			if tt.wantMsg != "" {
				if appErr, _ := apperrors.As(err); appErr.Message != tt.wantMsg {
					t.Fatalf("message = %q, want %q", appErr.Message, tt.wantMsg)
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("tenant = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestUpdateSubscription(t *testing.T) {
	canceled := "canceled"
	canceledRow := dbBilledTenant
	canceledRow.Plan = db.TenantPlanFree
	canceledRow.SubscriptionStatus = &canceled
	canceledTenant := billedTenant
	canceledTenant.Plan = PlanFree
	canceledTenant.SubscriptionStatus = canceled

	clearedRow := dbBilledTenant
	clearedRow.Plan = db.TenantPlanFree
	clearedRow.StripeSubscriptionID, clearedRow.SubscriptionStatus, clearedRow.CurrentPeriodEnd = nil, nil, nil
	clearedTenant := billedTenant
	clearedTenant.Plan = PlanFree
	clearedTenant.StripeSubscriptionID, clearedTenant.SubscriptionStatus, clearedTenant.CurrentPeriodEnd = "", "", nil

	tests := []struct {
		name       string
		in         SubscriptionUpdate
		wantParams db.UpdateTenantSubscriptionByCustomerParams
		row        db.Tenant
		err        error
		wantCode   apperrors.Code
		want       Tenant
	}{
		{
			"active",
			SubscriptionUpdate{CustomerID: customerID, SubscriptionID: subscriptionID, Status: statusActive, CurrentPeriodEnd: &periodEnd, Plan: PlanPro, EventAt: now},
			db.UpdateTenantSubscriptionByCustomerParams{StripeCustomerID: customerID, StripeSubscriptionID: &subscriptionID, SubscriptionStatus: &statusActive, CurrentPeriodEnd: &periodEnd, EventAt: now, Plan: db.TenantPlanPro},
			dbBilledTenant, nil, "", billedTenant,
		},
		{
			"canceled",
			SubscriptionUpdate{CustomerID: customerID, SubscriptionID: subscriptionID, Status: canceled, CurrentPeriodEnd: &periodEnd, Plan: PlanFree},
			db.UpdateTenantSubscriptionByCustomerParams{StripeCustomerID: customerID, StripeSubscriptionID: &subscriptionID, SubscriptionStatus: &canceled, CurrentPeriodEnd: &periodEnd, Plan: db.TenantPlanFree},
			canceledRow, nil, "", canceledTenant,
		},
		{
			"empty fields clear columns",
			SubscriptionUpdate{CustomerID: customerID, Plan: PlanFree},
			db.UpdateTenantSubscriptionByCustomerParams{StripeCustomerID: customerID, Plan: db.TenantPlanFree},
			clearedRow, nil, "", clearedTenant,
		},
		{
			"unknown customer or stale update",
			SubscriptionUpdate{CustomerID: customerID, Plan: PlanFree},
			db.UpdateTenantSubscriptionByCustomerParams{StripeCustomerID: customerID, Plan: db.TenantPlanFree},
			db.Tenant{}, pgx.ErrNoRows, apperrors.CodeNotFound, Tenant{},
		},
		{
			"db failure",
			SubscriptionUpdate{CustomerID: customerID, Plan: PlanFree},
			db.UpdateTenantSubscriptionByCustomerParams{StripeCustomerID: customerID, Plan: db.TenantPlanFree},
			db.Tenant{}, errDB, apperrors.CodeInternal, Tenant{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{updateSub: func(arg db.UpdateTenantSubscriptionByCustomerParams) (db.Tenant, error) {
				if !reflect.DeepEqual(arg, tt.wantParams) {
					t.Fatalf("params = %+v, want %+v", arg, tt.wantParams)
				}
				return tt.row, tt.err
			}})

			got, err := svc.UpdateSubscription(context.Background(), tt.in)

			assertCode(t, err, tt.wantCode)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("tenant = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLinkSubscription(t *testing.T) {
	tests := []struct {
		name     string
		row      db.Tenant
		err      error
		wantCode apperrors.Code
		want     Tenant
	}{
		{"ok", dbBilledTenant, nil, "", billedTenant},
		{"unknown customer", db.Tenant{}, pgx.ErrNoRows, apperrors.CodeNotFound, Tenant{}},
		{"db failure", db.Tenant{}, errDB, apperrors.CodeInternal, Tenant{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{linkSub: func(arg db.LinkTenantSubscriptionParams) (db.Tenant, error) {
				if want := (db.LinkTenantSubscriptionParams{StripeCustomerID: customerID, StripeSubscriptionID: subscriptionID}); arg != want {
					t.Fatalf("params = %+v, want %+v", arg, want)
				}
				return tt.row, tt.err
			}})

			got, err := svc.LinkSubscription(context.Background(), customerID, subscriptionID)

			assertCode(t, err, tt.wantCode)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("tenant = %+v, want %+v", got, tt.want)
			}
		})
	}
}
