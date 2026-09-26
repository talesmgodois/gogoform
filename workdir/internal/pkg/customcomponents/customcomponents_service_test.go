package customcomponents

import (
	"context"
	stderrors "errors"
	"reflect"
	"testing"
	"time"

	"app/internal/db"
	apperrors "app/internal/errors"
)

// fakeQuerier implements db.Querier with per-method stubs. Calling a method
// without a stub panics through the nil embedded interface.
type fakeQuerier struct {
	db.Querier
	createCustomComponent func(db.CreateCustomComponentParams) (db.CustomComponent, error)
	listCustomComponents  func() ([]db.CustomComponent, error)
}

func (f *fakeQuerier) CreateCustomComponent(_ context.Context, arg db.CreateCustomComponentParams) (db.CustomComponent, error) {
	return f.createCustomComponent(arg)
}

func (f *fakeQuerier) ListCustomComponents(context.Context) ([]db.CustomComponent, error) {
	return f.listCustomComponents()
}

var (
	errDB = stderrors.New("connection reset")
	now   = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	field = []byte(`{"type":"checkbox","name":"agree"}`)
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
	userID := int32(7)
	tests := []struct {
		name     string
		userID   *int32
		err      error
		wantCode apperrors.Code
	}{
		{"ok with user", &userID, nil, ""},
		{"ok without user", nil, nil, ""},
		{"db failure", &userID, errDB, apperrors.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{createCustomComponent: func(arg db.CreateCustomComponentParams) (db.CustomComponent, error) {
				want := db.CreateCustomComponentParams{UserID: tt.userID, Name: "Agree", Field: field}
				if !reflect.DeepEqual(arg, want) {
					t.Fatalf("params = %+v, want %+v", arg, want)
				}
				return db.CustomComponent{ID: 1, UserID: tt.userID, Name: "Agree", Field: field, CreatedAt: now}, tt.err
			}})

			got, err := svc.Create(context.Background(), CreateCustomComponentInput{Name: "Agree", Field: field, UserID: tt.userID})

			assertCode(t, err, tt.wantCode)
			want := CustomComponent{}
			if tt.err == nil {
				want = CustomComponent{ID: 1, UserID: tt.userID, Name: "Agree", Field: field, CreatedAt: now}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("component = %+v, want %+v", got, want)
			}
		})
	}
}

func TestList(t *testing.T) {
	userID := int32(7)
	row := db.CustomComponent{ID: 1, UserID: &userID, Name: "Agree", Field: field, CreatedAt: now}
	tests := []struct {
		name     string
		err      error
		wantCode apperrors.Code
		want     []CustomComponent
	}{
		{"ok", nil, "", []CustomComponent{{ID: 1, UserID: &userID, Name: "Agree", Field: field, CreatedAt: now}}},
		{"db failure", errDB, apperrors.CodeInternal, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{listCustomComponents: func() ([]db.CustomComponent, error) {
				if tt.err != nil {
					return nil, tt.err
				}
				return []db.CustomComponent{row}, nil
			}})

			got, err := svc.List(context.Background())

			assertCode(t, err, tt.wantCode)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("components = %+v, want %+v", got, tt.want)
			}
		})
	}
}
