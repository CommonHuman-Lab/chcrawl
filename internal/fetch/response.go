package fetch

import (
	"context"
	"net/http"
	"time"
)

// RedirectHop is one hop in a followed redirect chain.
type RedirectHop struct {
	URL        string
	StatusCode int
}

// HostGate lets a caller release and reacquire an external per-host
// concurrency slot around a retry's backoff sleep. When set on a Request,
// Fetch releases the slot before sleeping out a retry delay and reacquires
// it before the next attempt, instead of holding it for the sleep's full
// duration — so other pending requests to the same host aren't blocked
// behind a goroutine that is merely waiting out a backoff, not doing any
// network work. Nil (the default) preserves the original hold-through-sleep
// behavior exactly.
//
// Acquire's caller must treat a non-nil error as "the slot is not held" —
// implementations must guarantee Release is then safe to call again (e.g.
// track held/not-held state) so a caller's own unconditional release-on-exit
// can't double-release after a failed reacquire.
type HostGate interface {
	Release()
	Acquire(ctx context.Context) error
}

// Request describes a single fetch.
type Request struct {
	URL         string
	Method      string // defaults to GET if empty
	Body        []byte
	ContentType string // sent as the Content-Type header when Body is set
	// Gate, if set, is released during retry backoff sleeps and reacquired
	// before the next attempt. See HostGate's doc comment.
	Gate HostGate
}

// Response is the result of a single fetch, including the full followed
// redirect chain.
type Response struct {
	RequestedURL  string
	FinalURL      string
	RedirectChain []RedirectHop
	StatusCode    int
	Headers       http.Header
	Body          []byte
	ContentType   string
	Truncated     bool
	FetchDuration time.Duration
	RetryAttempts int
	RetryDelay    time.Duration
}
