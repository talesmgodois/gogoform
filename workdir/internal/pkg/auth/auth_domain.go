// Package auth holds the authentication and authorization domain: the user
// accounts and their roles, the Repository port used to persist them, a
// UserStore implementing it on top of sqlc, and a Service that checks
// passwords (HTTP Basic), issues and verifies JWTs and decides, through
// Policy, which users may reach a route.
package auth

import (
	"slices"
	"time"
)

// Role is the access level of a user.
type Role string

// Roles, from the most to the least privileged. ADMIN passes every Policy.
const (
	RoleAdmin       Role = "ADMIN"
	RoleFormCreator Role = "FORM_CREATOR"
	RoleBasic       Role = "BASIC"
)

// DefaultRole is the role of newly signed-up users.
const DefaultRole = RoleBasic

// AllRoles lists every valid Role.
var AllRoles = []Role{RoleAdmin, RoleFormCreator, RoleBasic}

// Valid reports whether r is one of AllRoles.
func (r Role) Valid() bool {
	return slices.Contains(AllRoles, r)
}

// User is an account. It signs in with a username and password, through an
// external OpenID Connect identity linked to it, or both.
type User struct {
	ID        int32
	Username  string
	Role      Role
	IsActive  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// StoredUser is a User together with its password hash, as needed to check
// credentials. PasswordHash is empty for users without a password (created
// through OIDC sign-in). Never expose the hash outside this package.
type StoredUser struct {
	User
	PasswordHash string
}

// ExternalIdentity is a person authenticated by an external OpenID Connect
// provider. (Issuer, Subject) identifies them; the other claims are only
// used to name a new account and are never trusted to find an existing one.
type ExternalIdentity struct {
	Issuer            string
	Subject           string
	Email             string
	EmailVerified     bool
	PreferredUsername string
	Name              string
}

// CreateExternalUserInput holds the data needed to store a user without a
// password along with the external identity it signs in with.
type CreateExternalUserInput struct {
	// Username must already be normalized and valid.
	Username string
	Role     Role
	Issuer   string
	Subject  string
	// Email is informative only; empty when the provider sent none.
	Email string
}

// CreateUserInput holds the data needed to store a user.
type CreateUserInput struct {
	// Username must already be normalized (see NormalizeUsername).
	Username     string
	PasswordHash string
	Role         Role
}

// Token is a signed JWT issued to a user.
type Token struct {
	AccessToken string
	ExpiresAt   time.Time
	User        User
}

// Page selects a window of a listing.
type Page struct {
	Offset int32
	Limit  int32
}

// Policy decides who may reach a route: everyone (Public) or signed-in users,
// optionally restricted to some roles (SignedIn). The zero value is SignedIn().
type Policy struct {
	public bool
	roles  []Role
}

// Public lets anyone through, signed in or not.
var Public = Policy{public: true}

// SignedIn requires a signed-in user whose role is one of roles, or any role
// when roles is empty. ADMIN is always allowed.
func SignedIn(roles ...Role) Policy {
	return Policy{roles: roles}
}

// IsPublic reports whether p lets anonymous requests through.
func (p Policy) IsPublic() bool {
	return p.public
}

// Roles returns the roles p is restricted to; empty means any role.
func (p Policy) Roles() []Role {
	return slices.Clone(p.roles)
}

// Allows reports whether u, which is nil for anonymous requests, may reach a
// route guarded by p.
func (p Policy) Allows(u *User) bool {
	switch {
	case p.public:
		return true
	case u == nil:
		return false
	case len(p.roles) == 0 || u.Role == RoleAdmin:
		return true
	default:
		return slices.Contains(p.roles, u.Role)
	}
}
