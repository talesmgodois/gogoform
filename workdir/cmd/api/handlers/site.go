package handlers

import (
	"context"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"app/internal/pkg/auth"
	"app/internal/pkg/plans"
	"app/internal/pkg/tenants"
)

// proMonthlyPrice is the price of the pro plan shown on the pricing page; the
// amount charged is the Stripe price configured in stripe.price_id.
const proMonthlyPrice = "9 USD"

// siteController serves the public product pages: the landing page, the
// pricing and the terms and privacy placeholders. They are open to everyone;
// a signed-in viewer is recognized from the session cookie, so the pages link
// to the dashboard instead of the sign-up.
type siteController struct {
	auth *auth.Service
}

// mount registers the product pages on mux.
func (c *siteController) mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", c.page("home", siteLandingTemplate))
	mux.HandleFunc("GET /pricing", c.Pricing)
	mux.HandleFunc("GET /terms", c.page("terms", siteTermsTemplate))
	mux.HandleFunc("GET /privacy", c.page("privacy", sitePrivacyTemplate))
}

// sitePage is the data rendered by the product pages without data of their
// own.
type sitePage struct {
	appLayout
	// ProPrice is the monthly price of the pro plan.
	ProPrice string
}

// page returns the handler rendering t, highlighting the page active.
func (c *siteController) page(active string, t *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		renderAppPage(w, c.withViewer(w, r), t, &sitePage{appLayout: appLayout{Active: active}, ProPrice: proMonthlyPrice})
	}
}

// withViewer returns r carrying the user of its session cookie, if any.
func (c *siteController) withViewer(w http.ResponseWriter, r *http.Request) *http.Request {
	if u := sessionUserOf(w, r, c.auth); u != nil {
		return r.WithContext(withUser(r.Context(), u))
	}
	return r
}

// sitePricingPage is the data rendered by templates/pricing.html.
type sitePricingPage struct {
	appLayout
	// ProPrice is the monthly price of the pro plan.
	ProPrice string
	// Free and Pro list what each plan allows, built from plans.For so the
	// page shows what is enforced.
	Free, Pro []pricingLine
}

// pricingLine is what a plan allows for one limit or feature: a limit is
// rendered as Text, a feature (empty Text) as Included or not.
type pricingLine struct {
	// Key identifies the line in the page (data-pricing attribute).
	Key, Label, Text string
	Included         bool
}

// Pricing renders the comparison of the free and pro plans.
func (c *siteController) Pricing(w http.ResponseWriter, r *http.Request) {
	renderAppPage(w, c.withViewer(w, r), sitePricingTemplate, &sitePricingPage{
		appLayout: appLayout{Active: "pricing"},
		ProPrice:  proMonthlyPrice,
		Free:      pricingLines(plans.For(tenants.PlanFree)),
		Pro:       pricingLines(plans.For(tenants.PlanPro)),
	})
}

// pricingLines lists the limits and features of l.
func pricingLines(l plans.Limits) []pricingLine {
	limit := func(key, label string, n int64, format func(int64) string) pricingLine {
		if n == plans.Unlimited {
			return pricingLine{Key: key, Label: label, Text: "Unlimited", Included: true}
		}
		return pricingLine{Key: key, Label: label, Text: format(n), Included: n > 0}
	}
	feature := func(key, label string, included bool) pricingLine {
		return pricingLine{Key: key, Label: label, Included: included}
	}
	return []pricingLine{
		limit("forms", "forms", l.MaxForms, formatCount),
		limit("submissions", "submissions per month", l.MaxSubmissionsPerMonth, formatCount),
		limit("storage", "file storage", l.MaxStorageBytes, formatSize),
		feature("no-badge", `No "Powered by GoGoForm" badge`, !l.ShowBadge),
		feature("webhooks", "Webhooks", l.Webhooks),
		feature("exports", "CSV and JSON exports", l.Exports),
		feature("email-notifications", "Email per submission", l.EmailNotifications),
	}
}

// formatCount renders n with thousands separators, e.g. "10,000".
func formatCount(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// formatSize renders n like formatBytes without a trailing ".0", e.g.
// "100 MiB".
func formatSize(n int64) string {
	s := formatBytes(n)
	if num, unit, ok := strings.Cut(s, " "); ok {
		return strings.TrimSuffix(num, ".0") + " " + unit
	}
	return s
}

// supportEmailKey is the context key of the address set by withSupportEmail.
type supportEmailKey struct{}

// withSupportEmail stores email in the context of every request to h, for
// the page footers (see appLayout.SupportEmail).
func withSupportEmail(h http.Handler, email string) http.Handler {
	if email == "" {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), supportEmailKey{}, email)))
	})
}

func supportEmailFrom(ctx context.Context) string {
	email, _ := ctx.Value(supportEmailKey{}).(string)
	return email
}
