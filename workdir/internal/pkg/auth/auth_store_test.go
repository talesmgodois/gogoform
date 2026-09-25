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
	updateUserRole    func(db.UpdateUserRoleParams) (db.User, error)
}

func (f *fakeQuerier) CreateUser(_ context.Context, arg db.CreateUserParams) (db.User, error) {
	return f.createUser(arg)
}

func (f *fakeQuerier) GetUserByUsername(_ context.Context, username string) (db.User, error) {
	return f.getUserByUsername(username)
}

func (f *fakeQuerier) UpdateUserRole(_ context.Context, arg db.UpdateUserRoleParams) (db.User, error) {
	return f.updateUserRole(arg)
}

func TestUserStoreCreate(t *testing.T) {
	var got db.CreateUserParams
	store := NewUserStore(&fakeQuerier{createUser: func(arg db.CreateUserParams) (db.User, error) {
		got = arg
		return db.User{ID: 4, Username: arg.Username, Role: arg.Role, IsActive: true}, nil
	}})
	u, err := store.Create(context.Background(), CreateUserInput{Username: "alice", PasswordHash: "hash", Role: RoleFormCreator})
	if err != nil || u.ID != 4 || u.Role != RoleFormCreator {
		t.Fatalf("Create = %+v, %v", u, err)
	}
	if got.Role != db.UserRoleFORMCREATOR || got.PasswordHash != "hash" {
		t.Fatalf("params = %+v", got)
	}

	store = NewUserStore(&fakeQuerier{createUser: func(db.CreateUserParams) (db.User, error) {
		return db.User{}, &pgconn.PgError{Code: "23505"}
	}})
	_, err = store.Create(context.Background(), CreateUserInput{Username: "alice"})
	assertCode(t, err, apperrors.CodeConflict)
}

func TestUserStoreNotFound(t *testing.T) {
	store := NewUserStore(&fakeQuerier{
		getUserByUsername: func(string) (db.User, error) { return db.User{}, pgx.ErrNoRows },
		updateUserRole:    func(db.UpdateUserRoleParams) (db.User, error) { return db.User{}, pgx.ErrNoRows },
	})
	_, err := store.GetByUsername(context.Background(), "ghost")
	assertCode(t, err, apperrors.CodeNotFound)
	_, err = store.UpdateRole(context.Background(), 9, RoleBasic)
	assertCode(t, err, apperrors.CodeNotFound)
	_, err = store.List(context.Background(), Page{Limit: 0})
	assertCode(t, err, apperrors.CodeInvalidArgument)
}
