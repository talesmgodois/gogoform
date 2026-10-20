package handlers

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/forms"
	"app/internal/pkg/plans"
	"app/internal/pkg/tenants"
	"app/internal/pkg/webhooks"
)

// maxSecretTokenLen matches the form_webhooks.secret_token column size.
const maxSecretTokenLen = 100

// CreateWebhookRequest is the body of POST /forms/{id}/webhooks.
type CreateWebhookRequest struct {
	// TargetURL must be a public http(s) URL.
	TargetURL string `json:"target_url" example:"https://example.com/hooks/forms"`
	// SecretToken, when set, is used to sign deliveries. It is never returned.
	SecretToken *string `json:"secret_token" example:"s3cr3t"`
}

// WebhookResponse is a webhook as returned by the API, without its secret.
type WebhookResponse struct {
	ID        int32     `json:"id" example:"1"`
	FormID    int32     `json:"form_id" example:"1"`
	TargetURL string    `json:"target_url" example:"https://example.com/hooks/forms"`
	HasSecret bool      `json:"has_secret" example:"true"`
	IsActive  bool      `json:"is_active" example:"true"`
	CreatedAt time.Time `json:"created_at" example:"2026-01-01T00:00:00Z"`
}

// WebhookListResponse is the list of a form's webhooks.
type WebhookListResponse struct {
	Items []WebhookResponse `json:"items"`
}

// webhooksController serves the webhook endpoints.
type webhooksController struct {
	forms    forms.Repository
	webhooks webhooks.Repository
	// resolver resolves the target hosts of new webhooks; see validateTargetURL.
	resolver HostResolver
}

// HostResolver resolves a host name to its IP addresses; *net.Resolver
// implements it.
type HostResolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Create registers a webhook on one of the authenticated tenant's forms.
//
//	@Summary		Create a webhook
//	@Description	Registers an endpoint notified of the form's new submissions. The target must be a public http(s) URL: a host name must resolve, and only to public addresses (no loopback, private, link-local, carrier-grade NAT or other internal range). Fails with plan_feature_unavailable when the tenant's plan does not include webhooks.
//	@Tags			webhooks
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			id		path		int							true	"Form ID"
//	@Param			body	body		CreateWebhookRequest		true	"Webhook to create"
//	@Success		201		{object}	WebhookResponse				"Webhook created"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid request"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Missing or invalid API key or user credentials"
//	@Failure		403		{object}	errors.HTTPErrorResponse	"Plan without webhooks, signed-in user has no workspace, or cross-origin request authenticated by the session cookie"
//	@Failure		404		{object}	errors.HTTPErrorResponse	"Form not found"
//	@Failure		500		{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/forms/{id}/webhooks [post]
func (c *webhooksController) Create(w http.ResponseWriter, r *http.Request) {
	formID, err := pathID(r, "id")
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	var req CreateWebhookRequest
	if err := decodeJSON(w, r, &req); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	target, err := validateTargetURL(r.Context(), c.resolver, req.TargetURL)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	if req.SecretToken != nil && (*req.SecretToken == "" || len(*req.SecretToken) > maxSecretTokenLen) {
		apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("secret_token must be between 1 and 100 bytes"))
		return
	}
	// The webhooks repository is not tenant scoped: check ownership first.
	// The plan is checked after it so other tenants' forms stay 404.
	tenant := tenantFrom(r.Context())
	if _, err := c.forms.GetByID(r.Context(), tenant.ID, formID); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	if err := checkWebhooksAllowed(tenant); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	hook, err := c.webhooks.Create(r.Context(), webhooks.CreateWebhookInput{
		FormID:      formID,
		TargetURL:   target,
		SecretToken: req.SecretToken,
	})
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	writeJSON(w, r, http.StatusCreated, toWebhookResponse(hook))
}

// List lists the active webhooks of one of the authenticated tenant's forms.
//
//	@Summary		List webhooks
//	@Description	Lists the active webhooks of one of the authenticated tenant's forms, oldest first. Fails with plan_feature_unavailable when the tenant's plan does not include webhooks.
//	@Tags			webhooks
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Security		BearerAuth
//	@Security		BasicAuth
//	@Param			id	path		int							true	"Form ID"
//	@Success		200	{object}	WebhookListResponse			"Webhooks"
//	@Failure		400	{object}	errors.HTTPErrorResponse	"Invalid form ID"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Missing or invalid API key or user credentials"
//	@Failure		403	{object}	errors.HTTPErrorResponse	"Plan without webhooks, or signed-in user has no workspace"
//	@Failure		404	{object}	errors.HTTPErrorResponse	"Form not found"
//	@Failure		500	{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/forms/{id}/webhooks [get]
func (c *webhooksController) List(w http.ResponseWriter, r *http.Request) {
	formID, err := pathID(r, "id")
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	tenant := tenantFrom(r.Context())
	if _, err := c.forms.GetByID(r.Context(), tenant.ID, formID); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	if err := checkWebhooksAllowed(tenant); err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	hooks, err := c.webhooks.ListActiveByForm(r.Context(), formID)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	resp := WebhookListResponse{Items: make([]WebhookResponse, len(hooks))}
	for i, h := range hooks {
		resp.Items[i] = toWebhookResponse(h)
	}
	writeJSON(w, r, http.StatusOK, resp)
}

// checkWebhooksAllowed returns a plan_feature_unavailable error when the
// tenant's plan does not include webhooks.
func checkWebhooksAllowed(t tenants.Tenant) error {
	if !plans.For(t.Plan).Webhooks {
		return apperrors.NewPlanFeatureUnavailable("Webhooks are available on the Pro plan. Upgrade to Pro to use them.")
	}
	return nil
}

// validateTargetURL checks that raw is an absolute http(s) URL without
// credentials whose host is public, and returns it normalized. A host name is
// resolved with resolver and refused when it does not resolve or any of its
// addresses is internal (see isPublicAddr), so a webhook cannot be aimed at
// the server's own network (SSRF). DNS can change after the webhook is saved,
// so deliveries must still check the resolved address before connecting.
func validateTargetURL(ctx context.Context, resolver HostResolver, raw string) (string, error) {
	invalid := apperrors.NewBadRequest("target_url must be a public http or https URL")
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", invalid
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return "", invalid
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !isPublicAddr(ip) {
			return "", invalid
		}
		return u.String(), nil
	}
	addrs, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return "", apperrors.NewBadRequest("target_url host does not resolve")
	}
	for _, ip := range addrs {
		if !isPublicAddr(ip) {
			return "", invalid
		}
	}
	return u.String(), nil
}

// nonPublicPrefixes are the special-purpose ranges the netip predicates of
// isPublicAddr do not cover.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),     // "this network"
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),   // reserved, broadcast
	netip.MustParsePrefix("64:ff9b::/96"),  // NAT64, may embed an internal IPv4
}

// isPublicAddr reports whether ip, IPv4-mapped IPv6 included, is a public
// unicast address: not loopback, private, link-local, unspecified,
// multicast, carrier-grade NAT or another nonPublicPrefixes range.
func isPublicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, p := range nonPublicPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

func toWebhookResponse(h webhooks.Webhook) WebhookResponse {
	return WebhookResponse{
		ID:        h.ID,
		FormID:    h.FormID,
		TargetURL: h.TargetURL,
		HasSecret: h.SecretToken != nil,
		IsActive:  h.IsActive,
		CreatedAt: h.CreatedAt,
	}
}
