// Package webhooks holds the form webhook domain: its data types, the
// Repository port used to persist webhooks and a Service implementing it on
// top of sqlc.
package webhooks

import "time"

// Webhook is an endpoint notified of a form's new submissions.
type Webhook struct {
	ID        int32
	FormID    int32
	TargetURL string
	// SecretToken, when set, is used to sign deliveries; never expose it to
	// clients.
	SecretToken *string
	IsActive    bool
	CreatedAt   time.Time
}

// CreateWebhookInput holds the data needed to register a webhook.
type CreateWebhookInput struct {
	FormID      int32
	TargetURL   string
	SecretToken *string
}
