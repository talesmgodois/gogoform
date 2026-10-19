package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/forms"
	"app/internal/pkg/tenants"
)

// ownForm belongs to testTenant; otherForm to another tenant.
var (
	ownForm   = forms.Form{ID: 1, TenantID: testTenant.ID, Title: "Contact", Slug: "contact", IsActive: true, Content: []byte(`{"fields":[]}`), PublicAvailable: true, AcceptAnonymous: true}
	otherForm = forms.Form{ID: 2, TenantID: 99, Title: "Other", Slug: "other", IsActive: true, Content: []byte(`{}`)}
)

const validForm = `{"title":"Contact","slug":"contact-us","content":{"fields":[]},` +
	`"start_date":"2026-01-01T03:00:00+03:00","end_date":"2026-02-01T00:00:00Z"}`

func TestCreateForm(t *testing.T) {
	svc := testServices()
	rec := do(t, svc, http.MethodPost, "/forms", validForm, true)
	assertStatus(t, rec, http.StatusCreated)

	in := svc.forms.(*fakeForms).created
	if in.TenantID != testTenant.ID {
		t.Fatalf("tenant_id = %d, want %d", in.TenantID, testTenant.ID)
	}
	if in.IsActive != nil {
		t.Fatalf("is_active = %v, want nil (default)", *in.IsActive)
	}
	if in.StartDate.Location() != time.UTC || !in.StartDate.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("start_date = %v, want 2026-01-01 00:00 UTC", in.StartDate)
	}
	if body := decode[FormResponse](t, rec); body.Slug != "contact-us" || string(body.Content) != `{"fields":[]}` {
		t.Fatalf("body = %+v", body)
	}
}

func TestCreateFormPlanLimit(t *testing.T) {
	proTenant := tenants.Tenant{ID: 8, Name: "Pro", APIKey: "pro-key", IsActive: true, Plan: tenants.PlanPro}
	tests := []struct {
		name     string
		tenant   tenants.Tenant
		existing int
		want     int
	}{
		{"free below limit", testTenant, 2, http.StatusCreated},
		{"free at limit", testTenant, 3, http.StatusPaymentRequired},
		{"pro at free limit", proTenant, 3, http.StatusCreated},
		{"pro many", proTenant, 50, http.StatusCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fs []forms.Form
			for i := range tt.existing {
				fs = append(fs, forms.Form{ID: int32(100 + i), TenantID: tt.tenant.ID, Slug: fmt.Sprintf("form-%d", i)})
			}
			svc := testServices(fs...)
			svc.tenants = &fakeTenants{owned: map[int32]tenants.Tenant{proTenant.ID: proTenant}}

			req := httptest.NewRequest(http.MethodPost, "/forms", strings.NewReader(validForm))
			req.Header.Set(apiKeyHeader, tt.tenant.APIKey)
			rec := serve(svc, req)
			assertStatus(t, rec, tt.want)

			created := svc.forms.(*fakeForms).created
			if tt.want != http.StatusCreated {
				if created.Slug != "" {
					t.Fatalf("form created over the limit: %+v", created)
				}
				body := decode[apperrors.HTTPErrorResponse](t, rec)
				if body.Code != apperrors.CodePlanLimitReached || !strings.Contains(body.Message, "Upgrade to Pro") {
					t.Fatalf("body = %+v", body)
				}
				return
			}
			if created.TenantID != tt.tenant.ID {
				t.Fatalf("tenant_id = %d, want %d", created.TenantID, tt.tenant.ID)
			}
		})
	}
}

func TestCreateFormInvalid(t *testing.T) {
	tests := map[string]string{
		"missing title":   `{"slug":"a","content":{}}`,
		"bad slug":        `{"title":"a","slug":"Not A Slug","content":{}}`,
		"missing content": `{"title":"a","slug":"a"}`,
		"null content":    `{"title":"a","slug":"a","content":null}`,
		"dates reversed":  `{"title":"a","slug":"a","content":{},"start_date":"2026-02-01T00:00:00Z","end_date":"2026-01-01T00:00:00Z"}`,
		"unknown field":   `{"title":"a","slug":"a","content":{},"tenant_id":2}`,
		"malformed JSON":  `{"title":`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			assertStatus(t, do(t, testServices(), http.MethodPost, "/forms", body, true), http.StatusBadRequest)
		})
	}
}

func TestListForms(t *testing.T) {
	svc := testServices(ownForm, otherForm)
	rec := do(t, svc, http.MethodGet, "/forms?is_active=true&search=%20con%20&offset=5&limit=10", "", true)
	assertStatus(t, rec, http.StatusOK)

	fake := svc.forms.(*fakeForms)
	if fake.filter.TenantID != testTenant.ID || *fake.filter.IsActive != true || *fake.filter.Search != "con" {
		t.Fatalf("filter = %+v", fake.filter)
	}
	if fake.page != (forms.Page{Offset: 5, Limit: 10}) {
		t.Fatalf("page = %+v", fake.page)
	}
	body := decode[FormListResponse](t, rec)
	if len(body.Items) != 1 || body.Items[0].ID != ownForm.ID || body.Items[0].SubmissionCount != 3 || body.Total != 1 {
		t.Fatalf("body = %+v", body)
	}
}

func TestListFormsDefaultsAndInvalidQuery(t *testing.T) {
	svc := testServices()
	rec := do(t, svc, http.MethodGet, "/forms", "", true)
	assertStatus(t, rec, http.StatusOK)
	if page := svc.forms.(*fakeForms).page; page != (forms.Page{Offset: 0, Limit: defaultPageLimit}) {
		t.Fatalf("page = %+v", page)
	}
	if body := rec.Body.String(); body == "" || decode[FormListResponse](t, rec).Items == nil {
		t.Fatalf("items must be an empty array, got %s", body)
	}

	for _, q := range []string{"limit=0", "limit=101", "offset=-1", "limit=x", "is_active=maybe"} {
		assertStatus(t, do(t, testServices(), http.MethodGet, "/forms?"+q, "", true), http.StatusBadRequest)
	}
}

func TestGetForm(t *testing.T) {
	svc := testServices(ownForm, otherForm)
	rec := do(t, svc, http.MethodGet, "/forms/1", "", true)
	assertStatus(t, rec, http.StatusOK)
	if body := decode[FormDetailsResponse](t, rec); body.ID != 1 || body.TenantName != "Acme" {
		t.Fatalf("body = %+v", body)
	}

	assertStatus(t, do(t, svc, http.MethodGet, "/forms/2", "", true), http.StatusNotFound)
	assertStatus(t, do(t, svc, http.MethodGet, "/forms/abc", "", true), http.StatusBadRequest)
	assertStatus(t, do(t, svc, http.MethodGet, "/forms/0", "", true), http.StatusBadRequest)
}

func TestUpdateForm(t *testing.T) {
	svc := testServices(ownForm, otherForm)
	rec := do(t, svc, http.MethodPut, "/forms/1", `{"title":"New","slug":"new","content":{},"is_active":false}`, true)
	assertStatus(t, rec, http.StatusOK)
	in := svc.forms.(*fakeForms).updated
	if in.ID != 1 || in.TenantID != testTenant.ID || in.IsActive || in.Title != "New" {
		t.Fatalf("input = %+v", in)
	}

	rec = do(t, svc, http.MethodPut, "/forms/1", `{"title":"New","slug":"new","content":{}}`, true)
	assertStatus(t, rec, http.StatusOK)
	if !svc.forms.(*fakeForms).updated.IsActive {
		t.Fatal("omitted is_active must default to true")
	}

	assertStatus(t, do(t, svc, http.MethodPut, "/forms/2", `{"title":"a","slug":"a","content":{}}`, true), http.StatusNotFound)
}

func TestNotifyOnSubmissionPlanGate(t *testing.T) {
	const createBody = `{"title":"Contact","slug":"contact-notify","content":{"fields":[]},"notify_on_submission":true}`
	const updateBody = `{"title":"Contact","slug":"contact","content":{"fields":[]},"notify_on_submission":true}`

	t.Run("free create rejected", func(t *testing.T) {
		svc := testServices()
		rec := do(t, svc, http.MethodPost, "/forms", createBody, true)
		assertStatus(t, rec, http.StatusForbidden)
		if body := decode[apperrors.HTTPErrorResponse](t, rec); body.Code != apperrors.CodePlanFeatureUnavailable || !strings.Contains(body.Message, "Pro") {
			t.Fatalf("body = %+v", body)
		}
		if created := svc.forms.(*fakeForms).created; created.Slug != "" {
			t.Fatalf("form created without the plan feature: %+v", created)
		}
	})

	t.Run("free update rejected", func(t *testing.T) {
		svc := testServices(ownForm)
		rec := do(t, svc, http.MethodPut, "/forms/1", updateBody, true)
		assertStatus(t, rec, http.StatusForbidden)
		if body := decode[apperrors.HTTPErrorResponse](t, rec); body.Code != apperrors.CodePlanFeatureUnavailable {
			t.Fatalf("code = %q, want %q", body.Code, apperrors.CodePlanFeatureUnavailable)
		}
		if updated := svc.forms.(*fakeForms).updated; updated.ID != 0 {
			t.Fatalf("form updated without the plan feature: %+v", updated)
		}
	})

	t.Run("pro create saved", func(t *testing.T) {
		svc := testServices()
		svc.tenants = &fakeTenants{owned: map[int32]tenants.Tenant{proTenant.ID: proTenant}}
		rec := doAs(svc, proTenant.APIKey, http.MethodPost, "/forms", createBody)
		assertStatus(t, rec, http.StatusCreated)
		if created := svc.forms.(*fakeForms).created; !created.NotifyOnSubmission {
			t.Fatalf("notify_on_submission = %v, want true", created.NotifyOnSubmission)
		}
	})

	t.Run("pro update saved", func(t *testing.T) {
		proForm := ownForm
		proForm.TenantID = proTenant.ID
		svc := testServices(proForm)
		svc.tenants = &fakeTenants{owned: map[int32]tenants.Tenant{proTenant.ID: proTenant}}
		rec := doAs(svc, proTenant.APIKey, http.MethodPut, "/forms/1", updateBody)
		assertStatus(t, rec, http.StatusOK)
		if updated := svc.forms.(*fakeForms).updated; !updated.NotifyOnSubmission {
			t.Fatalf("notify_on_submission = %v, want true", updated.NotifyOnSubmission)
		}
	})
}

func TestFormPublishRequiresVerifiedEmail(t *testing.T) {
	const email = "dana@example.com"
	draftForm := forms.Form{ID: 10, TenantID: 50, Title: "Draft", Slug: "contact-draft", Content: []byte(`{"fields":[]}`)}
	svc := testServices(draftForm)

	rec := serve(svc, userRequest(http.MethodPost, "/auth/signup", `{"email":"`+email+`","password":"`+testPassword+`"}`, ""))
	assertStatus(t, rec, http.StatusCreated)
	signUp := decode[SignUpResponse](t, rec)

	svc.tenants = &fakeTenants{owned: map[int32]tenants.Tenant{
		signUp.ID: {ID: 50, Name: "Dana", APIKey: "dana-key", IsActive: true, OwnerUserID: &signUp.ID},
	}}

	signin := serve(svc, userRequest(http.MethodPost, "/app/signin", "", basicHeader(email, testPassword)))
	assertStatus(t, signin, http.StatusOK)
	cookie := signin.Result().Cookies()[0]

	send := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(cookie)
		return serve(svc, req)
	}

	// Publishing (is_active true, the default) is rejected while unverified.
	rec = send(http.MethodPost, "/forms", `{"title":"Contact","slug":"contact-active","content":{"fields":[]}}`)
	assertStatus(t, rec, http.StatusForbidden)
	if body := decode[apperrors.HTTPErrorResponse](t, rec); body.Code != apperrors.CodeEmailUnverified {
		t.Fatalf("code = %q, want %q", body.Code, apperrors.CodeEmailUnverified)
	}

	// Saving a draft (is_active false) is unaffected.
	assertStatus(t, send(http.MethodPost, "/forms", `{"title":"Draft","slug":"contact-draft-2","content":{"fields":[]},"is_active":false}`), http.StatusCreated)

	// Activating an existing draft through an update is rejected too.
	assertStatus(t, send(http.MethodPut, "/forms/10", `{"title":"Draft","slug":"contact-draft","content":{"fields":[]},"is_active":true}`), http.StatusForbidden)

	// Once verified, both creating and activating succeed.
	token := lastVerificationToken(t, svc, email)
	assertStatus(t, serve(svc, httptest.NewRequest(http.MethodGet, "/app/verify?token="+token, nil)), http.StatusOK)

	assertStatus(t, send(http.MethodPut, "/forms/10", `{"title":"Draft","slug":"contact-draft","content":{"fields":[]},"is_active":true}`), http.StatusOK)
	assertStatus(t, send(http.MethodPost, "/forms", `{"title":"Contact","slug":"contact-verified","content":{"fields":[]}}`), http.StatusCreated)
}

func TestDeleteForm(t *testing.T) {
	svc := testServices(ownForm, otherForm)
	assertStatus(t, do(t, svc, http.MethodDelete, "/forms/2", "", true), http.StatusNotFound)
	rec := do(t, svc, http.MethodDelete, "/forms/1", "", true)
	assertStatus(t, rec, http.StatusNoContent)
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", rec.Body.String())
	}
}

func TestGetPublicForm(t *testing.T) {
	svc := testServices(ownForm)
	rec := do(t, svc, http.MethodGet, "/public/forms/contact", "", false)
	assertStatus(t, rec, http.StatusOK)
	if body := decode[PublicFormResponse](t, rec); body.Slug != "contact" || string(body.Content) != `{"fields":[]}` {
		t.Fatalf("body = %+v", body)
	}
	assertStatus(t, do(t, svc, http.MethodGet, "/public/forms/nope", "", false), http.StatusNotFound)
}
