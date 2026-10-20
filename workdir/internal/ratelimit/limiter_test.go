package ratelimit

import (
	"testing"
	"time"
)

// fakeClock lets tests advance time deterministically.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newFakeClock() *fakeClock { return &fakeClock{t: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)} }

func TestLimiterAllowsBurstThenDenies(t *testing.T) {
	clock := newFakeClock()
	l := New(60, 3, WithClock(clock.now)) // 1/sec refill, burst 3

	for i := 0; i < 3; i++ {
		if allowed, _ := l.Allow("a"); !allowed {
			t.Fatalf("request %d: expected allowed", i)
		}
	}
	allowed, retryAfter := l.Allow("a")
	if allowed {
		t.Fatal("4th request within the burst should be denied")
	}
	if retryAfter < time.Second {
		t.Fatalf("retryAfter = %v, want >= 1s", retryAfter)
	}
}

func TestLimiterRefillsOverTime(t *testing.T) {
	clock := newFakeClock()
	l := New(60, 1, WithClock(clock.now)) // 1/sec refill, burst 1

	if allowed, _ := l.Allow("a"); !allowed {
		t.Fatal("first request should be allowed")
	}
	if allowed, _ := l.Allow("a"); allowed {
		t.Fatal("second immediate request should be denied")
	}

	clock.advance(time.Second)
	if allowed, _ := l.Allow("a"); !allowed {
		t.Fatal("request after the refill interval should be allowed")
	}
}

func TestLimiterKeysAreIndependent(t *testing.T) {
	clock := newFakeClock()
	l := New(60, 1, WithClock(clock.now))

	if allowed, _ := l.Allow("a"); !allowed {
		t.Fatal("key a should be allowed")
	}
	if allowed, _ := l.Allow("a"); allowed {
		t.Fatal("key a should now be denied")
	}
	if allowed, _ := l.Allow("b"); !allowed {
		t.Fatal("key b has its own bucket and should be allowed")
	}
}

func TestLimiterRetryAfterIsRoundedUpToWholeSeconds(t *testing.T) {
	clock := newFakeClock()
	l := New(30, 1, WithClock(clock.now)) // 0.5/sec refill

	l.Allow("a")
	_, retryAfter := l.Allow("a")
	if retryAfter != 2*time.Second {
		t.Fatalf("retryAfter = %v, want 2s", retryAfter)
	}
}

func TestLimiterEvictsIdleBuckets(t *testing.T) {
	clock := newFakeClock()
	l := New(60, 1, WithClock(clock.now), WithIdleTTL(time.Minute))

	l.Allow("a")
	if l.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", l.Len())
	}

	// Advance past the idle TTL and the sweep interval (equal to it), then
	// touch a different key: the sweep runs on every Allow call once
	// sweepEvery has elapsed, evicting "a".
	clock.advance(2 * time.Minute)
	l.Allow("b")
	if l.Len() != 1 {
		t.Fatalf("Len() = %d after eviction, want 1 (only %q)", l.Len(), "b")
	}
	// A fresh bucket for "a" allows immediately (full burst), proving the old
	// one was evicted rather than merely refilled.
	if allowed, _ := l.Allow("a"); !allowed {
		t.Fatal("expected a fresh bucket for \"a\" to allow immediately")
	}
}
