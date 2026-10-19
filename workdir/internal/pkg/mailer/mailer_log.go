package mailer

import (
	"context"
	"log/slog"
	"sync"
)

var _ Mailer = (*LogMailer)(nil)

// LogMailer does not deliver email: it logs each message and keeps it in
// memory so tests can inspect what would have been sent.
type LogMailer struct {
	log *slog.Logger

	mu   sync.Mutex
	sent []Message
}

// NewLogMailer returns a LogMailer that logs through log, or through
// slog.Default when log is nil.
func NewLogMailer(log *slog.Logger) *LogMailer {
	if log == nil {
		log = slog.Default()
	}
	return &LogMailer{log: log}
}

// Send validates, logs and records msg.
func (m *LogMailer) Send(ctx context.Context, msg Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := msg.validate(); err != nil {
		return err
	}
	m.log.InfoContext(ctx, "email (not delivered: log mailer)",
		slog.String("to", msg.To),
		slog.String("subject", msg.Subject),
		slog.String("text", msg.Text),
	)
	m.mu.Lock()
	m.sent = append(m.sent, msg)
	m.mu.Unlock()
	return nil
}

// Sent returns a copy of the messages sent so far, oldest first.
func (m *LogMailer) Sent() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Message, len(m.sent))
	copy(out, m.sent)
	return out
}

// Reset forgets the messages sent so far.
func (m *LogMailer) Reset() {
	m.mu.Lock()
	m.sent = nil
	m.mu.Unlock()
}
