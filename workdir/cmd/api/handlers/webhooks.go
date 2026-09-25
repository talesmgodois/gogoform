package handlers

import (
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	apperrors "app/internal/errors"
	"app/internal/pkg/forms"
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
}

// Create registers a webhook on one of the authenticated tenant's forms.
//
//	@Summary		Create a webhook
//	@Description	Registers an endpoint notified of the form's new submissions. The target must be a public http(s) URL.
//	@Tags			webhooks
//	@Accept			json
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			id		path		int							true	"Form ID"
//	@Param			body	body		CreateWebhookRequest		true	"Webhook to create"
//	@Success		201		{object}	WebhookResponse				"Webhook created"
//	@Failure		400		{object}	errors.HTTPErrorResponse	"Invalid request"
//	@Failure		401		{object}	errors.HTTPErrorResponse	"Missing or invalid API key"
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
	target, err := validateTargetURL(req.TargetURL)
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	if req.SecretToken != nil && (*req.SecretToken == "" || len(*req.SecretToken) > maxSecretTokenLen) {
		apperrors.WriteHTTP(w, r, apperrors.NewBadRequest("secret_token must be between 1 and 100 bytes"))
		return
	}
	// The webhooks repository is not tenant scoped: check ownership first.
	if _, err := c.forms.GetByID(r.Context(), tenantFrom(r.Context()).ID, formID); err != nil {
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
//	@Description	Lists the active webhooks of one of the authenticated tenant's forms, oldest first.
//	@Tags			webhooks
//	@Produce		json
//	@Security		ApiKeyAuth
//	@Param			id	path		int							true	"Form ID"
//	@Success		200	{object}	WebhookListResponse			"Webhooks"
//	@Failure		400	{object}	errors.HTTPErrorResponse	"Invalid form ID"
//	@Failure		401	{object}	errors.HTTPErrorResponse	"Missing or invalid API key"
//	@Failure		404	{object}	errors.HTTPErrorResponse	"Form not found"
//	@Failure		500	{object}	errors.HTTPErrorResponse	"Internal error"
//	@Router			/forms/{id}/webhooks [get]
func (c *webhooksController) List(w http.ResponseWriter, r *http.Request) {
	formID, err := pathID(r, "id")
	if err != nil {
		apperrors.WriteHTTP(w, r, err)
		return
	}
	if _, err := c.forms.GetByID(r.Context(), tenantFrom(r.Context()).ID, formID); err != nil {
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

// validateTargetURL checks that raw is an absolute http(s) URL without
// credentials whose host is not obviously internal, and returns it
// normalized. Host names are not resolved here, so deliveries must still
// check the resolved address before connecting.
func validateTargetURL(raw string) (string, error) {
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
		ip = ip.Unmap()
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
			return "", invalid
		}
	}
	return u.String(), nil
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
