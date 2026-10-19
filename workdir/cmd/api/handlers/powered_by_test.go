package handlers

import (
	"html/template"
	"strings"
	"testing"

	"app/internal/pkg/tenants"
)

func TestShowPoweredBy(t *testing.T) {
	tests := []struct {
		plan tenants.Plan
		want bool
	}{
		{tenants.PlanFree, true},
		{tenants.PlanPro, false},
		{"", true},
		{"enterprise", true},
	}
	for _, tt := range tests {
		if got := showPoweredBy(tt.plan); got != tt.want {
			t.Errorf("showPoweredBy(%q) = %v, want %v", tt.plan, got, tt.want)
		}
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
