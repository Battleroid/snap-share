package ratelimit

import (
	"testing"
	"time"
)

func TestAllow(t *testing.T) {
	now := time.Unix(1000, 0)
	l := New(1, time.Second, 3).WithClock(func() time.Time { return now })
	for i := range 3 {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("burst %d denied", i)
		}
	}
	ok, wait := l.Allow("a")
	if ok || wait <= 0 || wait > time.Second {
		t.Fatalf("expected denial with wait ≤1s, got %v %v", ok, wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("keys must be independent")
	}
	now = now.Add(time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("refill")
	}
	l.Reset("a")
	for range 3 {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatal("reset should restore burst")
		}
	}
	// Check never consumes.
	for range 10 {
		if ok, _ := l.Check("fresh"); !ok {
			t.Fatal("check consumed a token")
		}
	}
	l.Allow("d")
	l.Allow("d")
	l.Allow("d")
	if ok, wait := l.Check("d"); ok || wait <= 0 {
		t.Fatal("check should report an empty bucket")
	}
	now = now.Add(time.Hour)
	l.Allow("c")
	if len(l.buckets) != 1 { // "fresh" was only checked, never stored
		t.Fatalf("sweep left %d buckets", len(l.buckets))
	}
}
