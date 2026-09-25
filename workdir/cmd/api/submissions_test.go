package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"app/internal/pkg/submissions"
)

func TestCreateSubmission(t *testing.T) {
	svc := testServices(ownForm)
	req := httptest.NewRequest(http.MethodPost, "/public/forms/contact/submissions",
		strings.NewReader(`{"payload":{"email":"a@b.c"},"completion_time_seconds":42}`))
	req.RemoteAddr = "203.0.113.7:5555"
	req.Header.Set("User-Agent", "test-agent")
	req.Header.Set("Referer", "https://example.com/contact")
	req.Header.Set("X-Forwarded-For", "10.0.0.1")
	rec := httptest.NewRecorder()
	routes(svc, testAppConfig).ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	fake := svc.submissions.(*fakeSubmissions)
	if fake.created.FormID != ownForm.ID || string(fake.created.Payload) != `{"email":"a@b.c"}` {
		t.Fatalf("created = %+v", fake.created)
	}
	m := fake.metadata
	if *m.IPAddress != "203.0.113.7" || *m.UserAgent != "test-agent" || *m.Referer != "https://example.com/contact" || *m.CompletionTimeSeconds != 42 {
		t.Fatalf("metadata = ip %v ua %v ref %v time %v", *m.IPAddress, *m.UserAgent, *m.Referer, *m.CompletionTimeSeconds)
	}
	if body := decode[SubmissionResponse](t, rec); body.ID != 11 || body.Metadata == nil {
		t.Fatalf("body = %+v", body)
	}
}

func TestCreateSubmissionMetadataFailureStillSucceeds(t *testing.T) {
	svc := testServices(ownForm)
	svc.submissions.(*fakeSubmissions).metadataErr = errors.New("db down")
	rec := do(t, svc, http.MethodPost, "/public/forms/contact/submissions", `{"payload":{}}`, false)
	assertStatus(t, rec, http.StatusCreated)
	if body := decode[SubmissionResponse](t, rec); body.Metadata != nil {
		t.Fatalf("metadata = %+v, want nil", body.Metadata)
	}
}

func TestCreateSubmissionInvalid(t *testing.T) {
	svc := testServices(ownForm)
	for _, body := range []string{`{}`, `{"payload":null}`, `{"payload":{},"completion_time_seconds":-1}`, `{"payload":{},"x":1}`} {
		assertStatus(t, do(t, svc, http.MethodPost, "/public/forms/contact/submissions", body, false), http.StatusBadRequest)
	}
	assertStatus(t, do(t, svc, http.MethodPost, "/public/forms/nope/submissions", `{"payload":{}}`, false), http.StatusNotFound)
}

func TestListSubmissions(t *testing.T) {
	svc := testServices(ownForm, otherForm)
	fake := svc.submissions.(*fakeSubmissions)
	ip := "203.0.113.7"
	fake.list = []submissions.Submission{
		{ID: 2, FormID: 1, Payload: []byte(`{}`), Metadata: &submissions.Metadata{IPAddress: &ip}},
		{ID: 1, FormID: 1, Payload: []byte(`{}`)},
	}

	rec := do(t, svc, http.MethodGet, "/forms/1/submissions?limit=2", "", true)
	assertStatus(t, rec, http.StatusOK)
	if fake.filter != (submissions.ListSubmissionsFilter{TenantID: testTenant.ID, FormID: 1}) || fake.page.Limit != 2 {
		t.Fatalf("filter = %+v, page = %+v", fake.filter, fake.page)
	}
	body := decode[SubmissionListResponse](t, rec)
	if len(body.Items) != 2 || *body.Items[0].Metadata.IPAddress != ip || body.Items[1].Metadata != nil {
		t.Fatalf("body = %+v", body)
	}

	assertStatus(t, do(t, svc, http.MethodGet, "/forms/2/submissions", "", true), http.StatusNotFound)
	assertStatus(t, do(t, svc, http.MethodGet, "/forms/1/submissions?limit=500", "", true), http.StatusBadRequest)
}
