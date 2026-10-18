package handlers

import (
	"context"
	"html/template"
	"strings"
	"testing"
)

func TestShowPoweredByAlwaysForNow(t *testing.T) {
	if !showPoweredBy(context.Background(), 7) {
		t.Fatal("showPoweredBy = false, want true until plans exist")
	}
}

func TestPoweredByPartial(t *testing.T) {
	tmpl := template.Must(template.ParseFS(templatesFS, "templates/powered_by.html"))
	for _, show := range []bool{true, false} {
		var b strings.Builder
		if err := tmpl.ExecuteTemplate(&b, "powered-by", show); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(b.String(), "Powered by gogoform"); got != show {
			t.Errorf("show=%v: badge rendered = %v, body %q", show, got, b.String())
		}
	}
}
