package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHostLimiter_NeverExceedsCapacity(t *testing.T) {
	const perHost = 4
	const workers = 50
	const cyclesPerWorker = 200

	h := newHostLimiter(perHost)
	ctx := context.Background()

	var current int32
	var maxSeen int32
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := 0; c < cyclesPerWorker; c++ {
				if err := h.Acquire(ctx, "example.com"); err != nil {
					t.Errorf("Acquire: %v", err)
					return
				}
				n := atomic.AddInt32(&current, 1)
				for {
					m := atomic.LoadInt32(&maxSeen)
					if n <= m || atomic.CompareAndSwapInt32(&maxSeen, m, n) {
						break
					}
				}
				atomic.AddInt32(&current, -1)
				h.Release("example.com")
			}
		}()
	}
	wg.Wait()

	if maxSeen > perHost {
		t.Errorf("observed %d concurrent holders, want <= perHost=%d", maxSeen, perHost)
	}
	if maxSeen < 1 {
		t.Error("observed 0 concurrent holders — Acquire/Release never actually overlapped, test didn't exercise anything")
	}
}

func TestHostLimiter_AcquireRespectsContextCancellation(t *testing.T) {
	h := newHostLimiter(1)
	ctx := context.Background()

	if err := h.Acquire(ctx, "example.com"); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	// The single slot is now held; a second Acquire must block until ctx
	// is done, and return promptly (not hang past the deadline) once it is.
	shortCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := h.Acquire(shortCtx, "example.com")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected Acquire to fail once ctx deadline passes while the only slot is held")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Acquire took %s to respect a 50ms context deadline — too slow", elapsed)
	}
}

func TestHostLimiter_PerHostIsolation(t *testing.T) {
	h := newHostLimiter(1)
	ctx := context.Background()

	if err := h.Acquire(ctx, "a.example.com"); err != nil {
		t.Fatalf("Acquire host a: %v", err)
	}
	defer h.Release("a.example.com")

	// A different host must not be blocked by host a's exhausted capacity.
	done := make(chan error, 1)
	go func() {
		done <- h.Acquire(ctx, "b.example.com")
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Acquire host b: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire on an independent host blocked — hostLimiter is not isolating per-host capacity")
	}
}
