// Package ratelimit implements a simple in-memory token-bucket rate
// limiter keyed by an arbitrary string, typically a client IP. It has no
// external dependencies and keeps no state beyond the process, so every
// replica of the application enforces its own limit; that is an accepted
// trade-off for the abuse protection it exists for (see
// __ai_work/tasks/000027_security_hardening.md).
package ratelimit

import (
	"math"
	"sync"
	"time"
)

// defaultIdleTTL is how long an unused bucket is kept before eviction: ample
// for the refill rates this package is configured with in practice, while
// bounding memory use under a sustained flood of distinct client IPs.
const defaultIdleTTL = 10 * time.Minute

// bucket is the token-bucket state of a single key.
type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter enforces a token-bucket rate limit per key. Each key gets its own
// bucket of size burst that refills at ratePerMinute tokens per minute.
// A Limiter is safe for concurrent use.
type Limiter struct {
	ratePerSecond float64
	burst         float64

	now        func() time.Time
	idleTTL    time.Duration
	sweepEvery time.Duration

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

// Option configures a Limiter built by New.
type Option func(*Limiter)

// WithClock overrides the clock used to advance and evict buckets; tests use
// it for deterministic refill and eviction.
func WithClock(now func() time.Time) Option {
	return func(l *Limiter) { l.now = now }
}

// WithIdleTTL overrides how long an idle bucket is kept before eviction.
func WithIdleTTL(d time.Duration) Option {
	return func(l *Limiter) { l.idleTTL, l.sweepEvery = d, d }
}

// New returns a Limiter allowing ratePerMinute requests per minute per key,
// bursting up to burst requests at once. Both must be >= 1.
func New(ratePerMinute, burst int, opts ...Option) *Limiter {
	l := &Limiter{
		ratePerSecond: float64(ratePerMinute) / 60,
		burst:         float64(burst),
		now:           time.Now,
		idleTTL:       defaultIdleTTL,
		sweepEvery:    defaultIdleTTL,
		buckets:       make(map[string]*bucket),
	}
	for _, opt := range opts {
		opt(l)
	}
	l.lastSweep = l.now()
	return l
}

// Allow reports whether the request identified by key may proceed, consuming
// one token if so. When it may not, retryAfter is how long the caller should
// wait before a token is next available, rounded up to the second so it can
// be used directly as a Retry-After header value.
func (l *Limiter) Allow(key string) (allowed bool, retryAfter time.Duration) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.evictIdleLocked(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	} else if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(l.burst, b.tokens+elapsed*l.ratePerSecond)
		b.last = now
	}

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, ceilSeconds(time.Duration((1 - b.tokens) / l.ratePerSecond * float64(time.Second)))
}

// evictIdleLocked drops buckets untouched for longer than idleTTL. Called
// with mu held; runs at most every sweepEvery so the common case stays O(1).
func (l *Limiter) evictIdleLocked(now time.Time) {
	if now.Sub(l.lastSweep) < l.sweepEvery {
		return
	}
	l.lastSweep = now
	for k, b := range l.buckets {
		if now.Sub(b.last) > l.idleTTL {
			delete(l.buckets, k)
		}
	}
}

// Len returns the number of keys currently tracked, for tests asserting on
// idle eviction.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// ceilSeconds rounds d up to a whole, non-zero number of seconds.
func ceilSeconds(d time.Duration) time.Duration {
	secs := math.Ceil(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	return time.Duration(secs) * time.Second
}
