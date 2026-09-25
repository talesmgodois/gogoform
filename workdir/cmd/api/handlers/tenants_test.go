package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apperrors "app/internal/errors"
)

func TestCreateTenant(t *testing.T) {
	svc := testServices()
	rec := do(t, svc, http.MethodPost, "/tenants", `{"name":"  Acme  "}`, false)
	assertStatus(t, rec, http.StatusCreated)

	body := decode[CreateTenantResponse](t, rec)
	created := svc.tenants.(*fakeTenants).created
	if created.Name != "Acme" || body.Name != "Acme" {
		t.Fatalf("name not trimmed: input %q, response %q", created.Name, body.Name)
	}
	if len(body.APIKey) != 64 || body.APIKey != created.APIKey {
		t.Fatalf("api_key = %q, stored %q", body.APIKey, created.APIKey)
	}
}

func TestCreateTenantInvalid(t *testing.T) {
	for _, body := range []string{
		`{"name":""}`,
		`{"name":"` + strings.Repeat("a", 101) + `"}`,
		`{"name":"a","api_key":"mine"}`,
		`{"name":"a"}{}`,
		`not json`,
	} {
		rec := do(t, testServices(), http.MethodPost, "/tenants", body, false)
		assertStatus(t, rec, http.StatusBadRequest)
	}
}

func TestCreateTenantConflict(t *testing.T) {
	svc := testServices()
	svc.tenants.(*fakeTenants).err = apperrors.NewConflict("api key already in use")
	assertStatus(t, do(t, svc, http.MethodPost, "/tenants", `{"name":"a"}`, false), http.StatusConflict)
}

func TestMe(t *testing.T) {
	rec := do(t, testServices(), http.MethodGet, "/tenants/me", "", true)
	assertStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), testAPIKey) {
		t.Fatalf("response leaks the API key: %s", rec.Body.String())
	}
	if body := decode[TenantResponse](t, rec); body.ID != testTenant.ID {
		t.Fatalf("id = %d, want %d", body.ID, testTenant.ID)
	}
}

func TestAuthenticateInvalidKey(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/tenants/me", nil)
	req.Header.Set(apiKeyHeader, "wrong")
	rec := httptest.NewRecorder()
	Routes(testServices(), testAppConfig).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusUnauthorized)
	if body := decode[apperrors.HTTPErrorResponse](t, rec); body.Code != apperrors.CodeUnauthorized {
		t.Fatalf("code = %q", body.Code)
	}
}
