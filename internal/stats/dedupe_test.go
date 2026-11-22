package stats

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a settable clock for tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 22, 14, 3, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestDeduperWindow(t *testing.T) {
	clock := newFakeClock()
	d := NewDeduper(30*time.Minute, 100, clock.Now)

	if !d.Allow("1", "v", KindView) {
		t.Fatal("first view suppressed")
	}
	clock.Add(10 * time.Minute)
	if d.Allow("1", "v", KindView) {
		t.Error("reload after 10m counted")
	}
	// Other kinds, subjects and visitors are separate.
	if !d.Allow("1", "v", KindDownload) || !d.Allow("2", "v", KindView) || !d.Allow("1", "w", KindView) {
		t.Error("distinct key suppressed")
	}
	// The window restarts on each repeat: 29m after the reload is still
	// inside it.
	clock.Add(29 * time.Minute)
	if d.Allow("1", "v", KindView) {
		t.Error("repeat 29m after the last one counted")
	}
	clock.Add(30 * time.Minute)
	if !d.Allow("1", "v", KindView) {
		t.Error("view after 30m idle suppressed")
	}
}

// TestDeduperRangeRequests is the video case: a player issues many Range
// requests during one playback; they count as one play.
func TestDeduperRangeRequests(t *testing.T) {
	clock := newFakeClock()
	d := NewDeduper(0, 0, clock.Now)
	plays := 0
	for range 120 { // a request every 30s for an hour
		if d.Allow("7", "visitor", KindPlay) {
			plays++
		}
		clock.Add(30 * time.Second)
	}
	if plays != 1 {
		t.Errorf("plays = %d, want 1", plays)
	}
	clock.Add(DefaultDedupeWindow)
	if !d.Allow("7", "visitor", KindPlay) {
		t.Error("new playback after the window suppressed")
	}
}

func TestDeduperBounded(t *testing.T) {
	clock := newFakeClock()
	d := NewDeduper(time.Hour, 3, clock.Now)
	for i := range 5 {
		d.Allow(fmt.Sprint(i), "v", KindView)
	}
	if d.Len() != 3 {
		t.Fatalf("Len = %d, want 3", d.Len())
	}
	// 0 and 1 were evicted (least recently seen) and count again.
	if !d.Allow("0", "v", KindView) {
		t.Error("evicted key still suppressed")
	}
	if d.Allow("4", "v", KindView) {
		t.Error("recent key not suppressed")
	}
	// Expired keys are dropped as time passes.
	clock.Add(2 * time.Hour)
	d.Allow("new", "v", KindView)
	if d.Len() != 1 {
		t.Errorf("Len after expiry = %d, want 1", d.Len())
	}
}

func TestDeduperConcurrent(t *testing.T) {
	d := NewDeduper(time.Hour, 1000, nil)
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for i := range 200 {
				if d.Allow(fmt.Sprint(i%50), "v", KindPlay) {
					allowed.Add(1)
				}
			}
		})
	}
	wg.Wait()
	if allowed.Load() != 50 {
		t.Errorf("allowed = %d, want 50", allowed.Load())
	}
}
