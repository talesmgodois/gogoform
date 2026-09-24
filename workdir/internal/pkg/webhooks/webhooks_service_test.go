package webhooks

import (
	"context"
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
	createFormWebhook        func(db.CreateFormWebhookParams) (db.FormWebhook, error)
	listActiveWebhooksByForm func(int32) ([]db.FormWebhook, error)
}

func (f *fakeQuerier) CreateFormWebhook(_ context.Context, arg db.CreateFormWebhookParams) (db.FormWebhook, error) {
	return f.createFormWebhook(arg)
}

func (f *fakeQuerier) ListActiveWebhooksByForm(_ context.Context, formID int32) ([]db.FormWebhook, error) {
	return f.listActiveWebhooksByForm(formID)
}

var (
	errDB         = stderrors.New("connection reset")
	errForeignKey = &pgconn.PgError{Code: "23503"}

	now       = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	active    = true
	secret    = "s3cret"
	targetURL = "https://example.com/hook"
	dbHook    = db.FormWebhook{ID: 5, FormID: 7, TargetUrl: targetURL, SecretToken: &secret, IsActive: &active, CreatedAt: &now}
	hook      = Webhook{ID: 5, FormID: 7, TargetURL: targetURL, SecretToken: &secret, IsActive: true, CreatedAt: now}
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
			svc := NewService(&fakeQuerier{createFormWebhook: func(arg db.CreateFormWebhookParams) (db.FormWebhook, error) {
				if want := (db.CreateFormWebhookParams{FormID: 7, TargetUrl: targetURL, SecretToken: &secret}); arg != want {
					t.Fatalf("params = %+v, want %+v", arg, want)
				}
				return dbHook, tt.err
			}})

			got, err := svc.Create(context.Background(), CreateWebhookInput{FormID: 7, TargetURL: targetURL, SecretToken: &secret})

			assertCode(t, err, tt.wantCode)
			if err == nil && !reflect.DeepEqual(got, hook) {
				t.Fatalf("webhook = %+v, want %+v", got, hook)
			}
		})
	}
}

func TestListActiveByForm(t *testing.T) {
	tests := []struct {
		name     string
		rows     []db.FormWebhook
		err      error
		wantCode apperrors.Code
		want     []Webhook
	}{
		{"ok", []db.FormWebhook{dbHook, {ID: 6, FormID: 7, TargetUrl: targetURL}}, nil, "", []Webhook{
			hook,
			{ID: 6, FormID: 7, TargetURL: targetURL},
		}},
		{"none", []db.FormWebhook{}, nil, "", []Webhook{}},
		{"db failure", nil, errDB, apperrors.CodeInternal, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{listActiveWebhooksByForm: func(formID int32) ([]db.FormWebhook, error) {
				if formID != 7 {
					t.Fatalf("formID = %d", formID)
				}
				return tt.rows, tt.err
			}})

			got, err := svc.ListActiveByForm(context.Background(), 7)

			assertCode(t, err, tt.wantCode)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("webhooks = %+v, want %+v", got, tt.want)
			}
		})
	}
}
