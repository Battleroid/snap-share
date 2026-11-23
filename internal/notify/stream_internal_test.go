package notify

import (
	"testing"
	"time"
)

// A stream that doesn't read never blocks the publisher: it misses events
// and is marked to resync from the count.
func TestBrokerNeverBlocks(t *testing.T) {
	b := newBroker()
	lim := DefaultStreamLimits
	slow, err := b.subscribe(1, lim)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := b.subscribe(2, lim)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range streamQueue * 4 {
			b.publish(1, readEvent(0, 0))
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a full stream")
	}
	if len(slow.ch) != streamQueue || !slow.missed.Load() {
		t.Fatalf("queue %d, missed %v", len(slow.ch), slow.missed.Load())
	}
	if len(other.ch) != 0 || other.missed.Load() {
		t.Fatal("another user's stream got the events")
	}
	b.unsubscribe(slow)
	b.unsubscribe(slow)
	b.unsubscribe(other)
	if n := b.open(); n != 0 || len(b.subs) != 0 {
		t.Fatalf("%d open, %d users", n, len(b.subs))
	}
}

func TestAgo(t *testing.T) {
	now := time.Date(2026, 11, 22, 14, 3, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{
		30 * time.Second: "now", 2 * time.Minute: "2m", 59 * time.Minute: "59m", time.Hour: "1h", 23 * time.Hour: "23h",
		30 * time.Hour: "yesterday", 3 * 24 * time.Hour: "3d", 10 * 24 * time.Hour: "2026-11-12", -time.Minute: "now",
	} {
		if got := ago(now.Add(-d), now); got != want {
			t.Errorf("ago(-%v) = %q, want %q", d, got, want)
		}
	}
}

func TestSecretEntryLinks(t *testing.T) {
	for id, want := range map[string]string{
		"QUJDREVGR0hJSktMTU5PUA": "/dashboard/secrets/QUJDREVGR0hJSktMTU5PUA/timeline",
		"":                       "/dashboard/secrets",
		"../../admin/x/yy/zz/ab": "/dashboard/secrets",
	} {
		if got := secretLink(id); got != want {
			t.Errorf("secretLink(%q) = %q", id, got)
		}
	}
}
