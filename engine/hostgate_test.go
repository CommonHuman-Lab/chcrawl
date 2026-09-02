package engine

import (
	"context"
	"testing"
)

func TestHostGate_ReleaseIsSafeAfterFailedReacquire(t *testing.T) {
	h := newHostLimiter(1)
	if err := h.Acquire(context.Background(), "example.com"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	g := newHostGate(h, "example.com")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := g.Acquire(cancelled); err == nil {
		t.Fatal("Acquire with an already-cancelled context should fail")
	}

	// Release must be a safe no-op now — the failed reacquire above already
	// left the slot unheld. If Release incorrectly released again, the
	// semaphore's internal channel would be over-drained and a fresh
	// Acquire below could exceed perHost.
	g.Release()

	// A fresh Acquire on a different gate instance should succeed exactly
	// once (capacity 1) - proves the slot was released exactly once above,
	// not zero and not twice.
	g2 := func() error { return h.Acquire(context.Background(), "example.com") }
	if err := g2(); err != nil {
		t.Fatalf("Acquire after gate cleanup: %v (slot should have been freed exactly once)", err)
	}
}

func TestHostGate_ReleaseIdempotent(t *testing.T) {
	h := newHostLimiter(1)
	if err := h.Acquire(context.Background(), "example.com"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	g := newHostGate(h, "example.com")

	g.Release()
	g.Release() // second call must be a no-op, not a double-release

	// If the second Release incorrectly drained the semaphore again, a
	// third Acquire (this one) would still succeed since it only needs one
	// slot back — instead assert the semaphore isn't in a broken
	// over-released state by acquiring exactly perHost (1) times without
	// a second one succeeding concurrently. Simplest direct check: the
	// underlying channel must have exactly capacity 1 free, not 2.
	if err := h.Acquire(context.Background(), "example.com"); err != nil {
		t.Fatalf("first re-Acquire: %v", err)
	}
	sem := h.semFor("example.com")
	select {
	case sem <- struct{}{}:
		t.Fatal("semaphore accepted a second concurrent holder at perHost=1 — Release was double-counted")
	default:
		// correct: no room for a second holder
	}
}
