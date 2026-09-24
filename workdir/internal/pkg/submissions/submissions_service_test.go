package submissions

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"app/internal/db"
	apperrors "app/internal/errors"
)

// fakeQuerier implements db.Querier with per-method stubs. Calling a method
// without a stub panics through the nil embedded interface.
type fakeQuerier struct {
	db.Querier
	createFormSubmission     func(db.CreateFormSubmissionParams) (db.FormSubmission, error)
	createSubmissionMetadata func(db.CreateSubmissionMetadataParams) (db.SubmissionMetadatum, error)
	listSubmissionsByForm    func(db.ListSubmissionsByFormParams) ([]db.ListSubmissionsByFormRow, error)
}

func (f *fakeQuerier) CreateFormSubmission(_ context.Context, arg db.CreateFormSubmissionParams) (db.FormSubmission, error) {
	return f.createFormSubmission(arg)
}

func (f *fakeQuerier) CreateSubmissionMetadata(_ context.Context, arg db.CreateSubmissionMetadataParams) (db.SubmissionMetadatum, error) {
	return f.createSubmissionMetadata(arg)
}

func (f *fakeQuerier) ListSubmissionsByForm(_ context.Context, arg db.ListSubmissionsByFormParams) ([]db.ListSubmissionsByFormRow, error) {
	return f.listSubmissionsByForm(arg)
}

var (
	errDB         = stderrors.New("connection reset")
	errUnique     = &pgconn.PgError{Code: "23505"}
	errForeignKey = &pgconn.PgError{Code: "23503"}

	now     = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	payload = json.RawMessage(`{"name":"Ada"}`)
	ip      = "203.0.113.9"
	agent   = "curl/8"
	seconds = int32(42)
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
		{"unknown form", errForeignKey, apperrors.CodeNotFound},
		{"db failure", errDB, apperrors.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{createFormSubmission: func(arg db.CreateFormSubmissionParams) (db.FormSubmission, error) {
				if want := (db.CreateFormSubmissionParams{FormID: 7, Payload: payload}); !reflect.DeepEqual(arg, want) {
					t.Fatalf("params = %+v, want %+v", arg, want)
				}
				return db.FormSubmission{ID: 1, FormID: 7, Payload: payload, SubmittedAt: &now}, tt.err
			}})

			got, err := svc.Create(context.Background(), CreateSubmissionInput{FormID: 7, Payload: payload})

			assertCode(t, err, tt.wantCode)
			want := Submission{ID: 1, FormID: 7, Payload: payload, SubmittedAt: now}
			if err == nil && !reflect.DeepEqual(got, want) {
				t.Fatalf("submission = %+v, want %+v", got, want)
			}
		})
	}
}

func TestAddMetadata(t *testing.T) {
	in := Metadata{IPAddress: &ip, UserAgent: &agent, CompletionTimeSeconds: &seconds}
	tests := []struct {
		name     string
		err      error
		wantCode apperrors.Code
	}{
		{"ok", nil, ""},
		{"already recorded", errUnique, apperrors.CodeConflict},
		{"unknown submission", errForeignKey, apperrors.CodeNotFound},
		{"db failure", errDB, apperrors.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{createSubmissionMetadata: func(arg db.CreateSubmissionMetadataParams) (db.SubmissionMetadatum, error) {
				want := db.CreateSubmissionMetadataParams{
					SubmissionID: 1, IpAddress: &ip, UserAgent: &agent, CompletionTimeSeconds: &seconds,
				}
				if arg != want {
					t.Fatalf("params = %+v, want %+v", arg, want)
				}
				return db.SubmissionMetadatum{
					ID: 9, SubmissionID: 1, IpAddress: &ip, UserAgent: &agent,
					CompletionTimeSeconds: &seconds, CreatedAt: &now,
				}, tt.err
			}})

			got, err := svc.AddMetadata(context.Background(), 1, in)

			assertCode(t, err, tt.wantCode)
			if err == nil && !reflect.DeepEqual(got, in) {
				t.Fatalf("metadata = %+v, want %+v", got, in)
			}
		})
	}
}

func TestListByForm(t *testing.T) {
	filter := ListSubmissionsFilter{TenantID: 3, FormID: 7}
	rows := []db.ListSubmissionsByFormRow{
		{ID: 2, FormID: 7, Payload: payload, SubmittedAt: &now, IpAddress: &ip, Referer: &agent},
		{ID: 1, FormID: 7, Payload: payload, SubmittedAt: nil},
	}
	tests := []struct {
		name     string
		page     Page
		err      error
		wantCode apperrors.Code
		want     []Submission
	}{
		{"ok", Page{Offset: 10, Limit: 5}, nil, "", []Submission{
			{ID: 2, FormID: 7, Payload: payload, SubmittedAt: now, Metadata: &Metadata{IPAddress: &ip, Referer: &agent}},
			{ID: 1, FormID: 7, Payload: payload},
		}},
		{"negative offset", Page{Offset: -1, Limit: 5}, nil, apperrors.CodeInvalidArgument, nil},
		{"zero limit", Page{Offset: 0, Limit: 0}, nil, apperrors.CodeInvalidArgument, nil},
		{"db failure", Page{Offset: 0, Limit: 5}, errDB, apperrors.CodeInternal, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{listSubmissionsByForm: func(arg db.ListSubmissionsByFormParams) ([]db.ListSubmissionsByFormRow, error) {
				want := db.ListSubmissionsByFormParams{
					FormID: 7, TenantID: 3, PageOffset: tt.page.Offset, PageLimit: tt.page.Limit,
				}
				if arg != want {
					t.Fatalf("params = %+v, want %+v", arg, want)
				}
				if tt.err != nil {
					return nil, tt.err
				}
				return rows, nil
			}})

			got, err := svc.ListByForm(context.Background(), filter, tt.page)

			assertCode(t, err, tt.wantCode)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("submissions = %+v, want %+v", got, tt.want)
			}
		})
	}
}
