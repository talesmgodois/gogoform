package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/auth"
)

// SignUpRequest is the body of POST /auth/signup.
type SignUpRequest struct {
	// Username is case-insensitive: 3 to 50 letters, digits, '.', '_' or '-'.
	Username string `json:"username" example:"alice"`
	// Password is 8 to 72 bytes long.
	Password string `json:"password" example:"correct horse battery"`
}

// UserResponse is a user as returned by the API.
type UserResponse struct {
	ID        int32     `json:"id" example:"1"`
	Username  string    `json:"username" example:"alice"`
	Role      auth.Role `json:"role" enums:"ADMIN,FORM_CREATOR,BASIC" example:"BASIC"`
	IsActive  bool      `json:"is_active" example:"true"`
	CreatedAt time.Time `json:"created_at" example:"2026-01-01T00:00:00Z"`
}

// TokenResponse is the body returned by POST /auth/signin.
type TokenResponse struct {
	// AccessToken is a JWT; send it as "Authorization: Bearer <token>".
	AccessToken string       `json:"access_token" example:"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."`
	TokenType   string       `json:"token_type" example:"Bearer"`
	ExpiresAt   time.Time    `json:"expires_at" example:"2026-01-01T01:00:00Z"`
	User        UserResponse `json:"user"`
}

// SetRoleRequest is the body of PUT /users/{id}/role.
type SetRoleRequest struct {
	Role auth.Role `json:"role" enums:"ADMIN,FORM_CREATOR,BASIC" example:"FORM_CREATOR"`
}

// UserListResponse is a page of users.
type UserListResponse struct {
	Items []UserResponse `json:"items"`
	PageResponse
}

// authController serves the account endpoints and guards routes with an
// auth.Policy.
type authController struct {
	auth *auth.Service
}

// SignUp creates an account.
//
//	@Summary		Sign up
//	@Description	Creates an account with the BASIC role. Usernames are case-insensitive.
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		SignUpRequest				true	"Credentials of the new account"
//	@Success		201		{object}	UserResponse				"Account created"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid username or password"
//	@Failure		409		{object}	errors.HTTPErrorResponse	"Username already taken"
//	@Failure		500		{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/auth/signup [post]
func (c *authController) SignUp(w http.ResponseWriter, r *http.Request) {
	var req SignUpRequest
	if err := decodeJSON(w, r, &req); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	u, err := c.auth.SignUp(r.Context(), req.Username, req.Password)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, toUserResponse(u))
}

// SignIn exchanges HTTP Basic credentials for a JWT.
//
//	@Summary		Sign in
//	@Description	Checks the HTTP Basic credentials ("Authorization: Basic " + btoa(username + ":" + password)) and returns a JWT access token to send as "Authorization: Bearer <token>".
//	@Tags			auth
//	@Produce		json
//	@Security		BasicAuth
//	@Success		200	{object}	TokenResponse				"Signed in"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Missing or invalid credentials"
//	@Failure		500	{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/auth/signin [post]
func (c *authController) SignIn(w http.ResponseWriter, r *http.Request) {
	username, password, ok := r.BasicAuth()
	if !ok {
		apperrors.WriteHTTP(w, r, apperrors.NewUnauthorized("missing Basic credentials"))
		return
	}
	tok, err := c.auth.SignIn(r.Context(), username, password)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, TokenResponse{
		AccessToken: tok.AccessToken,
		TokenType:   "Bearer",
		ExpiresAt:   tok.ExpiresAt,
		User:        toUserResponse(tok.User),
	})
}

// Me returns the signed-in user.
//
//	@Summary		Get the current user
//	@Description	Returns the user authenticated by the bearer token or Basic credentials.
//	@Tags			auth
//	@Produce		json
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Success		200	{object}	UserResponse				"Signed-in user"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Not signed in"
//	@Failure		500	{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/auth/me [get]
func (c *authController) Me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, toUserResponse(*userFrom(r.Context())))
}

// ListUsers lists every user.
//
//	@Summary		List users
//	@Description	Lists every user by ID. Requires the ADMIN role.
//	@Tags			users
//	@Produce		json
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			offset	query		int							false	"Number of users to skip"			minimum(0)	default(0)
//	@Param			limit	query		int							false	"Maximum number of users to return"	minimum(1)	maximum(100)	default(20)
//	@Success		200		{object}	UserListResponse			"Page of users"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid query parameters"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Not signed in"
//	@Failure		403		{object}	errors.HTTPErrorResponse	"Not an admin"
//	@Failure		500		{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/users [get]
func (c *authController) ListUsers(w http.ResponseWriter, r *http.Request) {
	offset, limit, err := parsePage(r)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	users, err := c.auth.ListUsers(r.Context(), auth.Page{Offset: offset, Limit: limit})
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	resp := UserListResponse{Items: make([]UserResponse, len(users)), PageResponse: PageResponse{Offset: offset, Limit: limit}}
	for i, u := range users {
		resp.Items[i] = toUserResponse(u)
	}
	writeJSON(w, r, http.StatusOK, resp)
}

// SetRole changes the role of a user.
//
//	@Summary		Change the role of a user
//	@Description	Sets the role of another user. Requires the ADMIN role; admins cannot change their own role.
//	@Tags			users
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			id		path		int							true	"User ID"
//	@Param			body	body		SetRoleRequest				true	"New role"
//	@Success		200		{object}	UserResponse				"User updated"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid request"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Not signed in"
//	@Failure		403		{object}	errors.HTTPErrorResponse	"Not an admin"
//	@Failure		404		{object}	errors.HTTPErrorResponse	"User not found"
//	@Failure		500		{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/users/{id}/role [put]
func (c *authController) SetRole(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	var req SetRoleRequest
	if err := decodeJSON(w, r, &req); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	u, err := c.auth.SetRole(r.Context(), *userFrom(r.Context()), id, req.Role)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, toUserResponse(u))
}

// Guard wraps next so it only runs for requests p allows. The user is
// authenticated by an "Authorization: Bearer <jwt>" or "Authorization: Basic"
// header and is then available through userFrom. Invalid credentials are
// rejected even on public routes, rather than silently treated as anonymous.
func (c *authController) Guard(p auth.Policy, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := c.authenticate(r)
		if err != nil {
			unauthorized(w, r, err)
			return
		}
		if !p.Allows(u) {
			if u == nil {
				unauthorized(w, r, apperrors.NewUnauthorized("sign in required"))
			} else {
				apperrors.WriteHTTP(w, r, apperrors.NewForbidden("your role does not grant access to this resource"))
			}
			return
		}
		next(w, r.WithContext(withUser(r.Context(), u)))
	}
}

// authenticate returns the user identified by the Authorization header, or
// nil when there is none.
func (c *authController) authenticate(r *http.Request) (*auth.User, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return nil, nil
	}
	var u auth.User
	var err error
	if scheme, token, _ := strings.Cut(h, " "); strings.EqualFold(scheme, "Bearer") {
		u, err = c.auth.CheckToken(r.Context(), strings.TrimSpace(token))
	} else if username, password, ok := r.BasicAuth(); ok {
		u, err = c.auth.CheckPassword(r.Context(), username, password)
	} else {
		err = apperrors.NewUnauthorized("unsupported Authorization header: use Bearer or Basic")
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// unauthorized writes err, advertising the bearer scheme on 401 responses.
// Basic is not advertised so browsers do not prompt for credentials.
func unauthorized(w http.ResponseWriter, r *http.Request, err error) {
	if apperrors.HTTPStatus(err) == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
	}
	apperrors.WriteHTTP(w, r, err)
}

// userKey is the context key of the signed-in user.
type userKey struct{}

// withUser returns ctx carrying u, which may be nil.
func withUser(ctx context.Context, u *auth.User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

// userFrom returns the user stored in ctx by Guard, or nil for anonymous
// requests.
func userFrom(ctx context.Context) *auth.User {
	u, _ := ctx.Value(userKey{}).(*auth.User)
	return u
}

func toUserResponse(u auth.User) UserResponse {
	return UserResponse{ID: u.ID, Username: u.Username, Role: u.Role, IsActive: u.IsActive, CreatedAt: u.CreatedAt}
}
