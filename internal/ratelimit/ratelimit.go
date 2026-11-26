// Package ratelimit throttles failed authentication attempts per client.
//
// Only failures are recorded: callers consult the limiter after a request has
// failed authentication, so requests with a valid token never touch it (no
// lock on the success path) and are never rejected because their source IP
// is being throttled.
package ratelimit

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// Limiter counts failed attempts per key in fixed windows. Within a window
// the first max failures are allowed; further failures are limited until the
// window, which starts at the key's first failure, ends.
type Limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu        sync.Mutex
	buckets   map[string]*bucket
	nextSweep time.Time
}

type bucket struct {
	failures int
	reset    time.Time
}

// New returns a Limiter allowing max failed attempts per key per window.
func New(max int, window time.Duration) (*Limiter, error) {
	return newWithClock(max, window, time.Now)
}

func newWithClock(max int, window time.Duration, now func() time.Time) (*Limiter, error) {
	if max <= 0 {
		return nil, errors.New("max failed attempts must be positive")
	}
	if window <= 0 {
		return nil, errors.New("window must be positive")
	}
	return &Limiter{
		max:     max,
		window:  window,
		now:     now,
		buckets: map[string]*bucket{},
	}, nil
}

// Max returns the number of failed attempts allowed per window.
func (l *Limiter) Max() int { return l.max }

// Window returns the window length.
func (l *Limiter) Window() time.Duration { return l.window }

// Fail records a failed attempt for key. It reports whether the attempt is
// over the limit and, if so, how long until the key's window resets.
func (l *Limiter) Fail(key string) (limited bool, retryAfter time.Duration) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{}
		// key may alias a request buffer; copy before keeping it.
		l.buckets[strings.Clone(key)] = b
	}
	if !now.Before(b.reset) {
		b.failures = 0
		b.reset = now.Add(l.window)
	}
	// Capped at max+1: enough to know the key is limited, no overflow.
	if b.failures <= l.max {
		b.failures++
	}
	if b.failures > l.max {
		return true, b.reset.Sub(now)
	}
	return false, 0
}

// Len returns the number of keys currently tracked.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// sweep drops expired buckets, at most once per window, so memory is bounded
// by the number of distinct failing keys seen in roughly two windows.
// Callers hold l.mu.
func (l *Limiter) sweep(now time.Time) {
	if now.Before(l.nextSweep) {
		return
	}
	for key, b := range l.buckets {
		if !now.Before(b.reset) {
			delete(l.buckets, key)
		}
	}
	l.nextSweep = now.Add(l.window)
}
