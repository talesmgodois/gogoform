// Package mailer sends transactional email. It provides an SMTP
// implementation for production (works with any SMTP relay such as Resend,
// Postmark or SES) and a log-only implementation for development and tests.
package mailer

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

// Message is a single email to one recipient. Text is required; HTML is an
// optional alternative rendering of the same content.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Mailer sends email messages.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// validate checks the fields every implementation relies on and rejects
// values that could inject headers.
func (m Message) validate() error {
	if strings.TrimSpace(m.To) == "" {
		return errors.New("mailer: recipient is required")
	}
	if _, err := mail.ParseAddress(m.To); err != nil {
		return fmt.Errorf("mailer: invalid recipient %q: %w", m.To, err)
	}
	if strings.TrimSpace(m.Subject) == "" {
		return errors.New("mailer: subject is required")
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return errors.New("mailer: subject must be a single line")
	}
	if m.Text == "" {
		return errors.New("mailer: text body is required")
	}
	return nil
}
