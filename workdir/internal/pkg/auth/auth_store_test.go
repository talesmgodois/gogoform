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
	createUser             func(db.CreateUserParams) (db.User, error)
	getUserByUsername      func(string) (db.User, error)
	updateUserRole         func(db.UpdateUserRoleParams) (db.User, error)
	createUserWithIdentity func(db.CreateUserWithIdentityParams) (db.CreateUserWithIdentityRow, error)
}

func (f *fakeQuerier) CreateUserWithIdentity(_ context.Context, arg db.CreateUserWithIdentityParams) (db.CreateUserWithIdentityRow, error) {
	return f.createUserWithIdentity(arg)
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
	if got.Role != db.UserRoleFORMCREATOR || got.PasswordHash == nil || *got.PasswordHash != "hash" {
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

func TestUserStoreNullPasswordHash(t *testing.T) {
	store := NewUserStore(&fakeQuerier{getUserByUsername: func(username string) (db.User, error) {
		return db.User{ID: 1, Username: username, Role: db.UserRoleBASIC, IsActive: true}, nil
	}})
	su, err := store.GetByUsername(context.Background(), "oidc-user")
	if err != nil || su.PasswordHash != "" {
		t.Fatalf("GetByUsername = %+v, %v; want an empty hash", su, err)
	}
}

func TestUserStoreCreateWithIdentity(t *testing.T) {
	var got db.CreateUserWithIdentityParams
	store := NewUserStore(&fakeQuerier{createUserWithIdentity: func(arg db.CreateUserWithIdentityParams) (db.CreateUserWithIdentityRow, error) {
		got = arg
		return db.CreateUserWithIdentityRow{ID: 7, Username: arg.Username, Role: arg.Role, IsActive: true}, nil
	}})
	in := CreateExternalUserInput{Username: "alice", Role: RoleBasic, Issuer: "https://idp", Subject: "sub-1"}
	u, err := store.CreateWithIdentity(context.Background(), in)
	if err != nil || u.ID != 7 || u.Username != "alice" {
		t.Fatalf("CreateWithIdentity = %+v, %v", u, err)
	}
	if got.Email != nil || got.Issuer != "https://idp" || got.Subject != "sub-1" {
		t.Fatalf("params = %+v; want a NULL email", got)
	}

	store = NewUserStore(&fakeQuerier{createUserWithIdentity: func(db.CreateUserWithIdentityParams) (db.CreateUserWithIdentityRow, error) {
		return db.CreateUserWithIdentityRow{}, &pgconn.PgError{Code: "23505"}
	}})
	_, err = store.CreateWithIdentity(context.Background(), in)
	assertCode(t, err, apperrors.CodeConflict)
}
