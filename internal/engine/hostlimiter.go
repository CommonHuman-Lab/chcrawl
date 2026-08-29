package engine

import (
	"context"
	"sync"
	"sync/atomic"
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

// hostGate adapts a hostLimiter slot to fetch.HostGate, letting Fetch
// release and reacquire the slot around a retry's backoff sleep instead of
// holding it for the sleep's full duration. held tracks whether the slot is
// currently ours so Release is always safe to call — including from
// pipeline.go's own unconditional release-on-exit — even after a failed
// reacquire (e.g. context cancelled during backoff) already left the slot
// unheld.
type hostGate struct {
	hosts *hostLimiter
	host  string
	held  atomic.Bool
}

func newHostGate(hosts *hostLimiter, host string) *hostGate {
	g := &hostGate{hosts: hosts, host: host}
	g.held.Store(true)
	return g
}

func (g *hostGate) Release() {
	if g.held.CompareAndSwap(true, false) {
		g.hosts.Release(g.host)
	}
}

func (g *hostGate) Acquire(ctx context.Context) error {
	if err := g.hosts.Acquire(ctx, g.host); err != nil {
		return err
	}
	g.held.Store(true)
	return nil
}
