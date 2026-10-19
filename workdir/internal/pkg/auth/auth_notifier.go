package auth

import (
	"context"

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

// SendEmailVerification sends the link redeeming token at
// GET /app/verify?token=.
func (n *MailNotifier) SendEmailVerification(ctx context.Context, user User, token string) error {
	r, err := mailer.RenderEmailVerification(n.baseURL, token)
	if err != nil {
		return err
	}
	return n.mail.Send(ctx, mailer.Message{To: user.Email, Subject: r.Subject, Text: r.Text, HTML: r.HTML})
}

// SendPasswordReset sends the link redeeming token at
// GET /app/reset-password?token=.
func (n *MailNotifier) SendPasswordReset(ctx context.Context, user User, token string) error {
	r, err := mailer.RenderPasswordReset(n.baseURL, token)
	if err != nil {
		return err
	}
	return n.mail.Send(ctx, mailer.Message{To: user.Email, Subject: r.Subject, Text: r.Text, HTML: r.HTML})
}
