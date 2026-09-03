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

// HostGate lets a caller release and reacquire a per-host concurrency slot around a retry's
// backoff sleep, instead of holding it for the sleep's full duration. Nil (the default) preserves
// the original hold-through-sleep behavior. Implementations must make Release safe to call again
// after a failed Acquire, so an unconditional release-on-exit can't double-release.
type HostGate interface {
	Release()
	Acquire(ctx context.Context) error
}

// Request describes a single fetch.
type Request struct {
	URL         string
	Method      string // defaults to GET if empty
	Body        []byte
	ContentType string   // sent as the Content-Type header when Body is set
	Gate        HostGate // if set, released during retry backoff sleeps and reacquired before the next attempt
}

// Response is the result of a single fetch, including the full followed redirect chain.
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
	// Cookies is the jar's effective Cookie header for FinalURL after every Set-Cookie across the
	// whole redirect chain — unlike Headers.Get("Set-Cookie"), which misses intermediate hops.
	Cookies string
}
