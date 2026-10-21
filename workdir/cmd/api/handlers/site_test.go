package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"app/internal/config"
	"app/internal/pkg/plans"
	"app/internal/pkg/tenants"
)

func TestSitePages(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{"/", []string{"GoGoForm", `href="/app/signin?mode=signup"`, `href="/pricing"`}},
		{"/pricing", []string{"Free", "Pro", "9 USD", "per month", "Stripe", `data-plan-cta="pro"`, `href="/app/signin?mode=signup`}},
		{"/terms", []string{"Placeholder, to be reviewed", "Stripe", "on your behalf", "data-legal-placeholder"}},
		{"/privacy", []string{"Placeholder, to be reviewed", "Stripe", "on behalf of the form owner", "data-legal-placeholder"}},
	}
	svc := testServices()
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := serve(svc, httptest.NewRequest(http.MethodGet, tt.path, nil))
			assertStatus(t, rec, http.StatusOK)
			if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "'nonce-") {
				t.Errorf("Content-Security-Policy = %q, want a nonce", csp)
			}
			body := rec.Body.String()
			for _, want := range append(tt.want, `href="/terms"`, `href="/privacy"`) {
				if !strings.Contains(body, want) {
					t.Errorf("body does not contain %q", want)
				}
			}
		})
	}
}

func TestSiteUnknownPathIsNotFound(t *testing.T) {
	rec := serve(testServices(), httptest.NewRequest(http.MethodGet, "/nope", nil))
	assertStatus(t, rec, http.StatusNotFound)
}

// TestPricingFollowsPlanLimits checks that the pricing page shows the limits
// plans enforces, so it cannot drift from them.
func TestPricingFollowsPlanLimits(t *testing.T) {
	body := serve(testServices(), httptest.NewRequest(http.MethodGet, "/pricing", nil)).Body.String()
	free, pro := plans.For(tenants.PlanFree), plans.For(tenants.PlanPro)
	card := func(plan string) string {
		start := strings.Index(body, `data-plan="`+plan+`"`)
		if start < 0 {
			t.Fatalf("no %s plan card", plan)
		}
		end := strings.Index(body[start:], "</section>")
		return body[start : start+end]
	}
	freeCard, proCard := card("free"), card("pro")
	for _, want := range []string{formatCount(free.MaxForms) + "</span> forms", formatCount(free.MaxSubmissionsPerMonth) + "</span> submissions per month", formatSize(free.MaxStorageBytes) + "</span> file storage"} {
		if !strings.Contains(freeCard, want) {
			t.Errorf("free card does not contain %q", want)
		}
	}
	for _, want := range []string{"Unlimited</span> forms", formatCount(pro.MaxSubmissionsPerMonth) + "</span> submissions per month", formatSize(pro.MaxStorageBytes) + "</span> file storage"} {
		if !strings.Contains(proCard, want) {
			t.Errorf("pro card does not contain %q", want)
		}
	}
	if strings.Contains(freeCard, "(not included)") != !free.Webhooks || strings.Contains(proCard, "(not included)") {
		t.Error("feature lines do not follow the plan features")
	}
}

func TestFormatSizeAndCount(t *testing.T) {
	if got := formatSize(100 << 20); got != "100 MiB" {
		t.Errorf("formatSize = %q", got)
	}
	if got := formatSize(10 << 30); got != "10 GiB" {
		t.Errorf("formatSize = %q", got)
	}
	if got := formatCount(10000); got != "10,000" {
		t.Errorf("formatCount = %q", got)
	}
	if got := formatCount(100); got != "100" {
		t.Errorf("formatCount = %q", got)
	}
}

func TestPricingSignedInLinksToBilling(t *testing.T) {
	svc := appServices()
	body := serve(svc, pageRequest(t, svc, "/pricing", appUser)).Body.String()
	for _, want := range []string{`href="/app/billing" data-plan-cta="pro"`, `href="/app" data-plan-cta="free"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	if strings.Contains(body, "mode=signup") {
		t.Error("signed-in pricing page links to the sign-up")
	}
}

func TestFooterSupportEmail(t *testing.T) {
	svc := testServices()
	app := config.AppConfig{BaseURL: testApp.BaseURL, SupportEmail: "help@example.com"}
	for _, path := range []string{"/", "/pricing", "/app/signin"} {
		rec := httptest.NewRecorder()
		Routes(svc, testFeatures, testStripe, app, testSecurity).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assertStatus(t, rec, http.StatusOK)
		if !strings.Contains(rec.Body.String(), `href="mailto:help@example.com"`) {
			t.Errorf("%s: footer does not link the support email", path)
		}
	}
	// Without SUPPORT_EMAIL the footer has no contact link.
	if body := serve(svc, httptest.NewRequest(http.MethodGet, "/", nil)).Body.String(); strings.Contains(body, "data-support-email") {
		t.Error("footer links a support email although none is configured")
	}
}

func TestSignInPageSignUpMode(t *testing.T) {
	svc := testServices()
	if body := serve(svc, httptest.NewRequest(http.MethodGet, "/app/signin?mode=signup", nil)).Body.String(); !strings.Contains(body, `data-initial-mode="signup"`) {
		t.Error("?mode=signup does not open the sign-up tab")
	}
	if body := serve(svc, httptest.NewRequest(http.MethodGet, "/app/signin?mode=other", nil)).Body.String(); !strings.Contains(body, `data-initial-mode="signin"`) {
		t.Error("an unknown mode does not open the sign-in tab")
	}
}
