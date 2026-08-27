package engine

import (
	"context"
	"sync"
)

// hostLimiter gates concurrent in-flight fetches per host, created lazily
// on first sight of a host.
type hostLimiter struct {
	perHost int
	mu      sync.Mutex
	sems    map[string]chan struct{}
}

func newHostLimiter(perHost int) *hostLimiter {
	return &hostLimiter{perHost: perHost, sems: make(map[string]chan struct{})}
}

func (h *hostLimiter) semFor(host string) chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	sem, ok := h.sems[host]
	if !ok {
		sem = make(chan struct{}, h.perHost)
		h.sems[host] = sem
	}
	return sem
}

// Acquire blocks until a per-host slot is free or ctx is done.
func (h *hostLimiter) Acquire(ctx context.Context, host string) error {
	sem := h.semFor(host)
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *hostLimiter) Release(host string) {
	h.mu.Lock()
	sem := h.sems[host]
	h.mu.Unlock()
	if sem != nil {
		<-sem
	}
}
