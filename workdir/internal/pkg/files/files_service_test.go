package files

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
	createFile  func(db.CreateFileParams) (db.CreateFileRow, error)
	getFileByID func(string) (db.File, error)
	deleteFile  func(db.DeleteFileParams) (int64, error)
}

func (f *fakeQuerier) CreateFile(_ context.Context, arg db.CreateFileParams) (db.CreateFileRow, error) {
	return f.createFile(arg)
}

func (f *fakeQuerier) GetFileByID(_ context.Context, id string) (db.File, error) {
	return f.getFileByID(id)
}

func (f *fakeQuerier) DeleteFile(_ context.Context, arg db.DeleteFileParams) (int64, error) {
	return f.deleteFile(arg)
}

const (
	fileID = "0b6a3b2e-8f1c-4c4e-9d5e-2f1a7c3b9e10"
	// helloSHA256 is the SHA-256 of "hello".
	helloSHA256 = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
)

var (
	errDB         = stderrors.New("connection reset")
	errForeignKey = &pgconn.PgError{Code: "23503"}

	now  = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	data = []byte("hello")
	meta = File{ID: fileID, TenantID: 7, Name: "hello.txt", ContentType: "text/plain", Size: 5, Checksum: helloSHA256, CreatedAt: now}
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
		{"unknown tenant", errForeignKey, apperrors.CodeNotFound},
		{"db failure", errDB, apperrors.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{createFile: func(arg db.CreateFileParams) (db.CreateFileRow, error) {
				want := db.CreateFileParams{TenantID: 7, Name: "hello.txt", ContentType: "text/plain", SizeBytes: 5, ChecksumSha256: helloSHA256, Data: data}
				if !reflect.DeepEqual(arg, want) {
					t.Fatalf("params = %+v, want %+v", arg, want)
				}
				return db.CreateFileRow{ID: fileID, TenantID: 7, Name: "hello.txt", ContentType: "text/plain", SizeBytes: 5, ChecksumSha256: helloSHA256, CreatedAt: now}, tt.err
			}})

			got, err := svc.Create(context.Background(), CreateFileInput{TenantID: 7, Name: "hello.txt", ContentType: "text/plain", Data: data})

			assertCode(t, err, tt.wantCode)
			if err == nil && !reflect.DeepEqual(got, meta) {
				t.Fatalf("file = %+v, want %+v", got, meta)
			}
		})
	}
}

func TestGetByID(t *testing.T) {
	withData := meta
	withData.Data = data
	tests := []struct {
		name     string
		id       string
		err      error
		wantCode apperrors.Code
		want     File
	}{
		{"ok", fileID, nil, "", withData},
		{"not found", fileID, pgx.ErrNoRows, apperrors.CodeNotFound, File{}},
		{"malformed id", "42", nil, apperrors.CodeNotFound, File{}},
		{"db failure", fileID, errDB, apperrors.CodeInternal, File{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{getFileByID: func(id string) (db.File, error) {
				if id != fileID {
					t.Fatalf("id = %q", id)
				}
				return db.File{ID: fileID, TenantID: 7, Name: "hello.txt", ContentType: "text/plain", SizeBytes: 5, ChecksumSha256: helloSHA256, Data: data, CreatedAt: now}, tt.err
			}})

			got, err := svc.GetByID(context.Background(), tt.id)

			assertCode(t, err, tt.wantCode)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("file = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		rows     int64
		err      error
		wantCode apperrors.Code
	}{
		{"ok", fileID, 1, nil, ""},
		{"not found or other tenant", fileID, 0, nil, apperrors.CodeNotFound},
		{"malformed id", "not-a-uuid", 0, nil, apperrors.CodeNotFound},
		{"db failure", fileID, 0, errDB, apperrors.CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeQuerier{deleteFile: func(arg db.DeleteFileParams) (int64, error) {
				if want := (db.DeleteFileParams{ID: fileID, TenantID: 7}); arg != want {
					t.Fatalf("params = %+v, want %+v", arg, want)
				}
				return tt.rows, tt.err
			}})

			assertCode(t, svc.Delete(context.Background(), 7, tt.id), tt.wantCode)
		})
	}
}
