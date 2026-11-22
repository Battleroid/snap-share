package stats

import (
	"container/list"
	"sync"
	"time"
)

// Dedupe defaults.
const (
	DefaultDedupeWindow  = 30 * time.Minute
	DefaultDedupeEntries = 50_000
)

// Deduper suppresses repeats of (subject, visitor key, kind) within a
// window of inactivity, like a session: each repeat restarts the window.
// It merges the Range requests of one playback into a single play and
// keeps reloads from counting as new views.
//
// It holds at most a fixed number of keys, evicting the least recently
// seen; an evicted key counts again on its next event. Safe for
// concurrent use.
type Deduper struct {
	window time.Duration
	max    int
	now    func() time.Time

	mu    sync.Mutex
	order *list.List // front = most recently seen; values are *dedupeEntry
	index map[dedupeKey]*list.Element
}

type dedupeKey struct {
	subject, visitor string
	kind             Kind
}

type dedupeEntry struct {
	key  dedupeKey
	last time.Time
}

// NewDeduper returns a Deduper. Zero or negative window and maxEntries use
// the defaults; a nil now uses time.Now.
func NewDeduper(window time.Duration, maxEntries int, now func() time.Time) *Deduper {
	if window <= 0 {
		window = DefaultDedupeWindow
	}
	if maxEntries <= 0 {
		maxEntries = DefaultDedupeEntries
	}
	if now == nil {
		now = time.Now
	}
	return &Deduper{
		window: window,
		max:    maxEntries,
		now:    now,
		order:  list.New(),
		index:  make(map[dedupeKey]*list.Element),
	}
}

// Allow reports whether the event should be recorded: true the first time
// a key is seen, or when it was last seen a full window ago; false for a
// repeat inside the window.
func (d *Deduper) Allow(subject, visitorKey string, kind Kind) bool {
	k := dedupeKey{subject, visitorKey, kind}
	now := d.now()

	d.mu.Lock()
	defer d.mu.Unlock()

	d.expire(now)
	if el, ok := d.index[k]; ok {
		e := el.Value.(*dedupeEntry)
		repeat := now.Sub(e.last) < d.window
		e.last = now
		d.order.MoveToFront(el)
		return !repeat
	}
	d.index[k] = d.order.PushFront(&dedupeEntry{key: k, last: now})
	for d.order.Len() > d.max {
		d.remove(d.order.Back())
	}
	return true
}

// Len returns the number of keys held.
func (d *Deduper) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.order.Len()
}

// expire drops keys idle for a full window. The list is ordered by last
// use, so they are all at the back.
func (d *Deduper) expire(now time.Time) {
	for el := d.order.Back(); el != nil; el = d.order.Back() {
		if now.Sub(el.Value.(*dedupeEntry).last) < d.window {
			return
		}
		d.remove(el)
	}
}

func (d *Deduper) remove(el *list.Element) {
	delete(d.index, el.Value.(*dedupeEntry).key)
	d.order.Remove(el)
}
