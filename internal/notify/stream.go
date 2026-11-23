package notify

// Live updates for the bell: GET /notifications/stream, Server-Sent Events.
//
// An in-process broker keyed by user id fans out what addInbox, MarkRead and
// MarkAllRead publish after their transactions commit. Publishing never
// blocks: a stream whose queue is full misses the event and resyncs from the
// unread count. Streams are bounded per user and in all, and each is closed
// after StreamLimits.MaxAge, so the browser reconnects and its session is
// checked again.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Battleroid/snap-share/internal/auth"
	"github.com/Battleroid/snap-share/internal/clientip"
)

// StreamLimits bound the notification streams.
type StreamLimits struct {
	PerUser   int           // open streams per user (not from SNAP_RATELIMIT_EXEMPT addresses)
	Total     int           // open streams in all
	MaxAge    time.Duration // a stream is closed after this; the browser reconnects
	Heartbeat time.Duration // interval of the keep-alive comment
}

// DefaultStreamLimits apply to the zero fields of Deps.Streams.
var DefaultStreamLimits = StreamLimits{PerUser: 4, Total: 500, MaxAge: 5 * time.Minute, Heartbeat: 25 * time.Second}

func (l StreamLimits) withDefaults() StreamLimits {
	d := DefaultStreamLimits
	if l.PerUser > 0 {
		d.PerUser = l.PerUser
	}
	if l.Total > 0 {
		d.Total = l.Total
	}
	if l.MaxAge > 0 {
		d.MaxAge = l.MaxAge
	}
	if l.Heartbeat > 0 {
		d.Heartbeat = l.Heartbeat
	}
	return d
}

const (
	streamRetry        = 5 * time.Second  // the browser's reconnection delay ("retry:")
	streamQueue        = 16               // events held per stream before it has to resync
	streamWriteTimeout = 15 * time.Second // per write, so a client that stops reading can't hold a stream
)

var errTooManyStreams = errors.New("notify: too many streams")

// streamEvent is one SSE event: its name and JSON data.
type streamEvent struct {
	name string
	data []byte
}

func (e streamEvent) frame() []byte {
	return fmt.Appendf(nil, "event: %s\ndata: %s\n\n", e.name, e.data)
}

func event(name string, v any) streamEvent {
	b, _ := json.Marshal(v) // plain structs; cannot fail
	return streamEvent{name: name, data: b}
}

// entryJSON is an entry as the browser renders it (with textContent only).
type entryJSON struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Icon   string `json:"icon"`
	Title  string `json:"title"`
	Body   string `json:"body,omitempty"`
	Link   string `json:"link"`
	Time   string `json:"time"` // "now"
	At     string `json:"at"`   // RFC 3339
	Unread bool   `json:"unread"`
}

type countJSON struct {
	Unread int  `json:"unread"`
	Resync bool `json:"resync,omitempty"` // events were missed: reload what is shown
}

type readJSON struct {
	Unread int   `json:"unread"`
	ID     int64 `json:"id,omitempty"`  // this entry was read
	All    bool  `json:"all,omitempty"` // every entry was read
}

func entryEvent(e Entry, unread int, now time.Time) streamEvent {
	return event("notification", struct {
		Unread int       `json:"unread"`
		Entry  entryJSON `json:"entry"`
	}{unread, entryJSON{
		ID: e.ID, Kind: e.Kind, Status: e.Status, Icon: e.Icon(), Title: e.Title, Body: e.Body, Link: e.Link,
		Time: ago(e.At, now), At: e.At.UTC().Format(time.RFC3339), Unread: e.Unread(),
	}})
}

func readEvent(unread int, id int64) streamEvent {
	return event("read", readJSON{Unread: unread, ID: id, All: id == 0})
}

type subscriber struct {
	user   int64
	ch     chan streamEvent
	missed atomic.Bool
}

// broker fans events out to the open streams of each user.
type broker struct {
	mu    sync.Mutex
	subs  map[int64]map[*subscriber]struct{}
	total int

	done     chan struct{} // closed on shutdown: every stream ends
	doneOnce sync.Once
}

func newBroker() *broker {
	return &broker{subs: map[int64]map[*subscriber]struct{}{}, done: make(chan struct{})}
}

func (b *broker) subscribe(user int64, lim StreamLimits) (*subscriber, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.total >= lim.Total || len(b.subs[user]) >= lim.PerUser {
		return nil, errTooManyStreams
	}
	sub := &subscriber{user: user, ch: make(chan streamEvent, streamQueue)}
	m := b.subs[user]
	if m == nil {
		m = map[*subscriber]struct{}{}
		b.subs[user] = m
	}
	m[sub] = struct{}{}
	b.total++
	return sub, nil
}

func (b *broker) unsubscribe(sub *subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()
	m := b.subs[sub.user]
	if _, ok := m[sub]; !ok {
		return
	}
	delete(m, sub)
	b.total--
	if len(m) == 0 {
		delete(b.subs, sub.user)
	}
}

// publish queues ev on the user's streams without blocking.
func (b *broker) publish(user int64, ev streamEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for sub := range b.subs[user] {
		select {
		case sub.ch <- ev:
		default:
			sub.missed.Store(true)
		}
	}
}

func (b *broker) open() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total
}

func (b *broker) shutdown() { b.doneOnce.Do(func() { close(b.done) }) }

// CloseStreams ends every open stream, now and for good (server shutdown):
// http.Server.Shutdown waits for handlers, and a stream would otherwise
// hold it for up to StreamLimits.MaxAge.
func (s *Service) CloseStreams() { s.streams.shutdown() }

// stream is GET /notifications/stream: the unread count on connect, then
// an event per new notification and per read, a comment every Heartbeat,
// and the end after MaxAge.
func (s *Service) stream(w http.ResponseWriter, r *http.Request) {
	p := auth.FromContext(r.Context())
	lim := *s.limits.Load()
	if clientip.In(clientip.From(r.Context()), s.d.Cfg.RateLimitExempt) {
		lim.PerUser = lim.Total // trusted networks (SNAP_RATELIMIT_EXEMPT): only the global cap
	}
	select {
	case <-s.streams.done:
		s.d.Errors.Error(w, r, http.StatusServiceUnavailable, "The server is shutting down.")
		return
	default:
	}
	sub, err := s.streams.subscribe(p.UserID, lim)
	if err != nil {
		w.Header().Set("Retry-After", "60")
		s.d.Errors.Error(w, r, http.StatusTooManyRequests, "Too many open notification streams.")
		return
	}
	defer s.streams.unsubscribe(sub)
	ctx := r.Context()
	// Subscribed first, then counted: nothing published in between is lost.
	n, err := unreadCount(ctx, s.d.DB.R, p.UserID)
	if err != nil {
		s.inboxError(w, r, err)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	// Without a server WriteTimeout, a deadline set here would outlive the
	// stream on a kept-alive connection.
	defer func() { _ = rc.SetWriteDeadline(time.Time{}) }()
	send := func(b []byte) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout)) // unsupported in some tests; the write still works
		if _, err := w.Write(b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	retry := fmt.Appendf(nil, "retry: %d\n\n", streamRetry.Milliseconds())
	if !send(append(retry, event("count", countJSON{Unread: n}).frame()...)) {
		return
	}
	// resync sends the count after events were dropped.
	resync := func() bool {
		if !sub.missed.Swap(false) {
			return true
		}
		n, err := unreadCount(ctx, s.d.DB.R, p.UserID)
		return err == nil && send(event("count", countJSON{Unread: n, Resync: true}).frame())
	}

	life := time.NewTimer(lim.MaxAge)
	defer life.Stop()
	beat := time.NewTicker(lim.Heartbeat)
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.streams.done:
			send(retry)
			return
		case <-life.C:
			send(retry)
			return
		case <-beat.C:
			if sub.missed.Load() {
				if !resync() {
					return
				}
			} else if !send([]byte(": ping\n\n")) {
				return
			}
		case ev := <-sub.ch:
			if !send(ev.frame()) || !resync() {
				return
			}
		}
	}
}
