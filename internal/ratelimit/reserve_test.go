package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Parallel reservations never get more tokens than the bucket holds, kept
// reservations stay spent, and refunds come back.
func TestReserveCannotBeRaced(t *testing.T) {
	now := time.Unix(1000, 0)
	l := New(5, 15*time.Minute, 5).WithClock(func() time.Time { return now })
	ctx := context.Background()

	var granted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 40 {
		wg.Go(func() {
			<-start
			res, ok, _ := l.Reserve(ctx, "k", time.Second)
			if !ok {
				return
			}
			granted.Add(1)
			time.Sleep(5 * time.Millisecond) // the expensive part
			res.Keep()
		})
	}
	close(start)
	wg.Wait()
	if n := granted.Load(); n != 5 {
		t.Fatalf("40 parallel reservations got %d tokens, want 5", n)
	}
	if _, ok, wait := l.Reserve(ctx, "k", 0); ok || wait <= 0 {
		t.Fatalf("bucket should be empty: ok=%v wait=%v", ok, wait)
	}
}

// Refunded reservations (successful attempts) never use up the budget, even
// when more of them run at once than the bucket holds: the extra ones wait.
func TestReserveRefundsLetParallelSuccessesThrough(t *testing.T) {
	l := New(5, 15*time.Minute, 5)
	ctx := context.Background()
	var ok atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 20 {
		wg.Go(func() {
			<-start
			res, granted, _ := l.Reserve(ctx, "k", 5*time.Second)
			if !granted {
				return
			}
			time.Sleep(2 * time.Millisecond)
			res.Refund()
			ok.Add(1)
		})
	}
	close(start)
	wg.Wait()
	if n := ok.Load(); n != 20 {
		t.Fatalf("%d of 20 refunded attempts got through", n)
	}
	if !full(l, "k") {
		t.Fatal("refunds did not restore the bucket")
	}
}

func TestReserveWaitsOnlyForOutstanding(t *testing.T) {
	now := time.Unix(1000, 0)
	l := New(1, time.Hour, 1).WithClock(func() time.Time { return now })
	ctx := context.Background()
	res, ok, _ := l.Reserve(ctx, "k", 0)
	if !ok {
		t.Fatal("first reservation refused")
	}
	// One outstanding: the next waits, and gets the token once it is refunded.
	got := make(chan bool, 1)
	go func() {
		r2, ok, _ := l.Reserve(ctx, "k", 5*time.Second)
		if ok {
			r2.Keep()
		}
		got <- ok
	}()
	time.Sleep(20 * time.Millisecond)
	res.Refund()
	if !<-got {
		t.Fatal("waiter did not get the refunded token")
	}
	// None outstanding and none left: refused at once.
	t0 := time.Now()
	if _, ok, _ := l.Reserve(ctx, "k", 5*time.Second); ok || time.Since(t0) > time.Second {
		t.Fatal("expected an immediate refusal")
	}
	// A cancelled context ends the wait.
	r3 := &Reservation{l: l, key: "k"}
	l.mu.Lock()
	l.buckets["k"].held++
	l.mu.Unlock()
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, ok, _ := l.Reserve(cctx, "k", 5*time.Second); ok {
		t.Fatal("cancelled wait succeeded")
	}
	r3.Keep()
	r3.Refund() // only the first call counts
	if l.buckets["k"].held != 0 || l.buckets["k"].tokens >= 1 {
		t.Fatalf("held=%d tokens=%v", l.buckets["k"].held, l.buckets["k"].tokens)
	}
	// Reset with nothing outstanding forgets the key.
	l.Reset("k")
	if _, ok := l.buckets["k"]; ok {
		t.Fatal("reset kept the bucket")
	}
}

func full(l *Limiter, key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	return !ok || (b.held == 0 && b.tokens >= l.burst)
}
