package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	apperrors "app/internal/errors"
	"app/internal/pkg/tenants"
)

// apiKeyHeader is the request header carrying a tenant's API key.
const apiKeyHeader = "X-API-Key"

// maxTenantNameLen matches the tenants.name column size.
const maxTenantNameLen = 100

// CreateTenantRequest is the body of POST /tenants.
type CreateTenantRequest struct {
	Name string `json:"name" example:"Acme Inc."`
}

// TenantResponse is a tenant as returned to its owner, without the API key.
type TenantResponse struct {
	ID        int32     `json:"id" example:"1"`
	Name      string    `json:"name" example:"Acme Inc."`
	IsActive  bool      `json:"is_active" example:"true"`
	CreatedAt time.Time `json:"created_at" example:"2026-01-01T00:00:00Z"`
}

// CreateTenantResponse is the body returned when a tenant is created. It is
// the only response that ever contains the API key.
type CreateTenantResponse struct {
	TenantResponse
	APIKey string `json:"api_key" example:"3f1c...e9a0"`
}

// tenantsController serves the tenant endpoints and authenticates requests.
type tenantsController struct {
	tenants tenants.Repository
}

// Create registers a new tenant and issues its API key.
//
//	@Summary		Create a tenant
//	@Description	Registers a new tenant and returns it with a freshly generated API key. The key is shown only once; send it in the X-API-Key header of authenticated requests.
//	@Tags			tenants
//	@Accept			json
//	@Produce		json
//	@Param			body	body		CreateTenantRequest			true	"Tenant to create"
//	@Success		201		{object}	CreateTenantResponse		"Tenant created"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid request"
//	@Failure		500		{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/tenants [post]
func (c *tenantsController) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateTenantRequest
	if err := decodeJSON(w, r, &req); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || utf8.RuneCountInString(name) > maxTenantNameLen {
		apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("name is required and must be at most 100 characters"))
		return
	}
	apiKey, err := generateAPIKey()
	if err != nil {
		apperrors.WriteHTTP(w, r, apperrors.NewInternal(err))
		return
	}
	t, err := c.tenants.Create(r.Context(), tenants.CreateTenantInput{Name: name, APIKey: apiKey})
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, CreateTenantResponse{TenantResponse: toTenantResponse(t), APIKey: t.APIKey})
}

// Me returns the authenticated tenant.
//
//	@Summary		Get the current tenant
//	@Description	Returns the tenant that owns the API key of the request.
//	@Tags			tenants
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Success		200	{object}	TenantResponse				"Authenticated tenant"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Missing or invalid API key"
//	@Failure		500	{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/tenants/me [get]
func (c *tenantsController) Me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, toTenantResponse(tenantFrom(r.Context())))
}

// Authenticate wraps next so it only runs for requests carrying the API key
// of an active tenant, which is then available through tenantFrom.
func (c *tenantsController) Authenticate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(apiKeyHeader)
		if key == "" {
			apperrors.WriteHTTP(w, r, apperrors.NewUnauthorized("missing API key"))
			return
		}
		t, err := c.tenants.GetByAPIKey(r.Context(), key)
		if err != nil {
			if appErr, ok := apperrors.As(err); ok && appErr.Code == apperrors.CodeNotFound {
				err = apperrors.NewUnauthorized("invalid API key")
			}
			apperrors.WriteHTTP(w, r, err)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), tenantKey{}, t)))
	}
}

// tenantKey is the context key of the authenticated tenant.
type tenantKey struct{}

// tenantFrom returns the tenant stored in ctx by Authenticate.
func tenantFrom(ctx context.Context) tenants.Tenant {
	t, _ := ctx.Value(tenantKey{}).(tenants.Tenant)
	return t
}

// generateAPIKey returns a random 256-bit key, hex encoded.
func generateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func toTenantResponse(t tenants.Tenant) TenantResponse {
	return TenantResponse{ID: t.ID, Name: t.Name, IsActive: t.IsActive, CreatedAt: t.CreatedAt}
}
