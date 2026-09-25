package handlers

import (
	"net/http"
	"testing"
	"time"

	"app/internal/pkg/forms"
)

// ownForm belongs to testTenant; otherForm to another tenant.
var (
	ownForm   = forms.Form{ID: 1, TenantID: testTenant.ID, Title: "Contact", Slug: "contact", IsActive: true, Content: []byte(`{"fields":[]}`)}
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
