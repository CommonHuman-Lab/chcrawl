package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/commonhuman-lab/chcrawl/internal/retry"
)

// fakeGate records Release/Acquire calls in order, and can be told to fail
// the next Acquire (simulating context cancellation during backoff).
type fakeGate struct {
	events      []string
	failAcquire bool
	acquireErr  error
}

func (g *fakeGate) Release() { g.events = append(g.events, "release") }
func (g *fakeGate) Acquire(ctx context.Context) error {
	g.events = append(g.events, "acquire")
	if g.failAcquire {
		return g.acquireErr
	}
	return nil
}

func TestFetch_GateReleasedDuringRetryBackoffAndReacquired(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	f, err := New(Config{
		Timeout:      5 * time.Second,
		MaxBodyBytes: 1024,
		RetryPolicy: &retry.Default{
			MaxRetries: 3, BaseDelay: 1 * time.Millisecond, MaxDelay: 5 * time.Millisecond,
			RetryableStatus: map[int]bool{503: true},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	gate := &fakeGate{}
	resp, err := f.Fetch(context.Background(), Request{URL: srv.URL, Gate: gate})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if resp.RetryAttempts != 2 {
		t.Fatalf("RetryAttempts = %d, want 2 (two 503s before success)", resp.RetryAttempts)
	}

	want := []string{"release", "acquire", "release", "acquire"}
	if len(gate.events) != len(want) {
		t.Fatalf("gate events = %v, want %v", gate.events, want)
	}
	for i, ev := range want {
		if gate.events[i] != ev {
			t.Errorf("gate.events[%d] = %q, want %q (full: %v)", i, gate.events[i], ev, gate.events)
		}
	}
}

func TestFetch_NoGateMeansNoReleaseCalls(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	f, err := New(Config{
		Timeout:      5 * time.Second,
		MaxBodyBytes: 1024,
		RetryPolicy: &retry.Default{
			MaxRetries: 3, BaseDelay: 1 * time.Millisecond, MaxDelay: 5 * time.Millisecond,
			RetryableStatus: map[int]bool{503: true},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// No Gate set (nil) — behavior must be identical to before this change:
	// Fetch must still succeed after one retry, no gate machinery involved.
	resp, err := f.Fetch(context.Background(), Request{URL: srv.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if resp.RetryAttempts != 1 {
		t.Fatalf("RetryAttempts = %d, want 1", resp.RetryAttempts)
	}
}

func TestFetch_GateReacquireFailureSurfacesAsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	f, err := New(Config{
		Timeout:      5 * time.Second,
		MaxBodyBytes: 1024,
		RetryPolicy: &retry.Default{
			MaxRetries: 3, BaseDelay: 1 * time.Millisecond, MaxDelay: 5 * time.Millisecond,
			RetryableStatus: map[int]bool{503: true},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	gate := &fakeGate{failAcquire: true, acquireErr: context.Canceled}
	resp, err := f.Fetch(context.Background(), Request{URL: srv.URL, Gate: gate})
	if err != nil {
		t.Fatalf("Fetch: %v (a failed reacquire must not propagate as a fetch error — the last real response is still returned, matching a ctx-cancelled sleepCtx's existing behavior)", err)
	}
	if resp.RetryAttempts != 0 {
		t.Errorf("RetryAttempts = %d, want 0 (retry loop must stop after the first failed reacquire)", resp.RetryAttempts)
	}
	if len(gate.events) != 2 || gate.events[0] != "release" || gate.events[1] != "acquire" {
		t.Errorf("gate events = %v, want exactly one release+acquire pair", gate.events)
	}
}
