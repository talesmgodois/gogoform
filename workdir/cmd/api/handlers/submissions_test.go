package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/files"
	"app/internal/pkg/forms"
	"app/internal/pkg/submissions"
)

// surveyForm is a public form with an input of each kind the submission
// validation checks.
var surveyForm = forms.Form{
	ID: 30, TenantID: testTenant.ID, Title: "Survey", Slug: "survey", IsActive: true, PublicAvailable: true, AcceptAnonymous: true,
	Content: []byte(`{"fields":[
		{"type":"textfield","name":"email","input_type":"email","required":true},
		{"type":"select","name":"color","options":[{"label":"Red","value":"red"}]},
		{"type":"panel","title":"More","fields":[
			{"type":"toggle","name":"agree"},
			{"type":"file","name":"photo","multiple":true}
		]},
		{"type":"content","text":"Thanks"}
	]}`),
}

// Files of surveyForm's tenant and of another tenant.
const (
	ownFileID   = "0b6a3b2e-8f1c-4c4e-9d5e-2f1a7c3b9e10"
	otherFileID = "1c7b4c3f-9a2d-4d5f-8e6f-3a2b8d4c0f21"
	missingID   = "2d8c5d4a-0b3e-4e6a-9f7a-4b3c9e5d1a32"
)

// surveyServices returns test services holding surveyForm and the files
// ownFileID and otherFileID.
func surveyServices(fs ...forms.Form) Services {
	svc := testServices(append([]forms.Form{surveyForm}, fs...)...)
	svc.files.(*fakeFiles).files = map[string]files.File{
		ownFileID:   {ID: ownFileID, TenantID: testTenant.ID},
		otherFileID: {ID: otherFileID, TenantID: 99},
	}
	return svc
}

func TestCreateSubmission(t *testing.T) {
	svc := surveyServices()
	payload := `{"email":"a@b.c","color":"red","agree":true,"photo":["` + ownFileID + `"]}`
	req := httptest.NewRequest(http.MethodPost, "/public/forms/survey/submissions",
		strings.NewReader(`{"payload":`+payload+`,"completion_time_seconds":42}`))
	req.RemoteAddr = "203.0.113.7:5555"
	req.Header.Set("User-Agent", "test-agent")
	req.Header.Set("Referer", "https://example.com/contact")
	req.Header.Set("X-Forwarded-For", "10.0.0.1")
	rec := httptest.NewRecorder()
	Routes(svc, testFeatures, testStripe).ServeHTTP(rec, req)
	assertStatus(t, rec, http.StatusCreated)

	fake := svc.submissions.(*fakeSubmissions)
	if fake.created.FormID != surveyForm.ID || string(fake.created.Payload) != payload {
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
	for _, body := range []string{
		`{}`, `{"payload":null}`, `{"payload":{},"completion_time_seconds":-1}`, `{"payload":{},"x":1}`,
		`{"payload":[]}`, `{"payload":"a"}`, `{"payload":1}`, `{"payload":true}`,
	} {
		assertStatus(t, do(t, svc, http.MethodPost, "/public/forms/contact/submissions", body, false), http.StatusBadRequest)
	}
	assertStatus(t, do(t, svc, http.MethodPost, "/public/forms/nope/submissions", `{"payload":{}}`, false), http.StatusNotFound)
	if fake := svc.submissions.(*fakeSubmissions); fake.created.FormID != 0 {
		t.Fatalf("created = %+v", fake.created)
	}
}

func TestCreateSubmissionValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    map[string]string
	}{
		{"missing required", `{}`, map[string]string{"email": forms.MsgRequired}},
		{"bad email", `{"email":"nope"}`, map[string]string{"email": forms.MsgInvalidEmail}},
		{"unknown key", `{"email":"a@b.c","age":3}`, map[string]string{"age": forms.MsgUnknownField}},
		{"layout key", `{"email":"a@b.c","More":"x"}`, map[string]string{"More": forms.MsgUnknownField}},
		{"option and types", `{"email":"a@b.c","color":"blue","agree":"yes","photo":"x"}`, map[string]string{
			"color": forms.MsgNotOption, "agree": forms.MsgNotBool, "photo": forms.MsgNotFileIDs,
		}},
		{"other tenant's file", `{"email":"a@b.c","photo":["` + ownFileID + `","` + otherFileID + `"]}`, map[string]string{"photo": msgFileNotFound}},
		{"missing file", `{"email":"a@b.c","photo":["` + missingID + `"]}`, map[string]string{"photo": msgFileNotFound}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := surveyServices()
			rec := do(t, svc, http.MethodPost, "/public/forms/survey/submissions", `{"payload":`+tt.payload+`}`, false)
			assertStatus(t, rec, http.StatusBadRequest)
			body := decode[apperrors.HTTPErrorResponse](t, rec)
			if body.Code != apperrors.CodeInvalidArgument || body.Message != msgInvalidAnswers || len(body.Fields) != len(tt.want) {
				t.Fatalf("body = %+v, want fields %v", body, tt.want)
			}
			for k, v := range tt.want {
				if body.Fields[k] != v {
					t.Fatalf("fields = %v, want %v", body.Fields, tt.want)
				}
			}
			if fake := svc.submissions.(*fakeSubmissions); fake.created.FormID != 0 {
				t.Fatalf("created = %+v", fake.created)
			}
		})
	}
}

func TestCreateSubmissionInvalidDefinition(t *testing.T) {
	broken := surveyForm
	broken.ID, broken.Slug, broken.Content = 31, "broken", []byte(`{"fields":[{"type":"slider","name":"x"}]}`)
	svc := surveyServices(broken)
	rec := do(t, svc, http.MethodPost, "/public/forms/broken/submissions", `{"payload":{}}`, false)
	assertStatus(t, rec, http.StatusInternalServerError)
	if fake := svc.submissions.(*fakeSubmissions); fake.created.FormID != 0 {
		t.Fatalf("created = %+v", fake.created)
	}
}

func TestCreateSubmissionAvailability(t *testing.T) {
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	form := func(id int32, slug string, edit func(*forms.Form)) forms.Form {
		f := surveyForm
		f.ID, f.Slug = id, slug
		edit(&f)
		return f
	}
	svc := surveyServices(
		form(40, "inactive", func(f *forms.Form) { f.IsActive = false }),
		form(41, "early", func(f *forms.Form) { f.StartDate = &future }),
		form(42, "ended", func(f *forms.Form) { f.EndDate = &past }),
		form(43, "orphan", func(f *forms.Form) { f.TenantID = 77 }),
		form(44, "private", func(f *forms.Form) { f.PublicAvailable = false }),
	)
	svc.forms.(*fakeForms).tenantInactive = map[int32]bool{77: true}
	body := `{"payload":{"email":"a@b.c"}}`

	for slug, want := range map[string]string{
		"inactive": "form is not accepting responses",
		"orphan":   "form is not accepting responses",
		"early":    "form opens on " + future.UTC().Format(time.RFC3339),
		"ended":    "form closed on " + past.UTC().Format(time.RFC3339),
	} {
		rec := do(t, svc, http.MethodPost, "/public/forms/"+slug+"/submissions", body, false)
		assertStatus(t, rec, http.StatusForbidden)
		if got := decode[apperrors.HTTPErrorResponse](t, rec); got.Message != want {
			t.Errorf("%s: message = %q, want %q", slug, got.Message, want)
		}
	}
	assertStatus(t, do(t, svc, http.MethodPost, "/public/forms/private/submissions", body, false), http.StatusUnauthorized)
	if fake := svc.submissions.(*fakeSubmissions); fake.created.FormID != 0 {
		t.Fatalf("created = %+v", fake.created)
	}

	req := httptest.NewRequest(http.MethodPost, "/public/forms/private/submissions", strings.NewReader(body))
	req.Header.Set("Authorization", bearer(tokenFor(t, svc, "basic")))
	assertStatus(t, serve(svc, req), http.StatusCreated)
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
