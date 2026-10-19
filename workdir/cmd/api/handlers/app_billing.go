package handlers

import (
	"net/http"

	"github.com/stripe/stripe-go/v82"

	"app/internal/pkg/billing"
	"app/internal/pkg/tenants"
)

// billingFormAction lets the billing page submit its forms to /billing,
// which redirects to Stripe Checkout or the Customer Portal; browsers check
// form-action against the redirect targets too.
const billingFormAction = "'self' https://checkout.stripe.com https://billing.stripe.com"

// appBillingPage is the data rendered by templates/billing.html.
type appBillingPage struct {
	appLayout
	Tenant tenants.Tenant
	// Enabled tells whether Stripe is configured; the Upgrade and Manage
	// buttons are hidden otherwise.
	Enabled bool
	// Checkout is the outcome Stripe Checkout returned with: "success",
	// "cancel" or empty.
	Checkout string
}

// Pro reports whether the workspace is on the pro plan.
func (p *appBillingPage) Pro() bool { return p.Tenant.Plan == tenants.PlanPro }

// CanUpgrade reports whether the page offers a checkout to the pro plan:
// not while a subscription that has not ended exists, which is managed from
// the portal instead.
func (p *appBillingPage) CanUpgrade() bool {
	t := p.Tenant
	return p.Enabled && !p.Pro() &&
		(t.StripeSubscriptionID == "" || billing.IsEndedStatus(stripe.SubscriptionStatus(t.SubscriptionStatus)))
}

// CanManage reports whether the page offers the Stripe Customer Portal.
func (p *appBillingPage) CanManage() bool { return p.Enabled && p.Tenant.StripeCustomerID != "" }

// Billing renders the plan of the viewer's workspace with the buttons to
// upgrade to pro or manage the subscription, and the outcome of a checkout
// Stripe sent the user back from (?checkout=success|cancel).
func (c *appController) Billing(w http.ResponseWriter, r *http.Request) {
	page := appBillingPage{
		appLayout: appLayout{Active: "billing", formAction: billingFormAction},
		Tenant:    tenantFrom(r.Context()),
		Enabled:   c.billingEnabled,
	}
	switch v := r.URL.Query().Get("checkout"); v {
	case "success", "cancel":
		page.Checkout = v
	}
	renderAppPage(w, r, appBillingTemplate, &page)
}
