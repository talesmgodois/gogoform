package mailer

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultQueueSize   = 100
	defaultWorkers     = 1
	defaultAsyncTimeout = 10 * time.Second
	defaultRetries     = 1
	defaultRetryDelay  = 2 * time.Second
)

var errQueueFull = errors.New("mailer: queue full, message dropped")
var errAsyncClosed = errors.New("mailer: async mailer is closed")

var _ Mailer = (*Async)(nil)

// Async wraps a Mailer so Send enqueues the message and returns immediately.
// A pool of workers delivers messages in the background using their own
// context, since the caller's context (typically an HTTP request) is often
// gone before delivery completes.
type Async struct {
	next Mailer
	log  *slog.Logger

	timeout    time.Duration
	retries    int
	retryDelay time.Duration

	queue chan Message
	wg    sync.WaitGroup

	closeOnce sync.Once
	closed    chan struct{}
}

// AsyncOptions configures Async. Zero values fall back to sane defaults.
type AsyncOptions struct {
	// QueueSize bounds how many messages can wait for a worker; default 100.
	QueueSize int
	// Workers is how many goroutines deliver messages concurrently; default 1.
	Workers int
	// Timeout bounds a single delivery attempt; default 10s.
	Timeout time.Duration
	// Retries is how many additional attempts follow a failed delivery;
	// default 1.
	Retries int
	// RetryDelay is the pause before a retry; default 2s.
	RetryDelay time.Duration
}

// NewAsync wraps next and starts its worker goroutines. Call Close to stop
// accepting new messages, drain the queue and wait for the workers to stop.
func NewAsync(next Mailer, log *slog.Logger, opts AsyncOptions) *Async {
	if log == nil {
		log = slog.Default()
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = defaultQueueSize
	}
	if opts.Workers <= 0 {
		opts.Workers = defaultWorkers
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultAsyncTimeout
	}
	if opts.Retries < 0 {
		opts.Retries = defaultRetries
	}
	if opts.RetryDelay <= 0 {
		opts.RetryDelay = defaultRetryDelay
	}

	a := &Async{
		next:       next,
		log:        log,
		timeout:    opts.Timeout,
		retries:    opts.Retries,
		retryDelay: opts.RetryDelay,
		queue:      make(chan Message, opts.QueueSize),
		closed:     make(chan struct{}),
	}
	for i := 0; i < opts.Workers; i++ {
		a.wg.Add(1)
		go a.worker()
	}
	return a
}

// Send enqueues msg for background delivery and returns immediately. It
// never blocks the caller: once closed, or once the queue is full, the
// message is dropped, logged and reported through the returned error.
func (a *Async) Send(ctx context.Context, msg Message) error {
	select {
	case <-a.closed:
		return errAsyncClosed
	default:
	}
	select {
	case a.queue <- msg:
		return nil
	default:
		a.log.ErrorContext(ctx, "mailer: queue full, dropping message",
			slog.String("to", msg.To), slog.String("subject", msg.Subject))
		return errQueueFull
	}
}

func (a *Async) worker() {
	defer a.wg.Done()
	for {
		select {
		case msg := <-a.queue:
			a.deliver(msg)
		case <-a.closed:
			a.drain()
			return
		}
	}
}

// drain delivers whatever is already buffered in the queue without blocking
// for more; it runs once a worker observes that Close was called.
func (a *Async) drain() {
	for {
		select {
		case msg := <-a.queue:
			a.deliver(msg)
		default:
			return
		}
	}
}

func (a *Async) deliver(msg Message) {
	var err error
	for attempt := 0; attempt <= a.retries; attempt++ {
		if attempt > 0 {
			time.Sleep(a.retryDelay)
		}
		ctx, cancel := context.WithTimeout(context.Background(), a.timeout)
		err = a.next.Send(ctx, msg)
		cancel()
		if err == nil {
			return
		}
		a.log.Error("mailer: delivery attempt failed",
			slog.String("to", msg.To), slog.String("subject", msg.Subject),
			slog.Int("attempt", attempt+1), slog.Any("error", err))
	}
	a.log.Error("mailer: delivery failed, giving up",
		slog.String("to", msg.To), slog.String("subject", msg.Subject), slog.Any("error", err))
}

// Close stops accepting new messages, lets the workers drain whatever is
// already queued, and waits for them to stop or for ctx to be done.
func (a *Async) Close(ctx context.Context) error {
	a.closeOnce.Do(func() { close(a.closed) })
	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
