package fetch

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/commonhuman-lab/chcrawl/internal/retry"
)

// Fetcher performs a single HTTP fetch.
type Fetcher interface {
	Fetch(ctx context.Context, req Request) (*Response, error)
}

// Config controls how a default Fetcher is constructed.
type Config struct {
	Timeout             time.Duration
	Proxy               string
	Headers             map[string]string
	Cookies             string
	InsecureSkipVerify  bool
	MaxBodyBytes        int64
	MaxRedirects        int
	AllowedContentTypes []string
	RetryPolicy         retry.Policy // nil disables retries entirely
	Delay               time.Duration
}

type httpFetcher struct {
	client              *http.Client
	baseHeaders         http.Header
	maxBodyBytes        int64
	allowedContentTypes []string
	retryPolicy         retry.Policy
	delay               time.Duration
}

// New builds the default net/http-backed Fetcher.
func New(cfg Config) (Fetcher, error) {
	transport, err := newTransport(cfg.Proxy, cfg.InsecureSkipVerify)
	if err != nil {
		return nil, err
	}

	maxRedirects := cfg.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = 20
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errTooManyRedirects
			}
			return nil
		},
	}

	headers := http.Header{}
	headers.Set("User-Agent", randomUA())
	for k, v := range cfg.Headers {
		headers.Set(k, v)
	}
	if cfg.Cookies != "" {
		headers.Set("Cookie", cfg.Cookies)
	}

	return &httpFetcher{
		client:              client,
		baseHeaders:         headers,
		maxBodyBytes:        cfg.MaxBodyBytes,
		allowedContentTypes: cfg.AllowedContentTypes,
		retryPolicy:         cfg.RetryPolicy,
		delay:               cfg.Delay,
	}, nil
}

var errTooManyRedirects = errors.New("fetch: too many redirects")

// isRetryableErr excludes deterministic, never-going-to-succeed failures
// from the retry loop — retrying a redirect loop with backoff just wastes
// time three times over instead of once, since the outcome can't change
// between attempts.
func isRetryableErr(err error) bool {
	return !errors.Is(err, errTooManyRedirects)
}

func (f *httpFetcher) Fetch(ctx context.Context, req Request) (*Response, error) {
	if f.retryPolicy == nil {
		return f.doOnce(ctx, req)
	}
	var lastErr error
	var retryAttempts int
	var retryDelay time.Duration
	for attempt := 0; ; attempt++ {
		resp, err := f.doOnce(ctx, req)
		if err != nil {
			lastErr = err
			if !isRetryableErr(err) {
				return nil, lastErr
			}
			d := f.retryPolicy.Next(attempt, 0, "", err)
			if !d.Retry || !sleepBackoff(ctx, req.Gate, d.Delay) {
				return nil, lastErr
			}
			retryAttempts++
			retryDelay += d.Delay
			continue
		}
		retryAfter := ""
		if resp.Headers != nil {
			retryAfter = resp.Headers.Get("Retry-After")
		}
		d := f.retryPolicy.Next(attempt, resp.StatusCode, retryAfter, nil)
		if !d.Retry || !sleepBackoff(ctx, req.Gate, d.Delay) {
			resp.RetryAttempts = retryAttempts
			resp.RetryDelay = retryDelay
			return resp, nil
		}
		retryAttempts++
		retryDelay += d.Delay
	}
}

// sleepBackoff sleeps out a retry delay, releasing gate (if non-nil) for the
// sleep's duration and reacquiring it before returning, so a held
// per-host concurrency slot isn't wasted on a goroutine that's merely
// waiting out backoff. Returns false if ctx is cancelled during the sleep
// or the reacquire — callers must not retry after a false return, and must
// not assume the gate is held in that case (see HostGate's doc comment).
func sleepBackoff(ctx context.Context, gate HostGate, d time.Duration) bool {
	if gate == nil {
		return sleepCtx(ctx, d)
	}
	gate.Release()
	if !sleepCtx(ctx, d) {
		return false
	}
	return gate.Acquire(ctx) == nil
}

// sleepCtx sleeps for d, or returns false immediately if ctx is cancelled
// first — callers must not retry after a false return.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (f *httpFetcher) doOnce(ctx context.Context, req Request) (*Response, error) {
	if f.delay > 0 && !sleepCtx(ctx, f.delay) {
		return nil, ctx.Err()
	}

	method := req.Method
	if method == "" {
		method = http.MethodGet
	}

	var bodyReader io.Reader
	if len(req.Body) > 0 {
		bodyReader = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, bodyReader)
	if err != nil {
		return nil, err
	}
	// f.baseHeaders is built once at fetcher construction and never mutated
	// afterward, so concurrent GET requests (the overwhelming majority of
	// any crawl) can safely share it directly. Only clone when this request
	// needs its own Content-Type, since that mutates the map in place.
	httpReq.Header = f.baseHeaders
	if req.ContentType != "" {
		httpReq.Header = f.baseHeaders.Clone()
		httpReq.Header.Set("Content-Type", req.ContentType)
	}

	var chain []RedirectHop
	client := *f.client
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) > 0 {
			prev := via[len(via)-1]
			status := 0
			// r.Response is the redirect response that produced r, i.e. the
			// response from prev's URL — not prev.Response, which net/http
			// never populates on entries in `via`.
			if r.Response != nil {
				status = r.Response.StatusCode
			}
			chain = append(chain, RedirectHop{URL: prev.URL.String(), StatusCode: status})
		}
		return f.client.CheckRedirect(r, via)
	}

	start := time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	contentType := resp.Header.Get("Content-Type")

	if !contentTypeAllowed(contentType, f.allowedContentTypes) {
		_, _ = io.CopyN(io.Discard, resp.Body, 4096)
		return &Response{
			RequestedURL:  req.URL,
			FinalURL:      resp.Request.URL.String(),
			RedirectChain: chain,
			StatusCode:    resp.StatusCode,
			Headers:       resp.Header,
			ContentType:   contentType,
			FetchDuration: time.Since(start),
		}, nil
	}

	limited := io.LimitReader(resp.Body, f.maxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	truncated := false
	if int64(len(body)) > f.maxBodyBytes {
		body = body[:f.maxBodyBytes]
		truncated = true
	}

	return &Response{
		RequestedURL:  req.URL,
		FinalURL:      resp.Request.URL.String(),
		RedirectChain: chain,
		StatusCode:    resp.StatusCode,
		Headers:       resp.Header,
		Body:          body,
		ContentType:   contentType,
		Truncated:     truncated,
		FetchDuration: time.Since(start),
	}, nil
}

func contentTypeAllowed(contentType string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	ct := strings.ToLower(contentType)
	for _, a := range allowed {
		if strings.Contains(ct, strings.ToLower(a)) {
			return true
		}
	}
	return false
}
