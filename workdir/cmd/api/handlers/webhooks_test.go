package handlers

import (
	"net/http"
	"strings"
	"testing"

	"app/internal/pkg/webhooks"
)

func TestCreateWebhook(t *testing.T) {
	svc := testServices(ownForm, otherForm)
	rec := do(t, svc, http.MethodPost, "/forms/1/webhooks", `{"target_url":"https://example.com/hook","secret_token":"s3cr3t"}`, true)
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

	assertStatus(t, do(t, svc, http.MethodPost, "/forms/2/webhooks", `{"target_url":"https://example.com"}`, true), http.StatusNotFound)
}

func TestCreateWebhookInvalid(t *testing.T) {
	svc := testServices(ownForm)
	for _, body := range []string{
		`{"target_url":""}`,
		`{"target_url":"https://example.com","secret_token":""}`,
		`{"target_url":"https://example.com","secret_token":"` + strings.Repeat("x", 101) + `"}`,
	} {
		assertStatus(t, do(t, svc, http.MethodPost, "/forms/1/webhooks", body, true), http.StatusBadRequest)
	}
}

func TestValidateTargetURL(t *testing.T) {
	valid := []string{"https://example.com/hook", "http://203.0.113.7:8080/x", " https://api.example.com "}
	for _, raw := range valid {
		if _, err := validateTargetURL(raw); err != nil {
			t.Errorf("validateTargetURL(%q) = %v, want nil", raw, err)
		}
	}
	invalid := []string{
		"ftp://example.com", "example.com/hook", "https://", "https://user:pass@example.com",
		"http://localhost:8080", "http://LOCALHOST.", "http://app.localhost",
		"http://127.0.0.1", "http://10.1.2.3", "http://192.168.0.1", "http://169.254.169.254/latest",
		"http://0.0.0.0", "http://[::1]", "http://[::ffff:127.0.0.1]", "http://[fe80::1]", "http://[fd00::1]",
	}
	for _, raw := range invalid {
		if _, err := validateTargetURL(raw); err == nil {
			t.Errorf("validateTargetURL(%q) = nil, want error", raw)
		}
	}
}

func TestListWebhooks(t *testing.T) {
	svc := testServices(ownForm, otherForm)
	secret := "s"
	svc.webhooks.(*fakeWebhooks).list = []webhooks.Webhook{{ID: 1, FormID: 1, TargetURL: "https://a.example", SecretToken: &secret, IsActive: true}}

	rec := do(t, svc, http.MethodGet, "/forms/1/webhooks", "", true)
	assertStatus(t, rec, http.StatusOK)
	if body := decode[WebhookListResponse](t, rec); len(body.Items) != 1 || !body.Items[0].HasSecret {
		t.Fatalf("body = %+v", body)
	}
	assertStatus(t, do(t, svc, http.MethodGet, "/forms/2/webhooks", "", true), http.StatusNotFound)
}
