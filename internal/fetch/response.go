package fetch

import (
	"net/http"
	"time"
)

// RedirectHop is one hop in a followed redirect chain.
type RedirectHop struct {
	URL        string
	StatusCode int
}

// Request describes a single fetch.
type Request struct {
	URL         string
	Method      string // defaults to GET if empty
	Body        []byte
	ContentType string // sent as the Content-Type header when Body is set
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
