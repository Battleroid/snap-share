package ratelimit

import (
	"sync"
	"time"
)

// Window allows at most n events per key in any interval of length per. A
// Limiter with the same n and interval lets up to 2n through in one
// interval (a full burst, then the refill); Window is for limits that must
// hold as stated, such as password guesses.
type Window struct {
	n   int
	per time.Duration

	name    string
	counter Counter

	mu    sync.Mutex
	now   func() time.Time
	keys  map[string][]time.Time // grant times, oldest first, at most n
	sweep time.Time
}

// NewWindow allows n events per key in any interval of length per.
func NewWindow(n int, per time.Duration) *Window {
	return &Window{n: n, per: per, now: time.Now, keys: map[string][]time.Time{}}
}

// Named gives the window a fixed name and counts each denial on c, as
// Limiter.Named does.
func (w *Window) Named(name string, c Counter) *Window {
	w.name, w.counter = name, c
	return w
}

// WithClock replaces the clock. It is safe to call while the window is in
// use.
func (w *Window) WithClock(now func() time.Time) *Window {
	w.mu.Lock()
	w.now = now
	w.mu.Unlock()
	return w
}

// Allow records an event for key if fewer than n happened in the last
// interval. Otherwise it returns false and how long until the oldest of them
// leaves the interval.
func (w *Window) Allow(key string) (bool, time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	w.maybeSweep(now)
	ts := w.recent(key, now)
	if len(ts) >= w.n {
		w.denied()
		return false, ts[0].Add(w.per).Sub(now)
	}
	w.keys[key] = append(ts, now)
	return true, 0
}

// Check reports whether Allow would succeed, without recording an event.
func (w *Window) Check(key string) (bool, time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	ts := w.recent(key, now)
	if len(ts) >= w.n {
		w.denied()
		return false, ts[0].Add(w.per).Sub(now)
	}
	return true, 0
}

// recent drops key's events that are an interval or more old and returns the
// rest. Call with w.mu held.
func (w *Window) recent(key string, now time.Time) []time.Time {
	ts, ok := w.keys[key]
	if !ok {
		return nil
	}
	i := 0
	for i < len(ts) && now.Sub(ts[i]) >= w.per {
		i++
	}
	if i == len(ts) {
		delete(w.keys, key)
		return nil
	}
	if i > 0 {
		ts = append(ts[:0], ts[i:]...)
		w.keys[key] = ts
	}
	return ts
}

func (w *Window) denied() {
	if w.counter != nil {
		w.counter.RateLimited(w.name)
	}
}

// maybeSweep drops keys with no event in the last interval, at most once a
// minute. Call with w.mu held.
func (w *Window) maybeSweep(now time.Time) {
	if now.Sub(w.sweep) < time.Minute {
		return
	}
	w.sweep = now
	for k, ts := range w.keys {
		if now.Sub(ts[len(ts)-1]) >= w.per {
			delete(w.keys, k)
		}
	}
}
