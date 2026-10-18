package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	apperrors "app/internal/errors"
	"app/internal/pkg/auth"
	"app/internal/pkg/tenants"
)

// apiKeyHeader is the request header carrying a tenant's API key.
const apiKeyHeader = "X-API-Key"

// CreateTenantRequest is the body of POST /tenants.
type CreateTenantRequest struct {
	Name string `json:"name" example:"Acme Inc."`
}

// TenantResponse is a tenant without its API key.
type TenantResponse struct {
	ID        int32     `json:"id" example:"1"`
	Name      string    `json:"name" example:"Acme Inc."`
	IsActive  bool      `json:"is_active" example:"true"`
	CreatedAt time.Time `json:"created_at" example:"2026-01-01T00:00:00Z"`
}

// TenantWithAPIKeyResponse is a tenant as returned to its owner, including
// the API key to send in the X-API-Key header.
type TenantWithAPIKeyResponse struct {
	TenantResponse
	APIKey string `json:"api_key" example:"3f1c...e9a0"`
}

// tenantsController serves the tenant endpoints.
type tenantsController struct {
	tenants tenants.Repository
}

// Create registers a new tenant and issues its API key.
//
//	@Summary		Create a tenant
//	@Description	Registers a new tenant and returns it with a freshly generated API key. The key is shown only once; send it in the X-API-Key header of authenticated requests. Development only: mounted when features.public_tenant_signup (FEATURE_PUBLIC_TENANT_SIGNUP) is enabled, which is off by default outside development.
//	@Tags			tenants
//	@Accept			json
//	@Produce		json
//	@Param			body	body		CreateTenantRequest			true	"Tenant to create"
//	@Success		201		{object}	TenantWithAPIKeyResponse	"Tenant created"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid request"
//	@Failure		404		{object}	errors.HTTPErrorResponse	"Disabled outside development"
//	@Failure		500		{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/tenants [post]
func (c *tenantsController) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateTenantRequest
	if err := decodeJSON(w, r, &req); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || utf8.RuneCountInString(name) > tenants.MaxNameLen {
		apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("name is required and must be at most 100 characters"))
		return
	}
	apiKey, err := tenants.GenerateAPIKey()
	if err != nil {
		apperrors.WriteHTTP(w, r, apperrors.NewInternal(err))
		return
	}
	t, err := c.tenants.Create(r.Context(), tenants.CreateTenantInput{Name: name, APIKey: apiKey})
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, toTenantWithAPIKeyResponse(t))
}

// Me returns the authenticated tenant with its API key.
//
//	@Summary		Get the current tenant
//	@Description	Returns the tenant that owns the API key of the request, or the workspace of the signed-in user, including its API key so signed-in users can integrate.
//	@Tags			tenants
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Success		200	{object}	TenantWithAPIKeyResponse	"Authenticated tenant"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Missing or invalid API key or user credentials"
//	@Failure		403	{object}	errors.HTTPErrorResponse	"Signed-in user has no workspace"
//	@Failure		500	{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/tenants/me [get]
func (c *tenantsController) Me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, toTenantWithAPIKeyResponse(tenantFrom(r.Context())))
}

// RotateAPIKey replaces the API key of the authenticated tenant.
//
//	@Summary		Rotate the API key
//	@Description	Generates a new API key for the tenant that owns the API key of the request, or the workspace of the signed-in user, and returns it. The previous key stops working immediately.
//	@Tags			tenants
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Success		200	{object}	TenantWithAPIKeyResponse	"Tenant with its new API key"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Missing or invalid API key or user credentials"
//	@Failure		403	{object}	errors.HTTPErrorResponse	"Signed-in user has no workspace, or cross-origin request authenticated by the session cookie"
//	@Failure		404	{object}	errors.HTTPErrorResponse	"Tenant no longer active"
//	@Failure		500	{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/tenants/me/api-key/rotate [post]
func (c *tenantsController) RotateAPIKey(w http.ResponseWriter, r *http.Request) {
	apiKey, err := tenants.GenerateAPIKey()
	if err != nil {
		apperrors.WriteHTTP(w, r, apperrors.NewInternal(err))
		return
	}
	t, err := c.tenants.RotateAPIKey(r.Context(), tenantFrom(r.Context()).ID, apiKey)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusOK, toTenantWithAPIKeyResponse(t))
}

// tenantOrUser returns a middleware that only runs next for requests
// resolved to an active tenant, which is then available through tenantFrom.
// An X-API-Key header identifies the tenant directly. Otherwise the user
// authenticated by the Authorization header (see authController.authenticate),
// or else by the session cookie of the /app pages (see sessionCookieUser), is
// resolved to the tenant they own, and is also available through userFrom.
// Missing or invalid credentials give 401, a user without a workspace or a
// cross-origin state-changing request authenticated by the cookie 403.
func tenantOrUser(ts tenants.Repository, authCtl *authController) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			var t tenants.Tenant
			var err error
			if key := r.Header.Get(apiKeyHeader); key != "" {
				t, err = ts.GetByAPIKey(ctx, key)
				if isNotFound(err) {
					err = apperrors.NewUnauthorized("invalid API key")
				}
				if err != nil {
					apperrors.WriteHTTP(w, r, err)
					return
				}
			} else {
				u, authErr := authCtl.authenticate(r)
				if authErr == nil && u == nil {
					u, authErr = sessionCookieUser(r, authCtl)
				}
				if authErr != nil {
					unauthorized(w, r, authErr)
					return
				}
				t, err = ts.GetByOwner(ctx, u.ID)
				if isNotFound(err) {
					err = apperrors.NewForbidden("your account has no workspace")
				}
				if err != nil {
					apperrors.WriteHTTP(w, r, err)
					return
				}
				ctx = withUser(ctx, u)
			}
			next(w, r.WithContext(withTenant(ctx, t)))
		}
	}
}

// sessionCookieUser returns the user of the session cookie of r. Only
// GET and HEAD requests may come from another origin: the cookie must not let
// another site change the user's data.
func sessionCookieUser(r *http.Request, authCtl *authController) (*auth.User, error) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return nil, apperrors.NewUnauthorized("missing API key, Authorization header or session")
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
		return nil, apperrors.NewForbidden("cross-origin request")
	}
	u, err := authCtl.auth.CheckToken(r.Context(), cookie.Value)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// isNotFound reports whether err is an apperrors NotFound error.
func isNotFound(err error) bool {
	appErr, ok := apperrors.As(err)
	return ok && appErr.Code == apperrors.CodeNotFound
}

// tenantKey is the context key of the authenticated tenant.
type tenantKey struct{}

func withTenant(ctx context.Context, t tenants.Tenant) context.Context {
	return context.WithValue(ctx, tenantKey{}, t)
}

// tenantFrom returns the tenant stored in ctx by tenantOrUser, or by
// guardPage for the /app workspace routes.
func tenantFrom(ctx context.Context) tenants.Tenant {
	t, _ := ctx.Value(tenantKey{}).(tenants.Tenant)
	return t
}

func toTenantResponse(t tenants.Tenant) TenantResponse {
	return TenantResponse{ID: t.ID, Name: t.Name, IsActive: t.IsActive, CreatedAt: t.CreatedAt}
}

func toTenantWithAPIKeyResponse(t tenants.Tenant) TenantWithAPIKeyResponse {
	return TenantWithAPIKeyResponse{TenantResponse: toTenantResponse(t), APIKey: t.APIKey}
}
