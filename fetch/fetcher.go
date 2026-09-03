package fetch

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
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
	jar                 *cookiejar.Jar
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

	// A jar is what lets a Set-Cookie on an intermediate redirect hop (a login POST's 302) take
	// effect on later requests — without one, only the final response's Headers are visible.
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
		Jar:       jar,
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
		jar:                 jar,
		baseHeaders:         headers,
		maxBodyBytes:        cfg.MaxBodyBytes,
		allowedContentTypes: cfg.AllowedContentTypes,
		retryPolicy:         cfg.RetryPolicy,
		delay:               cfg.Delay,
	}, nil
}

var errTooManyRedirects = errors.New("fetch: too many redirects")

// isRetryableErr excludes deterministic failures from the retry loop — retrying a redirect loop
// just wastes time, since the outcome can't change between attempts.
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

// sleepBackoff sleeps out a retry delay, releasing gate (if non-nil) for the duration and
// reacquiring it before returning. Returns false if ctx is cancelled; callers must not retry then.
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

// sleepCtx sleeps for d, or returns false immediately if ctx is cancelled first.
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
	// f.baseHeaders itself is never mutated after construction, but the
	// *map* handed to http.Request.Header must still be a fresh clone per
	// request: net/http's Transport writes into a request's Header map
	// internally during RoundTrip (e.g. default Accept-Encoding handling),
	// so every concurrent request sharing the same map instance races —
	// confirmed live as a "concurrent map iteration and map write" crash
	// once per-host concurrency was raised past chcrawl's conservative
	// default. Cloning is a small per-request map copy; sharing the map is
	// a crash waiting for enough concurrency to trigger it.
	httpReq.Header = f.baseHeaders.Clone()
	if req.ContentType != "" {
		httpReq.Header.Set("Content-Type", req.ContentType)
	}

	var chain []RedirectHop
	client := *f.client
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) > 0 {
			prev := via[len(via)-1]
			status := 0
			// r.Response is the redirect response from prev's URL; prev.Response is never
			// populated by net/http on entries in `via`.
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
	cookies := f.cookieHeader(resp.Request.URL)

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
			Cookies:       cookies,
		}, nil
	}

	limited := io.LimitReader(resp.Body, f.maxBodyBytes+1)
	body, err := readBody(limited, resp.ContentLength, f.maxBodyBytes)
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
		Cookies:       cookies,
	}, nil
}

// cookieHeader formats the jar's cookies for u in the same shape Config.Cookies expects, so a
// caller can feed a Response.Cookies value straight back into another fetcher's config.
func (f *httpFetcher) cookieHeader(u *url.URL) string {
	cookies := f.jar.Cookies(u)
	if len(cookies) == 0 {
		return ""
	}
	parts := make([]string, len(cookies))
	for i, c := range cookies {
		parts[i] = c.Name + "=" + c.Value
	}
	return strings.Join(parts, "; ")
}

func readBody(r io.Reader, contentLength, maxBodyBytes int64) ([]byte, error) {
	if contentLength <= 0 || contentLength > maxBodyBytes {
		return io.ReadAll(r)
	}
	var buf bytes.Buffer
	buf.Grow(int(contentLength) + bytes.MinRead)
	_, err := buf.ReadFrom(r)
	return buf.Bytes(), err
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
