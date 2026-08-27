package frontier

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestFrontier_FIFOOrder(t *testing.T) {
	f := New(10)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := f.Push(ctx, Item{URL: string(rune('a' + i))}); err != nil {
			t.Fatalf("Push: %v", err)
		}
	}
	for i := 0; i < 5; i++ {
		item, ok, err := f.Pop(ctx)
		if err != nil || !ok {
			t.Fatalf("Pop: ok=%v err=%v", ok, err)
		}
		want := string(rune('a' + i))
		if item.URL != want {
			t.Errorf("Pop #%d = %q, want %q (FIFO order violated)", i, item.URL, want)
		}
	}
}

func TestFrontier_PushBlocksWhenFull(t *testing.T) {
	f := New(1)
	ctx := context.Background()
	if err := f.Push(ctx, Item{URL: "a"}); err != nil {
		t.Fatalf("Push: %v", err)
	}

	blocked := make(chan struct{})
	go func() {
		pushCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		err := f.Push(pushCtx, Item{URL: "b"})
		if err == nil {
			t.Error("expected the second Push to block (and time out) while the frontier is full")
		}
		close(blocked)
	}()
	<-blocked
}

func TestFrontier_PopBlocksUntilPush(t *testing.T) {
	f := New(4)
	popped := make(chan Item, 1)
	go func() {
		item, ok, err := f.Pop(context.Background())
		if err != nil || !ok {
			t.Errorf("Pop: ok=%v err=%v", ok, err)
			return
		}
		popped <- item
	}()

	select {
	case <-popped:
		t.Fatal("Pop returned before anything was pushed")
	case <-time.After(30 * time.Millisecond):
	}

	if err := f.Push(context.Background(), Item{URL: "x"}); err != nil {
		t.Fatalf("Push: %v", err)
	}
	select {
	case item := <-popped:
		if item.URL != "x" {
			t.Errorf("got %q, want %q", item.URL, "x")
		}
	case <-time.After(time.Second):
		t.Fatal("Pop never returned after Push")
	}
}

func TestFrontier_CloseDrainsThenSignalsEmpty(t *testing.T) {
	f := New(4)
	ctx := context.Background()
	f.Push(ctx, Item{URL: "a"})
	f.Push(ctx, Item{URL: "b"})
	f.Close()

	for _, want := range []string{"a", "b"} {
		item, ok, err := f.Pop(ctx)
		if err != nil || !ok {
			t.Fatalf("Pop: ok=%v err=%v", ok, err)
		}
		if item.URL != want {
			t.Errorf("got %q, want %q", item.URL, want)
		}
	}
	_, ok, err := f.Pop(ctx)
	if err != nil || ok {
		t.Errorf("expected Pop on a closed, drained frontier to return ok=false, got ok=%v err=%v", ok, err)
	}
}

func TestFrontier_PopRespectsContextCancellation(t *testing.T) {
	f := New(4)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, ok, err := f.Pop(ctx)
	if ok || err == nil {
		t.Errorf("expected Pop to return an error on context cancellation, got ok=%v err=%v", ok, err)
	}
}

func TestFrontier_ConcurrentPushPop(t *testing.T) {
	f := New(16)
	ctx := context.Background()
	const n = 500

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if err := f.Push(ctx, Item{URL: "x"}); err != nil {
				t.Errorf("Push: %v", err)
				return
			}
		}
	}()

	received := 0
	for received < n {
		if _, ok, err := f.Pop(ctx); err != nil || !ok {
			t.Fatalf("Pop: ok=%v err=%v", ok, err)
		}
		received++
	}
	wg.Wait()
}
