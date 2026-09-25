package handlers

import (
	"encoding/json"
	"html"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"app/internal/pkg/forms"
)

func TestAppBuilderBlank(t *testing.T) {
	rec := getAppPath(testServices(), "/app/builder")

	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{`data-draft=""`, `id="palette"`, `id="canvas"`, `id="inspector"`, `aria-current="page">Form builder`, `<script nonce="`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAppBuilderFromForm(t *testing.T) {
	desc := `Say "hi"`
	svc := testServices(forms.Form{
		ID: 1, TenantID: 7, Title: "Survey", Slug: "survey", Description: &desc,
		Content: json.RawMessage(`{"version":1,"fields":[{"type":"toggle","name":"ok"}]}`),
	})

	rec := getAppPath(svc, "/app/builder?form=1")

	assertStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	start := strings.Index(body, `data-draft="`) + len(`data-draft="`)
	end := start + strings.Index(body[start:], `"`)
	var got builderDraft
	if err := json.Unmarshal([]byte(html.UnescapeString(body[start:end])), &got); err != nil {
		t.Fatalf("decode draft %q: %v", body[start:end], err)
	}
	want := builderDraft{Title: "Survey", Slug: "survey-copy", Description: &desc, Content: json.RawMessage(`{"version":1,"fields":[{"type":"toggle","name":"ok"}]}`)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("draft = %+v, want %+v", got, want)
	}
	if !strings.Contains(body, "Copy of “Survey”") {
		t.Error("source form not mentioned")
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
