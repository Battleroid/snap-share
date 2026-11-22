// Package ratelimit implements in-memory token buckets keyed by string
// (usually "ip:<addr>" or "user:<id>").
package ratelimit

import (
	"math"
	"sync"
	"time"
)

// Limiter is a set of token buckets sharing one rate and burst.
type Limiter struct {
	rate  float64 // tokens per second
	burst float64
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	sweep   time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New allows burst requests at once and refills n per interval.
func New(n int, per time.Duration, burst int) *Limiter {
	return &Limiter{
		rate:    float64(n) / per.Seconds(),
		burst:   float64(burst),
		now:     time.Now,
		buckets: map[string]*bucket{},
	}
}

// WithClock replaces the clock (tests).
func (l *Limiter) WithClock(now func() time.Time) *Limiter { l.now = now; return l }

// Allow takes one token for key. When none is available it returns false and
// how long until one will be.
func (l *Limiter) Allow(key string) (bool, time.Duration) { return l.AllowN(key, 1) }

// AllowN takes n tokens for key.
func (l *Limiter) AllowN(key string, n int) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.maybeSweep(now)
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	need := float64(n)
	if b.tokens >= need {
		b.tokens -= need
		return true, 0
	}
	wait := time.Duration((need - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}

// Reset forgets key (e.g. after a successful login).
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	delete(l.buckets, key)
	l.mu.Unlock()
}

// maybeSweep drops buckets that have refilled completely, at most once a minute.
func (l *Limiter) maybeSweep(now time.Time) {
	if now.Sub(l.sweep) < time.Minute {
		return
	}
	l.sweep = now
	full := time.Duration(l.burst / l.rate * float64(time.Second))
	for k, b := range l.buckets {
		if now.Sub(b.last) > full {
			delete(l.buckets, k)
		}
	}
}
