package billing

import (
	"context"

	"app/internal/pkg/auth"
	"app/internal/pkg/mailer"
)

var _ Notifier = (*MailNotifier)(nil)

// MailNotifier implements Notifier by rendering the mailer templates and
// handing the result to a mailer.Mailer, typically a mailer.Async wrapping
// the configured transport, so sending never blocks the caller.
type MailNotifier struct {
	mail    mailer.Mailer
	baseURL string
}

// NewMailNotifier returns a MailNotifier sending through mail; links in the
// emails are built from baseURL (e.g. "https://forms.example.com").
func NewMailNotifier(mail mailer.Mailer, baseURL string) *MailNotifier {
	return &MailNotifier{mail: mail, baseURL: baseURL}
}

// SendPaymentFailed sends the payment-failed email linking to /app/billing.
func (n *MailNotifier) SendPaymentFailed(ctx context.Context, owner auth.User) error {
	r, err := mailer.RenderPaymentFailed(n.baseURL)
	if err != nil {
		return err
	}
	return n.mail.Send(ctx, mailer.Message{To: owner.Email, Subject: r.Subject, Text: r.Text, HTML: r.HTML})
}
