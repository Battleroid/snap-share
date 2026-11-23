package ratelimit

import (
	"testing"
	"time"
)

func TestWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	w := NewWindow(3, time.Minute).WithClock(func() time.Time { return now })
	// Spread over the interval, still only n get through.
	for i := range 3 {
		if ok, _ := w.Allow("a"); !ok {
			t.Fatalf("event %d denied", i)
		}
		now = now.Add(29 * time.Second)
	}
	// 58 s after the first event: a bucket would have refilled by now.
	now = now.Add(-29 * time.Second)
	ok, wait := w.Allow("a")
	if ok || wait != 2*time.Second {
		t.Fatalf("4th event at +58s: %v, wait %v; want denied, wait 2s", ok, wait)
	}
	if ok, _ := w.Check("a"); ok {
		t.Fatal("Check passed a full window")
	}
	if ok, _ := w.Allow("b"); !ok {
		t.Fatal("keys must be independent")
	}
	// The first event leaves the interval at +60s: one more, not three.
	now = now.Add(2 * time.Second)
	if ok, _ := w.Allow("a"); !ok {
		t.Fatal("denied after the first event left the interval")
	}
	if ok, _ := w.Allow("a"); ok {
		t.Fatal("allowed a second event before the next one left the interval")
	}
	// Check never records.
	for range 10 {
		if ok, _ := w.Check("fresh"); !ok {
			t.Fatal("Check recorded an event")
		}
	}
	now = now.Add(time.Hour)
	w.Allow("c")
	if len(w.keys) != 1 { // a and b are past the interval, fresh never stored
		t.Fatalf("sweep left %d keys", len(w.keys))
	}
}
