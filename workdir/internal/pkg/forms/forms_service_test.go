package forms

import (
	"context"
	"encoding/json"
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
	createForm          func(db.CreateFormParams) (db.Form, error)
	getFormByID         func(db.GetFormByIDParams) (db.GetFormByIDRow, error)
	getPublicFormBySlug func(string) (db.Form, error)
	listFormsByTenant   func(db.ListFormsByTenantParams) ([]db.ListFormsByTenantRow, error)
	countFormsByTenant  func(db.CountFormsByTenantParams) (int64, error)
	listAllForms        func(db.ListAllFormsParams) ([]db.ListAllFormsRow, error)
	countAllForms       func() (int64, error)
	updateForm          func(db.UpdateFormParams) (db.Form, error)
	deleteForm          func(db.DeleteFormParams) (int64, error)
}

func (f *fakeQuerier) CreateForm(_ context.Context, arg db.CreateFormParams) (db.Form, error) {
	return f.createForm(arg)
}

func (f *fakeQuerier) GetFormByID(_ context.Context, arg db.GetFormByIDParams) (db.GetFormByIDRow, error) {
	return f.getFormByID(arg)
}

func (f *fakeQuerier) GetPublicFormBySlug(_ context.Context, slug string) (db.Form, error) {
	return f.getPublicFormBySlug(slug)
}

func (f *fakeQuerier) ListFormsByTenant(_ context.Context, arg db.ListFormsByTenantParams) ([]db.ListFormsByTenantRow, error) {
	return f.listFormsByTenant(arg)
}

func (f *fakeQuerier) CountFormsByTenant(_ context.Context, arg db.CountFormsByTenantParams) (int64, error) {
	return f.countFormsByTenant(arg)
}

func (f *fakeQuerier) ListAllForms(_ context.Context, arg db.ListAllFormsParams) ([]db.ListAllFormsRow, error) {
	return f.listAllForms(arg)
}

func (f *fakeQuerier) CountAllForms(context.Context) (int64, error) {
	return f.countAllForms()
}

func (f *fakeQuerier) UpdateForm(_ context.Context, arg db.UpdateFormParams) (db.Form, error) {
	return f.updateForm(arg)
}

func (f *fakeQuerier) DeleteForm(_ context.Context, arg db.DeleteFormParams) (int64, error) {
	return f.deleteForm(arg)
}

var (
	errDB         = stderrors.New("connection reset")
	errUnique     = &pgconn.PgError{Code: "23505"}
	errForeignKey = &pgconn.PgError{Code: "23503"}

	now     = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	content = json.RawMessage(`{"fields":[]}`)
	desc    = "a form"

	dbForm = db.Form{
		ID: 7, TenantID: 3, Title: "Survey", Slug: "survey", Description: &desc,
		IsActive: ptr(true), StartDate: &now, FormContent: content,
		CreatedAt: &now, UpdatedAt: &now,
	}
	wantForm = Form{
		ID: 7, TenantID: 3, Title: "Survey", Slug: "survey", Description: &desc,
		IsActive: true, StartDate: &now, Content: content,
		CreatedAt: now, UpdatedAt: now,
	}
)

func ptr[T any](v T) *T { return &v }

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
	in := CreateFormInput{
		TenantID: 3, Title: "Survey", Slug: "survey", Description: &desc,
		StartDate: &now, Content: content,
	}
	tests := []struct {
		name     string
		err      error
		wantCode apperrors.Code
	}{
		{"ok", nil, ""},
		{"slug taken", errUnique, apperrors.CodeConflict},
		{"unknown tenant", errForeignKey, apperrors.CodeNotFound},
		{"db failure", errDB, apperrors.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got db.CreateFormParams
			svc := NewService(&fakeQuerier{createForm: func(arg db.CreateFormParams) (db.Form, error) {
				got = arg
				return dbForm, tt.err
			}})

			form, err := svc.Create(context.Background(), in)

			assertCode(t, err, tt.wantCode)
			want := db.CreateFormParams{
				TenantID: 3, Title: "Survey", Slug: "survey", Description: &desc,
				IsActive: ptr(true), StartDate: &now, FormContent: content,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("params = %+v, want %+v", got, want)
			}
			if err == nil && !reflect.DeepEqual(form, wantForm) {
				t.Fatalf("form = %+v, want %+v", form, wantForm)
			}
		})
	}
}

func TestCreateIsActive(t *testing.T) {
	tests := []struct {
		name string
		in   *bool
		want bool
	}{
		{"nil defaults to active", nil, true},
		{"explicit true", ptr(true), true},
		{"explicit false", ptr(false), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got *bool
			svc := NewService(&fakeQuerier{createForm: func(arg db.CreateFormParams) (db.Form, error) {
				got = arg.IsActive
				return dbForm, nil
			}})

			if _, err := svc.Create(context.Background(), CreateFormInput{TenantID: 3, IsActive: tt.in}); err != nil {
				t.Fatalf("err = %v", err)
			}
			if got == nil || *got != tt.want {
				t.Fatalf("IsActive = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetByID(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode apperrors.Code
	}{
		{"ok", nil, ""},
		{"missing", pgx.ErrNoRows, apperrors.CodeNotFound},
		{"db failure", errDB, apperrors.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{getFormByID: func(arg db.GetFormByIDParams) (db.GetFormByIDRow, error) {
				if arg != (db.GetFormByIDParams{ID: 7, TenantID: 3}) {
					t.Fatalf("params = %+v", arg)
				}
				return db.GetFormByIDRow{
					ID: 7, TenantID: 3, Title: "Survey", Slug: "survey", Description: &desc,
					IsActive: ptr(true), StartDate: &now, FormContent: content,
					CreatedAt: &now, UpdatedAt: &now, TenantName: "Acme",
				}, tt.err
			}})

			got, err := svc.GetByID(context.Background(), 3, 7)

			assertCode(t, err, tt.wantCode)
			if want := (FormDetails{Form: wantForm, TenantName: "Acme"}); err == nil && !reflect.DeepEqual(got, want) {
				t.Fatalf("form = %+v, want %+v", got, want)
			}
		})
	}
}

func TestGetPublicBySlug(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode apperrors.Code
	}{
		{"ok", nil, ""},
		{"missing or unavailable", pgx.ErrNoRows, apperrors.CodeNotFound},
		{"db failure", errDB, apperrors.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{getPublicFormBySlug: func(slug string) (db.Form, error) {
				if slug != "survey" {
					t.Fatalf("slug = %q", slug)
				}
				return dbForm, tt.err
			}})

			got, err := svc.GetPublicBySlug(context.Background(), "survey")

			assertCode(t, err, tt.wantCode)
			if err == nil && !reflect.DeepEqual(got, wantForm) {
				t.Fatalf("form = %+v, want %+v", got, wantForm)
			}
		})
	}
}

func TestList(t *testing.T) {
	filter := ListFormsFilter{TenantID: 3, IsActive: ptr(true), Search: ptr("sur")}
	row := db.ListFormsByTenantRow{
		ID: 7, TenantID: 3, Title: "Survey", Slug: "survey", Description: &desc,
		IsActive: ptr(true), StartDate: &now, FormContent: content,
		CreatedAt: &now, UpdatedAt: &now, SubmissionCount: 12,
	}
	tests := []struct {
		name     string
		page     Page
		err      error
		wantCode apperrors.Code
		want     []FormSummary
	}{
		{"ok", Page{Offset: 20, Limit: 10}, nil, "", []FormSummary{{Form: wantForm, SubmissionCount: 12}}},
		{"negative offset", Page{Offset: -1, Limit: 10}, nil, apperrors.CodeInvalidArgument, nil},
		{"zero limit", Page{Offset: 0, Limit: 0}, nil, apperrors.CodeInvalidArgument, nil},
		{"db failure", Page{Offset: 0, Limit: 10}, errDB, apperrors.CodeInternal, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{listFormsByTenant: func(arg db.ListFormsByTenantParams) ([]db.ListFormsByTenantRow, error) {
				want := db.ListFormsByTenantParams{
					TenantID: 3, IsActive: filter.IsActive, Search: filter.Search,
					PageOffset: tt.page.Offset, PageLimit: tt.page.Limit,
				}
				if arg != want {
					t.Fatalf("params = %+v, want %+v", arg, want)
				}
				if tt.err != nil {
					return nil, tt.err
				}
				return []db.ListFormsByTenantRow{row}, nil
			}})

			got, err := svc.List(context.Background(), filter, tt.page)

			assertCode(t, err, tt.wantCode)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("forms = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestListEmpty(t *testing.T) {
	svc := NewService(&fakeQuerier{listFormsByTenant: func(db.ListFormsByTenantParams) ([]db.ListFormsByTenantRow, error) {
		return []db.ListFormsByTenantRow{}, nil
	}})

	got, err := svc.List(context.Background(), ListFormsFilter{TenantID: 3}, Page{Limit: 10})

	assertCode(t, err, "")
	if got == nil || len(got) != 0 {
		t.Fatalf("forms = %#v, want empty non-nil slice", got)
	}
}

func TestCount(t *testing.T) {
	filter := ListFormsFilter{TenantID: 3, Search: ptr("sur")}
	tests := []struct {
		name     string
		err      error
		wantCode apperrors.Code
		want     int64
	}{
		{"ok", nil, "", 42},
		{"db failure", errDB, apperrors.CodeInternal, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{countFormsByTenant: func(arg db.CountFormsByTenantParams) (int64, error) {
				if want := (db.CountFormsByTenantParams{TenantID: 3, Search: filter.Search}); arg != want {
					t.Fatalf("params = %+v, want %+v", arg, want)
				}
				if tt.err != nil {
					return 0, tt.err
				}
				return 42, nil
			}})

			got, err := svc.Count(context.Background(), filter)

			assertCode(t, err, tt.wantCode)
			if got != tt.want {
				t.Fatalf("count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestListAll(t *testing.T) {
	row := db.ListAllFormsRow{
		ID: 7, TenantID: 3, Title: "Survey", Slug: "survey", Description: &desc,
		IsActive: ptr(true), StartDate: &now, FormContent: content,
		CreatedAt: &now, UpdatedAt: &now, TenantName: "Acme", SubmissionCount: 12,
	}
	tests := []struct {
		name     string
		page     Page
		err      error
		wantCode apperrors.Code
		want     []FormOverview
	}{
		{"ok", Page{Offset: 20, Limit: 10}, nil, "", []FormOverview{{FormSummary: FormSummary{Form: wantForm, SubmissionCount: 12}, TenantName: "Acme"}}},
		{"negative offset", Page{Offset: -1, Limit: 10}, nil, apperrors.CodeInvalidArgument, nil},
		{"zero limit", Page{Offset: 0, Limit: 0}, nil, apperrors.CodeInvalidArgument, nil},
		{"db failure", Page{Offset: 0, Limit: 10}, errDB, apperrors.CodeInternal, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{listAllForms: func(arg db.ListAllFormsParams) ([]db.ListAllFormsRow, error) {
				if want := (db.ListAllFormsParams{PageOffset: tt.page.Offset, PageLimit: tt.page.Limit}); arg != want {
					t.Fatalf("params = %+v, want %+v", arg, want)
				}
				if tt.err != nil {
					return nil, tt.err
				}
				return []db.ListAllFormsRow{row}, nil
			}})

			got, err := svc.ListAll(context.Background(), tt.page)

			assertCode(t, err, tt.wantCode)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("forms = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCountAll(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode apperrors.Code
		want     int64
	}{
		{"ok", nil, "", 42},
		{"db failure", errDB, apperrors.CodeInternal, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{countAllForms: func() (int64, error) {
				if tt.err != nil {
					return 0, tt.err
				}
				return 42, nil
			}})

			got, err := svc.CountAll(context.Background())

			assertCode(t, err, tt.wantCode)
			if got != tt.want {
				t.Fatalf("count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestUpdate(t *testing.T) {
	in := UpdateFormInput{
		ID: 7, TenantID: 3, Title: "Survey", Slug: "survey", Description: &desc,
		IsActive: false, StartDate: &now, Content: content,
	}
	tests := []struct {
		name     string
		err      error
		wantCode apperrors.Code
	}{
		{"ok", nil, ""},
		{"missing", pgx.ErrNoRows, apperrors.CodeNotFound},
		{"slug taken", errUnique, apperrors.CodeConflict},
		{"db failure", errDB, apperrors.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got db.UpdateFormParams
			svc := NewService(&fakeQuerier{updateForm: func(arg db.UpdateFormParams) (db.Form, error) {
				got = arg
				return dbForm, tt.err
			}})

			form, err := svc.Update(context.Background(), in)

			assertCode(t, err, tt.wantCode)
			want := db.UpdateFormParams{
				ID: 7, TenantID: 3, Title: "Survey", Slug: "survey", Description: &desc,
				IsActive: ptr(false), StartDate: &now, FormContent: content,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("params = %+v, want %+v", got, want)
			}
			if err == nil && !reflect.DeepEqual(form, wantForm) {
				t.Fatalf("form = %+v, want %+v", form, wantForm)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	tests := []struct {
		name     string
		rows     int64
		err      error
		wantCode apperrors.Code
	}{
		{"ok", 1, nil, ""},
		{"missing", 0, nil, apperrors.CodeNotFound},
		{"still referenced", 0, errForeignKey, apperrors.CodeConflict},
		{"db failure", 0, errDB, apperrors.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{deleteForm: func(arg db.DeleteFormParams) (int64, error) {
				if arg != (db.DeleteFormParams{ID: 7, TenantID: 3}) {
					t.Fatalf("params = %+v", arg)
				}
				return tt.rows, tt.err
			}})

			assertCode(t, svc.Delete(context.Background(), 3, 7), tt.wantCode)
		})
	}
}

func TestToFormNullColumns(t *testing.T) {
	got := toForm(db.Form{ID: 1, TenantID: 2, Title: "t", Slug: "s", FormContent: content})

	want := Form{ID: 1, TenantID: 2, Title: "t", Slug: "s", Content: content}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("form = %+v, want %+v", got, want)
	}
}
