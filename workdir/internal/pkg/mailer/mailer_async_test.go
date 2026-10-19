package mailer

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// failingMailer fails the first failCount sends then succeeds, recording
// every attempt it receives.
type failingMailer struct {
	mu        sync.Mutex
	failCount int
	attempts  []Message
}

func (f *failingMailer) Send(ctx context.Context, msg Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts = append(f.attempts, msg)
	if len(f.attempts) <= f.failCount {
		return errors.New("simulated failure")
	}
	return nil
}

func (f *failingMailer) attemptCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.attempts)
}

// blockingMailer blocks each Send until release is closed or ctx is done.
type blockingMailer struct {
	release chan struct{}

	mu   sync.Mutex
	sent []Message
}

func (b *blockingMailer) Send(ctx context.Context, msg Message) error {
	select {
	case <-b.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	b.mu.Lock()
	b.sent = append(b.sent, msg)
	b.mu.Unlock()
	return nil
}

func closeWithin(t *testing.T, a *Async, timeout time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return a.Close(ctx)
}

func TestAsyncSendDeliversInBackground(t *testing.T) {
	log := NewLogMailer(nil)
	a := NewAsync(log, nil, AsyncOptions{RetryDelay: time.Millisecond})

	msg := Message{To: "user@example.com", Subject: "Hi", Text: "hello"}
	if err := a.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if err := closeWithin(t, a, time.Second); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	sent := log.Sent()
	if len(sent) != 1 || sent[0] != msg {
		t.Fatalf("underlying mailer Sent() = %+v, want [%+v]", sent, msg)
	}
}

func TestAsyncSendRetriesUntilSuccess(t *testing.T) {
	fake := &failingMailer{failCount: 2}
	a := NewAsync(fake, nil, AsyncOptions{Retries: 2, RetryDelay: time.Millisecond})

	if err := a.Send(context.Background(), Message{To: "user@example.com", Subject: "Hi", Text: "hello"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if err := closeWithin(t, a, time.Second); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := fake.attemptCount(); got != 3 {
		t.Fatalf("attemptCount() = %d, want 3 (1 initial + 2 retries)", got)
	}
}

func TestAsyncSendGivesUpAfterExhaustingRetries(t *testing.T) {
	fake := &failingMailer{failCount: 100}
	a := NewAsync(fake, nil, AsyncOptions{Retries: 1, RetryDelay: time.Millisecond})

	if err := a.Send(context.Background(), Message{To: "user@example.com", Subject: "Hi", Text: "hello"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if err := closeWithin(t, a, time.Second); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := fake.attemptCount(); got != 2 {
		t.Fatalf("attemptCount() = %d, want 2 (1 initial + 1 retry)", got)
	}
}

func TestAsyncSendDropsWhenQueueIsFull(t *testing.T) {
	// Built directly (not via NewAsync) so no worker drains the queue,
	// making the "full" state deterministic.
	a := &Async{
		next:   NewLogMailer(nil),
		log:    slog.New(slog.NewTextHandler(discard{}, nil)),
		queue:  make(chan Message, 1),
		closed: make(chan struct{}),
	}

	msg := Message{To: "user@example.com", Subject: "Hi", Text: "hello"}
	if err := a.Send(context.Background(), msg); err != nil {
		t.Fatalf("first Send() error = %v, want nil", err)
	}
	if err := a.Send(context.Background(), msg); !errors.Is(err, errQueueFull) {
		t.Fatalf("second Send() error = %v, want errQueueFull", err)
	}
}

func TestAsyncSendAfterCloseIsRejected(t *testing.T) {
	a := NewAsync(NewLogMailer(nil), nil, AsyncOptions{})
	if err := closeWithin(t, a, time.Second); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := a.Send(context.Background(), Message{To: "user@example.com", Subject: "Hi", Text: "hello"}); !errors.Is(err, errAsyncClosed) {
		t.Fatalf("Send() after Close() error = %v, want errAsyncClosed", err)
	}
}

func TestAsyncCloseDrainsQueuedMessages(t *testing.T) {
	log := NewLogMailer(nil)
	a := NewAsync(log, nil, AsyncOptions{QueueSize: 10, Workers: 1})

	for i := 0; i < 5; i++ {
		if err := a.Send(context.Background(), Message{To: "user@example.com", Subject: "Hi", Text: "hello"}); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
	}
	if err := closeWithin(t, a, time.Second); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := len(log.Sent()); got != 5 {
		t.Fatalf("underlying mailer received %d messages, want 5", got)
	}
}

func TestAsyncCloseRespectsContextDeadline(t *testing.T) {
	blocker := &blockingMailer{release: make(chan struct{})}
	a := NewAsync(blocker, nil, AsyncOptions{Timeout: time.Minute})

	if err := a.Send(context.Background(), Message{To: "user@example.com", Subject: "Hi", Text: "hello"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	// Give the worker a moment to pick up the message so it is in flight.
	time.Sleep(10 * time.Millisecond)

	err := closeWithin(t, a, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close() error = %v, want context.DeadlineExceeded", err)
	}
	close(blocker.release)
}
