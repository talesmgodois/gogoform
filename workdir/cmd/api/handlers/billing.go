package handlers

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/stripe/stripe-go/v82/webhook"

	"app/internal/config"
	apperrors "app/internal/errors"
	"app/internal/pkg/billing"
)

// maxStripeEventBytes caps the size of a Stripe webhook body.
const maxStripeEventBytes = 64 << 10

// stripeSignatureHeader carries the signature of a Stripe webhook body.
const stripeSignatureHeader = "Stripe-Signature"

// billingPagePath is the /app page Stripe sends the user back to.
const billingPagePath = "/app/billing"

// billingController serves the Stripe billing endpoints. It is only mounted
// when Stripe is configured.
type billingController struct {
	billing *billing.Service
	stripe  config.StripeConfig
	// app authenticates the session cookie of the checkout and portal
	// forms and lists the navigation tabs of their error pages.
	app *appController
}

// mount registers the billing routes on mux.
func (c *billingController) mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /webhooks/stripe", c.Webhook)
	mux.HandleFunc("POST /billing/checkout", c.withWorkspace(c.Checkout))
	mux.HandleFunc("POST /billing/portal", c.withWorkspace(c.Portal))
}

// Checkout sends the signed-in user to a Stripe Checkout Session subscribing
// their workspace to the pro plan.
//
//	@Summary		Start a pro plan checkout
//	@Description	Submitted by the /app/billing page with the session cookie. Creates the workspace's Stripe customer if needed and redirects to a subscription Checkout Session, which returns to /app/billing?checkout=success or ?checkout=cancel. Anonymous users are redirected to the sign-in page; errors render an HTML message page. Only available when Stripe is configured.
//	@Tags			billing
//	@Produce		html
//	@Success		303	"Redirect to Stripe Checkout, or to /app/signin when signed out"
//	@Failure		403	"Cross-origin request or no workspace"
//	@Failure		404	"Stripe is not configured"
//	@Failure		409	"Workspace already on the pro plan or with a subscription that has not ended"
//	@Failure		500	"Stripe request failed"
//	@Router			/billing/checkout [post]
func (c *billingController) Checkout(w http.ResponseWriter, r *http.Request) {
	urls := billing.CheckoutURLs{
		SuccessURL: c.billingURL("checkout=success"),
		CancelURL:  c.billingURL("checkout=cancel"),
	}
	dest, err := c.billing.StartCheckout(r.Context(), tenantFrom(r.Context()), *userFrom(r.Context()), urls)
	if err != nil {
		renderBillingError(w, r, err)
		return
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// Portal sends the signed-in user to the Stripe Customer Portal of their
// workspace.
//
//	@Summary		Open the Stripe Customer Portal
//	@Description	Submitted by the /app/billing page with the session cookie. Redirects to a Customer Portal session of the workspace's Stripe customer, which links back to /app/billing. Anonymous users are redirected to the sign-in page; errors render an HTML message page. Only available when Stripe is configured.
//	@Tags			billing
//	@Produce		html
//	@Success		303	"Redirect to the Stripe Customer Portal, or to /app/signin when signed out"
//	@Failure		403	"Cross-origin request or no workspace"
//	@Failure		404	"Stripe is not configured, or the workspace has no billing account"
//	@Failure		500	"Stripe request failed"
//	@Router			/billing/portal [post]
func (c *billingController) Portal(w http.ResponseWriter, r *http.Request) {
	dest, err := c.billing.OpenPortal(r.Context(), tenantFrom(r.Context()), c.billingURL(""))
	if err != nil {
		renderBillingError(w, r, err)
		return
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// withWorkspace authenticates next with the /app session cookie: anonymous
// users are sent to the sign-in page, cross-origin requests and users
// without a workspace get a 403 page. The user and their tenant are then
// available to next through userFrom and tenantFrom.
func (c *billingController) withWorkspace(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := c.app.sessionUser(w, r)
		if u == nil {
			http.Redirect(w, r, "/app/signin?next="+url.QueryEscape(billingPagePath), http.StatusSeeOther)
			return
		}
		ctx := withAppView(withUser(r.Context(), u), c.app.tabsFor(u))
		r = r.WithContext(ctx)
		if !sameOrigin(r) {
			renderAppMessage(w, r, http.StatusForbidden, "Access denied", "This request did not come from this site.")
			return
		}
		t, err := c.app.tenants.GetByOwner(ctx, u.ID)
		if isNotFound(err) {
			renderAppMessage(w, r, http.StatusForbidden, "No workspace", "Your account has no workspace to bill.")
			return
		}
		if err != nil {
			renderBillingError(w, r, err)
			return
		}
		next(w, r.WithContext(withTenant(ctx, t)))
	}
}

// billingURL returns the absolute URL of the billing page with query.
func (c *billingController) billingURL(query string) string {
	u := strings.TrimRight(c.stripe.AppBaseURL, "/") + billingPagePath
	if query != "" {
		u += "?" + query
	}
	return u
}

// renderBillingError renders err as a message page linking back to the
// billing page; 5xx errors are logged and shown without their details.
func renderBillingError(w http.ResponseWriter, r *http.Request, err error) {
	status := apperrors.HTTPStatus(err)
	page := appMessagePage{Href: billingPagePath, Link: "Back to billing"}
	if status >= http.StatusInternalServerError {
		slog.ErrorContext(r.Context(), "billing request failed", "status", status, "error", err)
		page.Title, page.Message = "Something went wrong", "We could not reach the billing provider. Try again in a moment."
	} else {
		page.Title, page.Message = http.StatusText(status), "This request could not be completed."
		if appErr, ok := apperrors.As(err); ok {
			page.Message = appErr.Message
		}
	}
	renderAppPageStatus(w, r, status, appMessageTemplate, &page)
}

// Webhook applies a Stripe webhook event to the tenant it is about.
//
//	@Summary		Receive a Stripe webhook
//	@Description	Endpoint called by Stripe. The body must be signed with the configured webhook secret (Stripe-Signature header). Events already processed are acknowledged without being applied again. Only available when Stripe is configured.
//	@Tags			billing
//	@Accept			json
//	@Produce		json
//	@Param			Stripe-Signature	header	string	true	"Signature of the body computed by Stripe"
//	@Param			body				body	object	true	"Stripe event"
//	@Success		200	"Event processed or ignored"
//	@Failure		400	{object}	errors.HTTPErrorResponse	"Body too large, bad signature or malformed event"
//	@Failure		404	{object}	errors.HTTPErrorResponse	"Stripe is not configured"
//	@Failure		500	{object}	errors.HTTPErrorResponse	"Processing failed; Stripe retries the event"
//	@Router			/webhooks/stripe [post]
func (c *billingController) Webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxStripeEventBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("request body too large"))
			return
		}
		apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("could not read request body"))
		return
	}
	// Events of another API version than stripe-go's are accepted: only
	// fields stable across versions are read, and rejecting them would make
	// Stripe retry every event until it disables the endpoint.
	event, err := webhook.ConstructEventWithOptions(body, r.Header.Get(stripeSignatureHeader), c.stripe.WebhookSecret,
		webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
	if err != nil {
		apperrors.WriteHTTP(w, r, apperrors.Wrap(err, apperrors.CodeInvalidArgument, "invalid stripe signature or event"))
		return
	}
	// A malformed event gets a 400; any other failure a 500 so Stripe retries.
	if err := c.billing.HandleEvent(r.Context(), event); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
