package mailer

import (
	"context"
	"log/slog"
	"sync"
	"testing"
)

func TestLogMailerSendRecordsAndLogs(t *testing.T) {
	log := slog.New(slog.NewTextHandler(discard{}, nil))
	m := NewLogMailer(log)

	msg := Message{To: "user@example.com", Subject: "Hi", Text: "hello"}
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	sent := m.Sent()
	if len(sent) != 1 || sent[0] != msg {
		t.Fatalf("Sent() = %+v, want [%+v]", sent, msg)
	}
}

func TestLogMailerSendRejectsInvalidMessage(t *testing.T) {
	m := NewLogMailer(nil)
	if err := m.Send(context.Background(), Message{To: "not-an-email", Subject: "Hi", Text: "hello"}); err == nil {
		t.Fatal("Send() error = nil, want error for invalid recipient")
	}
	if len(m.Sent()) != 0 {
		t.Fatalf("Sent() = %v, want empty after a rejected message", m.Sent())
	}
}

func TestLogMailerSendRejectsCancelledContext(t *testing.T) {
	m := NewLogMailer(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Send(ctx, Message{To: "user@example.com", Subject: "Hi", Text: "hello"}); err == nil {
		t.Fatal("Send() error = nil, want error for cancelled context")
	}
}

func TestLogMailerReset(t *testing.T) {
	m := NewLogMailer(nil)
	if err := m.Send(context.Background(), Message{To: "user@example.com", Subject: "Hi", Text: "hello"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	m.Reset()
	if sent := m.Sent(); len(sent) != 0 {
		t.Fatalf("Sent() after Reset() = %v, want empty", sent)
	}
}

func TestLogMailerSentIsACopy(t *testing.T) {
	m := NewLogMailer(nil)
	if err := m.Send(context.Background(), Message{To: "user@example.com", Subject: "Hi", Text: "hello"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	sent := m.Sent()
	sent[0].Subject = "mutated"
	if got := m.Sent()[0].Subject; got != "Hi" {
		t.Fatalf("Sent() after external mutation = %q, want %q", got, "Hi")
	}
}

func TestLogMailerSendIsConcurrencySafe(t *testing.T) {
	m := NewLogMailer(nil)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = m.Send(context.Background(), Message{To: "user@example.com", Subject: "Hi", Text: "hello"})
		}()
	}
	wg.Wait()
	if len(m.Sent()) != 50 {
		t.Fatalf("Sent() has %d messages, want 50", len(m.Sent()))
	}
}

// discard is an io.Writer that drops everything written to it.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
