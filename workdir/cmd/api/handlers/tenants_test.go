package handlers

import (
	"errors"
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

	body := decode[TenantWithAPIKeyResponse](t, rec)
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
	if body := decode[TenantWithAPIKeyResponse](t, rec); body.ID != testTenant.ID || body.APIKey != testAPIKey {
		t.Fatalf("got id %d key %q, want %d %q", body.ID, body.APIKey, testTenant.ID, testAPIKey)
	}
}

func TestMeSignedInUserGetsTheirAPIKey(t *testing.T) {
	svc := twoTenantServices()
	rec := serve(svc, userRequest(http.MethodGet, "/tenants/me", "", bearer(tokenFor(t, svc, "creator"))))
	assertStatus(t, rec, http.StatusOK)
	if body := decode[TenantWithAPIKeyResponse](t, rec); body.ID != tenantA.ID || body.APIKey != tenantA.APIKey {
		t.Fatalf("got id %d key %q, want %d %q", body.ID, body.APIKey, tenantA.ID, tenantA.APIKey)
	}
}

func TestRotateAPIKey(t *testing.T) {
	svc := testServices()
	rec := do(t, svc, http.MethodPost, "/tenants/me/api-key/rotate", "", true)
	assertStatus(t, rec, http.StatusOK)
	body := decode[TenantWithAPIKeyResponse](t, rec)
	if body.ID != testTenant.ID || len(body.APIKey) != 64 || body.APIKey == testAPIKey {
		t.Fatalf("got id %d key %q", body.ID, body.APIKey)
	}

	// The old key stops working at once; the new one authenticates.
	assertStatus(t, do(t, svc, http.MethodGet, "/tenants/me", "", true), http.StatusUnauthorized)
	req := httptest.NewRequest(http.MethodGet, "/tenants/me", nil)
	req.Header.Set(apiKeyHeader, body.APIKey)
	assertStatus(t, serve(svc, req), http.StatusOK)
}

func TestRotateAPIKeySignedInUserRotatesOnlyTheirTenant(t *testing.T) {
	svc := twoTenantServices()
	rec := serve(svc, userRequest(http.MethodPost, "/tenants/me/api-key/rotate", "", bearer(tokenFor(t, svc, "creator"))))
	assertStatus(t, rec, http.StatusOK)
	body := decode[TenantWithAPIKeyResponse](t, rec)
	if body.ID != tenantA.ID || body.APIKey == tenantA.APIKey {
		t.Fatalf("got id %d key %q", body.ID, body.APIKey)
	}

	keys := svc.tenants.(*fakeTenants).keys
	if len(keys) != 1 || keys[tenantA.ID] != body.APIKey {
		t.Fatalf("rotated keys = %v, want only tenant %d", keys, tenantA.ID)
	}
	for key, want := range map[string]int{
		tenantA.APIKey: http.StatusUnauthorized,
		body.APIKey:    http.StatusOK,
		tenantB.APIKey: http.StatusOK,
		testAPIKey:     http.StatusOK,
	} {
		req := httptest.NewRequest(http.MethodGet, "/tenants/me", nil)
		req.Header.Set(apiKeyHeader, key)
		if rec := serve(svc, req); rec.Code != want {
			t.Errorf("key %q = %d, want %d", key, rec.Code, want)
		}
	}
}

func TestRotateAPIKeyRejections(t *testing.T) {
	assertStatus(t, do(t, testServices(), http.MethodPost, "/tenants/me/api-key/rotate", "", false), http.StatusUnauthorized)

	svc := twoTenantServices()
	rec := serve(svc, userRequest(http.MethodPost, "/tenants/me/api-key/rotate", "", bearer(tokenFor(t, svc, "admin"))))
	assertStatus(t, rec, http.StatusForbidden)

	svc = testServices()
	svc.tenants.(*fakeTenants).err = apperrors.NewInternal(errors.New("boom"))
	assertStatus(t, do(t, svc, http.MethodPost, "/tenants/me/api-key/rotate", "", true), http.StatusInternalServerError)
}

func TestAuthenticateInvalidKey(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/tenants/me", nil)
	req.Header.Set(apiKeyHeader, "wrong")
	rec := httptest.NewRecorder()
	Routes(testServices(), testFeatures, testStripe).ServeHTTP(rec, req)

	assertStatus(t, rec, http.StatusUnauthorized)
	if body := decode[apperrors.HTTPErrorResponse](t, rec); body.Code != apperrors.CodeUnauthorized {
		t.Fatalf("code = %q", body.Code)
	}
}
