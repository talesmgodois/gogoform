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
