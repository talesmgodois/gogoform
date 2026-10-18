package auth

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"app/internal/db"
	apperrors "app/internal/errors"
)

// fakeQuerier implements db.Querier with per-method stubs. Calling a method
// without a stub panics through the nil embedded interface.
type fakeQuerier struct {
	db.Querier
	createUser        func(db.CreateUserParams) (db.User, error)
	getUserByUsername func(string) (db.User, error)
	getUserByEmail    func(string) (db.User, error)
	updateUserRole    func(db.UpdateUserRoleParams) (db.User, error)
}

func (f *fakeQuerier) CreateUser(_ context.Context, arg db.CreateUserParams) (db.User, error) {
	return f.createUser(arg)
}

func (f *fakeQuerier) GetUserByUsername(_ context.Context, username string) (db.User, error) {
	return f.getUserByUsername(username)
}

func (f *fakeQuerier) GetUserByEmail(_ context.Context, email string) (db.User, error) {
	return f.getUserByEmail(email)
}

func (f *fakeQuerier) UpdateUserRole(_ context.Context, arg db.UpdateUserRoleParams) (db.User, error) {
	return f.updateUserRole(arg)
}

func TestUserStoreCreate(t *testing.T) {
	var got db.CreateUserParams
	var store *UserStore
	store = NewUserStore(&fakeQuerier{createUser: func(arg db.CreateUserParams) (db.User, error) {
		got = arg
		return db.User{ID: 4, Username: arg.Username, Email: arg.Email, Role: arg.Role, IsActive: true}, nil
	}})
	u, err := store.Create(context.Background(), CreateUserInput{Username: "alice", Email: "alice@example.com", PasswordHash: "hash", Role: RoleFormCreator})
	if err != nil || u.ID != 4 || u.Role != RoleFormCreator || u.Email != "alice@example.com" {
		t.Fatalf("Create = %+v, %v", u, err)
	}
	if got.Role != db.UserRoleFORMCREATOR || got.PasswordHash != "hash" || got.Email == nil || *got.Email != "alice@example.com" {
		t.Fatalf("params = %+v", got)
	}

	// No email is stored as NULL.
	if _, err := store.Create(context.Background(), CreateUserInput{Username: "admin"}); err != nil || got.Email != nil {
		t.Fatalf("Create without email: params = %+v, err = %v", got, err)
	}
}

func TestUserStoreCreateConflicts(t *testing.T) {
	for _, tt := range []struct {
		constraint, want string
	}{
		{"users_email_key", msgEmailTaken},
		{"users_username_key", msgUsernameTaken},
	} {
		store := NewUserStore(&fakeQuerier{createUser: func(db.CreateUserParams) (db.User, error) {
			return db.User{}, &pgconn.PgError{Code: "23505", ConstraintName: tt.constraint}
		}})
		_, err := store.Create(context.Background(), CreateUserInput{Username: "alice", Email: "alice@example.com"})
		assertCode(t, err, apperrors.CodeConflict)
		if appErr, _ := apperrors.As(err); appErr.Message != tt.want {
			t.Errorf("%s: message = %q, want %q", tt.constraint, appErr.Message, tt.want)
		}
	}
}

func TestUserStoreNotFound(t *testing.T) {
	store := NewUserStore(&fakeQuerier{
		getUserByUsername: func(string) (db.User, error) { return db.User{}, pgx.ErrNoRows },
		getUserByEmail:    func(string) (db.User, error) { return db.User{}, pgx.ErrNoRows },
		updateUserRole:    func(db.UpdateUserRoleParams) (db.User, error) { return db.User{}, pgx.ErrNoRows },
	})
	_, err := store.GetByUsername(context.Background(), "ghost")
	assertCode(t, err, apperrors.CodeNotFound)
	_, err = store.GetByEmail(context.Background(), "ghost@example.com")
	assertCode(t, err, apperrors.CodeNotFound)
	_, err = store.UpdateRole(context.Background(), 9, RoleBasic)
	assertCode(t, err, apperrors.CodeNotFound)
	_, err = store.List(context.Background(), Page{Limit: 0})
	assertCode(t, err, apperrors.CodeInvalidArgument)
}

func TestUserStoreGetByEmail(t *testing.T) {
	email := "alice@example.com"
	store := NewUserStore(&fakeQuerier{getUserByEmail: func(got string) (db.User, error) {
		if got != email {
			t.Fatalf("email = %q, want %q", got, email)
		}
		return db.User{ID: 4, Username: "alice", Email: &email, PasswordHash: "hash", Role: db.UserRoleBASIC, IsActive: true}, nil
	}})
	su, err := store.GetByEmail(context.Background(), email)
	if err != nil || su.ID != 4 || su.Email != email || su.PasswordHash != "hash" {
		t.Fatalf("GetByEmail = %+v, %v", su, err)
	}
}
