package handlers

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"app/internal/pkg/forms"
)

func TestAppBuilderBlank(t *testing.T) {
	rec := getAppPath(t, appServices(), "/app/builder")

	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{`data-draft=""`, `data-mode="new"`, `data-form-id=""`, `id="palette"`, `id="canvas"`, `id="inspector"`, `id="code-panel"`, `id="preview-dialog"`, `id="download-code"`, `aria-current="page">Forms`, `<script nonce="`, `src="/static/form-render.js"`, `FormRender.field(f, "preview-" + f.uid)`,
		`id="builder-heading" class="text-sm font-semibold tracking-tight">New form</h1>`, `<span data-request-label class="font-mono">POST /forms</span>`, `credentials: "same-origin"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	for _, unwanted := range []string{testAPIKey, "data-api-key", "X-API-Key", "Workspace API key"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body contains %q", unwanted)
		}
	}
}

// builderSurvey is a form of testTenant to load in the builder.
func builderSurvey(desc *string) forms.Form {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return forms.Form{
		ID: 1, TenantID: 7, Title: "Survey", Slug: "survey", Description: desc, StartDate: &start,
		Content:         json.RawMessage(`{"version":1,"fields":[{"type":"toggle","name":"ok"}]}`),
		AcceptAnonymous: true,
	}
}

// pageDraft decodes the builderDraft of a rendered builder page.
func pageDraft(t *testing.T, body string) builderDraft {
	t.Helper()
	start := strings.Index(body, `data-draft="`) + len(`data-draft="`)
	end := start + strings.Index(body[start:], `"`)
	var got builderDraft
	if err := json.Unmarshal([]byte(html.UnescapeString(body[start:end])), &got); err != nil {
		t.Fatalf("decode draft %q: %v", body[start:end], err)
	}
	return got
}

func TestAppBuilderEditForm(t *testing.T) {
	desc := `Say "hi"`
	f := builderSurvey(&desc)

	rec := getAppPath(t, appServices(f), "/app/builder?form=1")

	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	inactive := false
	want := builderDraft{
		Title: "Survey", Slug: "survey", Description: &desc, IsActive: &inactive, StartDate: f.StartDate,
		Content: f.Content, AcceptAnonymous: true,
	}
	if got := pageDraft(t, body); !reflect.DeepEqual(got, want) {
		t.Fatalf("draft = %+v, want %+v", got, want)
	}
	for _, want := range []string{`data-mode="edit"`, `data-form-id="1"`, "Editing “Survey”",
		`tracking-tight">Edit form</h1>`, `<span data-request-label class="font-mono">PUT /forms/1</span>`, "save your changes"} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	if strings.Contains(body, testAPIKey) {
		t.Error("body contains the API key")
	}
}

func TestAppBuilderCopyForm(t *testing.T) {
	desc := `Say "hi"`
	f := builderSurvey(&desc)

	rec := getAppPath(t, appServices(f), "/app/builder?copy=1")

	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	want := builderDraft{Title: "Survey", Slug: "survey-copy", Description: &desc, Content: f.Content, AcceptAnonymous: true}
	if got := pageDraft(t, body); !reflect.DeepEqual(got, want) {
		t.Fatalf("draft = %+v, want %+v", got, want)
	}
	for _, want := range []string{`data-mode="copy"`, `data-form-id=""`, "Copy of “Survey”",
		`tracking-tight">New form</h1>`, `<span data-request-label class="font-mono">POST /forms</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAppBuilderFormErrors(t *testing.T) {
	svc := appServices(builderSurvey(nil))
	svc.forms.(*fakeForms).forms[2] = forms.Form{ID: 2, TenantID: 8, Title: "Other", Slug: "other"}
	tests := map[string]struct {
		status int
		title  string
	}{
		"/app/builder?form=2":   {http.StatusNotFound, "Form not found"}, // another user's form
		"/app/builder?copy=2":   {http.StatusNotFound, "Form not found"},
		"/app/builder?form=9":   {http.StatusNotFound, "Form not found"},
		"/app/builder?form=abc": {http.StatusBadRequest, "Invalid form"},
		"/app/builder?copy=0":   {http.StatusBadRequest, "Invalid form"},
	}
	for path, tt := range tests {
		rec := getAppPath(t, svc, path)

		assertStatus(t, rec, tt.status)
		body := rec.Body.String()
		if !strings.Contains(body, tt.title) || !strings.Contains(body, "<html") {
			t.Errorf("%s: body is not the %q message page: %s", path, tt.title, body)
		}
		if strings.Contains(body, "Other") || strings.Contains(body, "other") {
			t.Errorf("%s: body shows the other tenant's form", path)
		}
	}
}

func TestCopySlug(t *testing.T) {
	long := strings.Repeat("a", 249) + "-b"
	tests := map[string]string{
		"survey":                 "survey-copy",
		strings.Repeat("a", 250): strings.Repeat("a", 250) + "-copy",
		long:                     strings.Repeat("a", 249) + "-copy", // the cut would leave a trailing hyphen
		strings.Repeat("a", 255): strings.Repeat("a", 250) + "-copy",
	}
	for in, want := range tests {
		got := copySlug(in)
		if got != want || len(got) > maxFormTextLen || !slugPattern.MatchString(got) {
			t.Errorf("copySlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormRenderScript(t *testing.T) {
	rec := httptest.NewRecorder()
	FormRenderScript(rec, httptest.NewRequest(http.MethodGet, "/static/form-render.js", nil))

	assertStatus(t, rec, http.StatusOK)
	h := rec.Header()
	if got := h.Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
	for _, want := range []string{"window.FormRender", "const h = (tag, props, ...children)", "const control = (f, opts)", "const field = (f, id, prefix)", `"data-field": id`,
		"const fromContent = (o)", "const renderContent = (content, prefix)", "case \"panel\":", "case \"content\":", "case \"button\":"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("script does not contain %q", want)
		}
	}
}
