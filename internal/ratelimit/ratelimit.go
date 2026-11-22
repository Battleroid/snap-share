// Package ratelimit implements in-memory token buckets keyed by string
// (usually "ip:<addr>" or "user:<id>").
package ratelimit

import (
	"context"
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
	held   int           // outstanding reservations
	freed  chan struct{} // closed when a reservation resolves; nil until someone waits
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
	b := l.refill(key, now)
	need := float64(n)
	if b.tokens >= need {
		b.tokens -= need
		return true, 0
	}
	wait := time.Duration((need - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}

// refill returns key's bucket brought up to now. Call with l.mu held.
func (l *Limiter) refill(key string, now time.Time) *bucket {
	l.maybeSweep(now)
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	return b
}

// Check reports whether key has a token, without taking it. Check followed
// by Allow can be raced (parallel callers all pass Check); to charge only
// some outcomes of an expensive attempt, use Reserve.
func (l *Limiter) Check(key string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		return true, 0
	}
	tokens := math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	if tokens >= 1 {
		return true, 0
	}
	return false, time.Duration((1 - tokens) / l.rate * float64(time.Second))
}

// Reset refills key's bucket (e.g. after a successful login).
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if b, ok := l.buckets[key]; ok && b.held > 0 {
		b.tokens = l.burst // reservations still out keep the bucket
		return
	}
	delete(l.buckets, key)
}

// Reservation is a token taken by Reserve: Keep it when the attempt counts,
// Refund it when it does not. Only the first call has an effect.
type Reservation struct {
	l    *Limiter
	key  string
	once sync.Once
}

// Keep leaves the token spent.
func (r *Reservation) Keep() { r.resolve(false) }

// Refund gives the token back.
func (r *Reservation) Refund() { r.resolve(true) }

func (r *Reservation) resolve(refund bool) {
	r.once.Do(func() {
		l := r.l
		l.mu.Lock()
		defer l.mu.Unlock()
		b, ok := l.buckets[r.key]
		if !ok {
			return
		}
		b.held--
		if refund {
			b.tokens = math.Min(l.burst, b.tokens+1)
		}
		if b.freed != nil {
			close(b.freed)
			b.freed = nil
		}
	})
}

// Reserve takes one token for key before the work it pays for, so n parallel
// attempts get at most the tokens that are left (Check followed by Allow lets
// them all through). When no token is left but other reservations for key are
// outstanding, which may yet be refunded, it waits up to maxWait for one of
// them to resolve. ok is false when no token could be had; wait is then how
// long until the bucket refills one.
func (l *Limiter) Reserve(ctx context.Context, key string, maxWait time.Duration) (res *Reservation, ok bool, wait time.Duration) {
	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	for {
		l.mu.Lock()
		b := l.refill(key, l.now())
		if b.tokens >= 1 {
			b.tokens--
			b.held++
			l.mu.Unlock()
			return &Reservation{l: l, key: key}, true, 0
		}
		wait = time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
		if b.held == 0 {
			l.mu.Unlock()
			return nil, false, wait
		}
		if b.freed == nil {
			b.freed = make(chan struct{})
		}
		freed := b.freed
		l.mu.Unlock()
		select {
		case <-freed:
		case <-timer.C:
			return nil, false, wait
		case <-ctx.Done():
			return nil, false, wait
		}
	}
}

// maybeSweep drops buckets that have refilled completely, at most once a
// minute. Buckets with reservations outstanding stay.
func (l *Limiter) maybeSweep(now time.Time) {
	if now.Sub(l.sweep) < time.Minute {
		return
	}
	l.sweep = now
	full := time.Duration(l.burst / l.rate * float64(time.Second))
	for k, b := range l.buckets {
		if b.held == 0 && now.Sub(b.last) > full {
			delete(l.buckets, k)
		}
	}
}
