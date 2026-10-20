package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	apperrors "app/internal/errors"
	"app/internal/pkg/tenants"
	"app/internal/pkg/webhooks"
)

// proTenant is a tenant on the pro plan, whose plan includes webhooks.
var proTenant = tenants.Tenant{ID: 8, Name: "Pro", APIKey: "pro-key", IsActive: true, Plan: tenants.PlanPro}

// proServices is testServices where proTenant owns form 1 (a copy of
// ownForm) and otherForm belongs to another tenant.
func proServices() Services {
	proForm := ownForm
	proForm.TenantID = proTenant.ID
	svc := testServices(proForm, otherForm)
	svc.tenants = &fakeTenants{owned: map[int32]tenants.Tenant{proTenant.ID: proTenant}}
	return svc
}

// doAs sends a request through Routes(svc, testFeatures, testStripe, testApp, testSecurity),
// authenticated with apiKey.
func doAs(svc Services, apiKey, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(apiKeyHeader, apiKey)
	return serve(svc, req)
}

func TestCreateWebhook(t *testing.T) {
	svc := proServices()
	rec := doAs(svc, proTenant.APIKey, http.MethodPost, "/forms/1/webhooks", `{"target_url":"https://example.com/hook","secret_token":"s3cr3t"}`)
	assertStatus(t, rec, http.StatusCreated)

	in := svc.webhooks.(*fakeWebhooks).created
	if in.FormID != 1 || in.TargetURL != "https://example.com/hook" || *in.SecretToken != "s3cr3t" {
		t.Fatalf("input = %+v", in)
	}
	if strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Fatalf("response leaks the secret: %s", rec.Body.String())
	}
	if body := decode[WebhookResponse](t, rec); !body.HasSecret || body.ID != 5 {
		t.Fatalf("body = %+v", body)
	}

	assertStatus(t, doAs(svc, proTenant.APIKey, http.MethodPost, "/forms/2/webhooks", `{"target_url":"https://example.com"}`), http.StatusNotFound)
}

func TestCreateWebhookInvalid(t *testing.T) {
	svc := proServices()
	for _, body := range []string{
		`{"target_url":""}`,
		`{"target_url":"https://example.com","secret_token":""}`,
		`{"target_url":"https://example.com","secret_token":"` + strings.Repeat("x", 101) + `"}`,
	} {
		assertStatus(t, doAs(svc, proTenant.APIKey, http.MethodPost, "/forms/1/webhooks", body), http.StatusBadRequest)
	}
}

func TestValidateTargetURL(t *testing.T) {
	addr := netip.MustParseAddr
	resolver := fakeResolver{
		"internal.example.com": {addr("10.0.0.5")},
		"mixed.example.com":    {addr("203.0.113.20"), addr("127.0.0.1")},
		"cgnat.example.com":    {addr("100.64.1.1")},
		"mapped.example.com":   {addr("::ffff:192.168.1.1")},
		"v6.example.com":       {addr("2001:db8::1"), addr("203.0.113.21")},
		"nxdomain.example.com": {},
	}
	valid := []string{
		"https://example.com/hook", "http://203.0.113.7:8080/x", " https://api.example.com ",
		"https://v6.example.com/hook", "http://[2001:db8::1]/x",
	}
	for _, raw := range valid {
		if _, err := validateTargetURL(context.Background(), resolver, raw); err != nil {
			t.Errorf("validateTargetURL(%q) = %v, want nil", raw, err)
		}
	}
	invalid := []string{
		"ftp://example.com", "example.com/hook", "https://", "https://user:pass@example.com",
		"http://localhost:8080", "http://LOCALHOST.", "http://app.localhost",
		"http://127.0.0.1", "http://10.1.2.3", "http://192.168.0.1", "http://169.254.169.254/latest",
		"http://0.0.0.0", "http://[::1]", "http://[::ffff:127.0.0.1]", "http://[fe80::1]", "http://[fd00::1]",
		"http://100.64.0.1", "http://255.255.255.255", "http://224.0.0.1", "http://[64:ff9b::a00:1]",
		"https://internal.example.com/hook", "https://mixed.example.com", "https://cgnat.example.com",
		"https://mapped.example.com", "https://nxdomain.example.com",
	}
	for _, raw := range invalid {
		if _, err := validateTargetURL(context.Background(), resolver, raw); err == nil {
			t.Errorf("validateTargetURL(%q) = nil, want error", raw)
		}
	}
}

func TestCreateWebhookRejectsHostResolvingToPrivateIP(t *testing.T) {
	svc := proServices()
	svc.resolver = fakeResolver{"metadata.example.com": {netip.MustParseAddr("169.254.169.254")}}
	rec := doAs(svc, proTenant.APIKey, http.MethodPost, "/forms/1/webhooks", `{"target_url":"http://metadata.example.com/latest"}`)
	assertStatus(t, rec, http.StatusBadRequest)
	if in := svc.webhooks.(*fakeWebhooks).created; in.TargetURL != "" {
		t.Fatalf("webhook created: %+v", in)
	}
}

func TestListWebhooks(t *testing.T) {
	svc := proServices()
	secret := "s"
	svc.webhooks.(*fakeWebhooks).list = []webhooks.Webhook{{ID: 1, FormID: 1, TargetURL: "https://a.example", SecretToken: &secret, IsActive: true}}

	rec := doAs(svc, proTenant.APIKey, http.MethodGet, "/forms/1/webhooks", "")
	assertStatus(t, rec, http.StatusOK)
	if body := decode[WebhookListResponse](t, rec); len(body.Items) != 1 || !body.Items[0].HasSecret {
		t.Fatalf("body = %+v", body)
	}
	assertStatus(t, doAs(svc, proTenant.APIKey, http.MethodGet, "/forms/2/webhooks", ""), http.StatusNotFound)
}

func TestWebhooksPlanGate(t *testing.T) {
	const hook = `{"target_url":"https://example.com/hook"}`
	tests := []struct {
		name         string
		apiKey       string
		method, body string
		want         int
	}{
		{"free create", testAPIKey, http.MethodPost, hook, http.StatusForbidden},
		{"free list", testAPIKey, http.MethodGet, "", http.StatusForbidden},
		{"pro create", proTenant.APIKey, http.MethodPost, hook, http.StatusCreated},
		{"pro list", proTenant.APIKey, http.MethodGet, "", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			freeForm := ownForm
			freeForm.ID = 3
			svc := proServices()
			svc.forms.(*fakeForms).forms[freeForm.ID] = freeForm
			path := "/forms/1/webhooks"
			if tt.apiKey == testAPIKey {
				path = "/forms/3/webhooks"
			}

			rec := doAs(svc, tt.apiKey, tt.method, path, tt.body)
			assertStatus(t, rec, tt.want)
			if tt.want != http.StatusForbidden {
				return
			}
			if body := decode[apperrors.HTTPErrorResponse](t, rec); body.Code != apperrors.CodePlanFeatureUnavailable || !strings.Contains(body.Message, "Pro") {
				t.Fatalf("body = %+v", body)
			}
			if in := svc.webhooks.(*fakeWebhooks).created; in.FormID != 0 {
				t.Fatalf("webhook created without the plan feature: %+v", in)
			}
		})
	}
}

// A free tenant still gets 404 for another tenant's form, not the plan error.
func TestWebhooksPlanGateKeepsNotFound(t *testing.T) {
	svc := testServices(ownForm, otherForm)
	assertStatus(t, doAs(svc, testAPIKey, http.MethodGet, "/forms/2/webhooks", ""), http.StatusNotFound)
	assertStatus(t, doAs(svc, testAPIKey, http.MethodPost, "/forms/2/webhooks", `{"target_url":"https://example.com/hook"}`), http.StatusNotFound)
}
