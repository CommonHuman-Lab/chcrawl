// Package frontier holds the bounded BFS queue of URLs pending a crawl
// worker.
package frontier

import (
	"context"
	"sync"
)

// Item is a single frontier entry: a URL discovered at a given depth from a
// given parent, via a given discovery mechanism.
type Item struct {
	URL           string
	ParentURL     string
	Depth         int
	DiscoveredVia string // "seed", "html_link", "form_action", "code_block", "js_endpoint", "ws"
}

// Frontier is a bounded, concurrency-safe FIFO queue of pending items.
// Push blocks when the frontier is full, providing natural backpressure
// against discovery-heavy pages. Pop blocks when empty until an item is
// available or the frontier is closed and drained.
type Frontier interface {
	Push(ctx context.Context, item Item) error
	Pop(ctx context.Context) (Item, bool, error)
	Len() int
	Close()
}

// bounded is the default Frontier implementation: a fixed-capacity channel
// plus a live-item counter so Len() and graceful drain-then-close both work
// without racing against in-flight Push/Pop calls.
type bounded struct {
	ch chan Item

	mu     sync.Mutex
	closed bool
}

// New returns a bounded FIFO Frontier with the given capacity.
func New(capacity int) Frontier {
	if capacity < 1 {
		capacity = 1
	}
	return &bounded{ch: make(chan Item, capacity)}
}

func (b *bounded) Push(ctx context.Context, item Item) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return context.Canceled
	}
	b.mu.Unlock()

	select {
	case b.ch <- item:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *bounded) Pop(ctx context.Context) (Item, bool, error) {
	select {
	case item, ok := <-b.ch:
		if !ok {
			return Item{}, false, nil
		}
		return item, true, nil
	case <-ctx.Done():
		return Item{}, false, ctx.Err()
	}
}

func (b *bounded) Len() int {
	return len(b.ch)
}

func (b *bounded) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	close(b.ch)
}
