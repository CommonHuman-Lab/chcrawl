package headless

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-rod/rod"
)

func TestPagePool_BoundsSize(t *testing.T) {
	pool := newPagePool(2, func(ctx context.Context) (*rod.Page, error) { return &rod.Page{}, nil }, func(*rod.Page) {})

	p1, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p2, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	acquired := make(chan struct{})
	go func() {
		p3, err := pool.Acquire(context.Background())
		if err != nil {
			t.Error(err)
		}
		_ = p3
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("third Acquire returned before a slot was released")
	case <-time.After(50 * time.Millisecond):
	}

	pool.Release(p1)
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("third Acquire did not unblock after Release")
	}
	pool.Release(p2)
}

func TestPagePool_AcquireCancelled(t *testing.T) {
	pool := newPagePool(1, func(ctx context.Context) (*rod.Page, error) { return &rod.Page{}, nil }, func(*rod.Page) {})
	p, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Release(p)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := pool.Acquire(ctx); err == nil {
		t.Fatal("expected context deadline error, got nil")
	}
}

func TestPagePool_OpenFailureReleasesSlot(t *testing.T) {
	wantErr := errors.New("open failed")
	pool := newPagePool(1, func(ctx context.Context) (*rod.Page, error) { return nil, wantErr }, func(*rod.Page) {})

	if _, err := pool.Acquire(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Acquire error = %v, want %v", err, wantErr)
	}

	// A failed open must not leak the semaphore slot it provisionally took.
	pool.open = func(ctx context.Context) (*rod.Page, error) { return &rod.Page{}, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := pool.Acquire(ctx); err != nil {
		t.Fatalf("Acquire after failed open = %v, want nil (slot should be free)", err)
	}
}
