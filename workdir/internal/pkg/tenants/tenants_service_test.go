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
}

func (f *fakeQuerier) CreateTenant(_ context.Context, arg db.CreateTenantParams) (db.Tenant, error) {
	return f.createTenant(arg)
}

func (f *fakeQuerier) GetTenantByAPIKey(_ context.Context, apiKey string) (db.Tenant, error) {
	return f.getTenantByAPIKey(apiKey)
}

var (
	errDB     = stderrors.New("connection reset")
	errUnique = &pgconn.PgError{Code: "23505"}

	now      = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	active   = true
	dbTenant = db.Tenant{ID: 3, Name: "Acme", ApiKey: "key-1", IsActive: &active, CreatedAt: &now}
	tenant   = Tenant{ID: 3, Name: "Acme", APIKey: "key-1", IsActive: true, CreatedAt: now}
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
		err      error
		wantCode apperrors.Code
	}{
		{"ok", nil, ""},
		{"api key taken", errUnique, apperrors.CodeConflict},
		{"db failure", errDB, apperrors.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{createTenant: func(arg db.CreateTenantParams) (db.Tenant, error) {
				if want := (db.CreateTenantParams{Name: "Acme", ApiKey: "key-1"}); arg != want {
					t.Fatalf("params = %+v, want %+v", arg, want)
				}
				return dbTenant, tt.err
			}})

			got, err := svc.Create(context.Background(), CreateTenantInput{Name: "Acme", APIKey: "key-1"})

			assertCode(t, err, tt.wantCode)
			if err == nil && !reflect.DeepEqual(got, tenant) {
				t.Fatalf("tenant = %+v, want %+v", got, tenant)
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
