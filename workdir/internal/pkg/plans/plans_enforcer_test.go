package plans

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/forms"
	"app/internal/pkg/tenants"
)

// fakeUsage implements every port with canned counts and records the
// arguments it was called with.
type fakeUsage struct {
	forms, submissions, storage int64
	err                         error

	formFilter forms.ListFormsFilter
	since      time.Time
	tenantID   int32
	calls      int
}

func (f *fakeUsage) Count(_ context.Context, filter forms.ListFormsFilter) (int64, error) {
	f.calls++
	f.formFilter = filter
	return f.forms, f.err
}

func (f *fakeUsage) CountByTenantSince(_ context.Context, tenantID int32, since time.Time) (int64, error) {
	f.calls++
	f.tenantID, f.since = tenantID, since
	return f.submissions, f.err
}

func (f *fakeUsage) StorageUsedByTenant(_ context.Context, tenantID int32) (int64, error) {
	f.calls++
	f.tenantID = tenantID
	return f.storage, f.err
}

func newFakeEnforcer(f *fakeUsage) *Enforcer { return NewEnforcer(f, f, f) }

// checkLimitErr fails unless err is nil when wantOK, or a plan_limit_reached
// AppError otherwise.
func checkLimitErr(t *testing.T, err error, wantOK bool) {
	t.Helper()
	if wantOK {
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		return
	}
	appErr, ok := apperrors.As(err)
	if !ok || appErr.Code != apperrors.CodePlanLimitReached {
		t.Fatalf("err = %v, want %s", err, apperrors.CodePlanLimitReached)
	}
	if appErr.Message == "" {
		t.Fatal("limit error has no message")
	}
}

var testNow = time.Date(2026, time.October, 19, 15, 4, 5, 0, time.UTC)

func TestEnforcerCheckFormCreate(t *testing.T) {
	tests := []struct {
		name   string
		plan   tenants.Plan
		forms  int64
		wantOK bool
	}{
		{"free none", tenants.PlanFree, 0, true},
		{"free below", tenants.PlanFree, 2, true},
		{"free at limit", tenants.PlanFree, 3, false},
		{"free above", tenants.PlanFree, 4, false},
		{"unknown plan is free", "enterprise", 3, false},
		{"pro at free limit", tenants.PlanPro, 3, true},
		{"pro many", tenants.PlanPro, 1_000_000, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeUsage{forms: tt.forms}
			err := newFakeEnforcer(f).CheckFormCreate(context.Background(), 7, tt.plan)
			checkLimitErr(t, err, tt.wantOK)
			if f.calls > 0 && f.formFilter != (forms.ListFormsFilter{TenantID: 7}) {
				t.Fatalf("filter = %+v, want all forms of tenant 7", f.formFilter)
			}
		})
	}
}

func TestEnforcerCheckSubmission(t *testing.T) {
	tests := []struct {
		name        string
		plan        tenants.Plan
		submissions int64
		wantOK      bool
	}{
		{"free none", tenants.PlanFree, 0, true},
		{"free below", tenants.PlanFree, 99, true},
		{"free at limit", tenants.PlanFree, 100, false},
		{"free above", tenants.PlanFree, 101, false},
		{"pro at free limit", tenants.PlanPro, 100, true},
		{"pro below", tenants.PlanPro, 9_999, true},
		{"pro at limit", tenants.PlanPro, 10_000, false},
		{"pro above", tenants.PlanPro, 10_001, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeUsage{submissions: tt.submissions}
			err := newFakeEnforcer(f).CheckSubmission(context.Background(), 7, tt.plan, testNow)
			checkLimitErr(t, err, tt.wantOK)
			if f.tenantID != 7 || !f.since.Equal(MonthStart(testNow)) {
				t.Fatalf("counted tenant %d since %v, want 7 since %v", f.tenantID, f.since, MonthStart(testNow))
			}
		})
	}
}

func TestEnforcerCheckUpload(t *testing.T) {
	free, pro := For(tenants.PlanFree).MaxStorageBytes, For(tenants.PlanPro).MaxStorageBytes
	tests := []struct {
		name   string
		plan   tenants.Plan
		used   int64
		size   int64
		wantOK bool
	}{
		{"free empty", tenants.PlanFree, 0, 1024, true},
		{"free lands on limit", tenants.PlanFree, free - 10, 10, true},
		{"free one byte over", tenants.PlanFree, free - 10, 11, false},
		{"free already full", tenants.PlanFree, free, 1, false},
		{"free single file over", tenants.PlanFree, 0, free + 1, false},
		{"pro above free limit", tenants.PlanPro, free, 1, true},
		{"pro lands on limit", tenants.PlanPro, pro - 10, 10, true},
		{"pro one byte over", tenants.PlanPro, pro - 10, 11, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeUsage{storage: tt.used}
			err := newFakeEnforcer(f).CheckUpload(context.Background(), 7, tt.plan, tt.size)
			checkLimitErr(t, err, tt.wantOK)
			if f.tenantID != 7 {
				t.Fatalf("measured tenant %d, want 7", f.tenantID)
			}
		})
	}
}

func TestEnforcerPortError(t *testing.T) {
	portErr := apperrors.NewInternal(stderrors.New("db down"))
	e := newFakeEnforcer(&fakeUsage{err: portErr})
	ctx := context.Background()
	checks := map[string]error{
		"form create": e.CheckFormCreate(ctx, 7, tenants.PlanFree),
		"submission":  e.CheckSubmission(ctx, 7, tenants.PlanFree, testNow),
		"upload":      e.CheckUpload(ctx, 7, tenants.PlanFree, 1),
	}
	for name, err := range checks {
		if !stderrors.Is(err, portErr) {
			t.Errorf("%s: err = %v, want port error", name, err)
		}
	}
	if _, err := e.Usage(ctx, 7, tenants.PlanFree, testNow); !stderrors.Is(err, portErr) {
		t.Errorf("usage: err = %v, want port error", err)
	}
}

func TestEnforcerUsage(t *testing.T) {
	tests := []struct {
		name string
		plan tenants.Plan
		f    fakeUsage
	}{
		{"free at limits", tenants.PlanFree, fakeUsage{forms: 3, submissions: 100, storage: 100 << 20}},
		{"pro", tenants.PlanPro, fakeUsage{forms: 42, submissions: 5_000, storage: 1 << 30}},
		{"empty", tenants.PlanFree, fakeUsage{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.f
			got, err := newFakeEnforcer(&f).Usage(context.Background(), 7, tt.plan, testNow)
			if err != nil {
				t.Fatalf("Usage: %v", err)
			}
			want := Usage{
				Limits:               For(tt.plan),
				Forms:                tt.f.forms,
				SubmissionsThisMonth: tt.f.submissions,
				StorageBytes:         tt.f.storage,
			}
			if got != want {
				t.Fatalf("Usage = %+v, want %+v", got, want)
			}
			if f.tenantID != 7 || f.formFilter.TenantID != 7 || !f.since.Equal(MonthStart(testNow)) {
				t.Fatalf("queried tenant %d/%d since %v", f.tenantID, f.formFilter.TenantID, f.since)
			}
		})
	}
}
