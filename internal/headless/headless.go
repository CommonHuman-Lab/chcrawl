// Package headless provides a browser-backed fetch.Fetcher for JS-rendering
// crawls, opt-in via -render-js. Asset requests (JS/CSS/images/etc.) are
// delegated to a plain HTTP fetcher; only likely-HTML navigations spend a
// pooled browser tab.
package headless

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/commonhuman-lab/chcrawl/internal/fetch"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

type Config struct {
	Timeout            time.Duration // per-navigation budget
	PoolSize           int           // max concurrent browser tabs
	Inner              fetch.Fetcher // handles asset requests (required)
	Headers            map[string]string
	Cookies            string // raw "a=1; b=2" cookie header, sent as-is
	Proxy              string
	InsecureSkipVerify bool
}

type Fetcher struct {
	inner   fetch.Fetcher
	browser *rod.Browser
	pool    *pagePool
	timeout time.Duration
	headers []string
}

// New launches a browser (downloading a matching Chromium first if none is
// found on the system) and returns a Fetcher ready to use.
func New(cfg Config) (*Fetcher, error) {
	if cfg.Inner == nil {
		return nil, fmt.Errorf("headless: Config.Inner is required")
	}
	if cfg.PoolSize < 1 {
		cfg.PoolSize = 1
	}

	l := launcher.New().Headless(true)
	if cfg.Proxy != "" {
		l = l.Proxy(cfg.Proxy)
	}
	controlURL, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("headless: launching browser (needs outbound network on first run to download Chromium unless one is already cached): %w", err)
	}

	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("headless: connecting to browser: %w", err)
	}
	if cfg.InsecureSkipVerify {
		if err := browser.IgnoreCertErrors(true); err != nil {
			return nil, fmt.Errorf("headless: setting insecure TLS: %w", err)
		}
	}

	var headerPairs []string
	for k, v := range cfg.Headers {
		headerPairs = append(headerPairs, k, v)
	}
	if cfg.Cookies != "" {
		headerPairs = append(headerPairs, "Cookie", cfg.Cookies)
	}

	f := &Fetcher{
		inner:   cfg.Inner,
		browser: browser,
		timeout: cfg.Timeout,
		headers: headerPairs,
	}
	if f.timeout <= 0 {
		f.timeout = 25 * time.Second
	}
	f.pool = newPagePool(cfg.PoolSize,
		func(ctx context.Context) (*rod.Page, error) {
			return browser.Context(ctx).Page(proto.TargetCreateTarget{})
		},
		func(p *rod.Page) { _ = p.Close() },
	)
	return f, nil
}

func (f *Fetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	if isAsset(req.URL) {
		return f.inner.Fetch(ctx, req)
	}

	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	page, err := f.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("headless: acquiring page: %w", err)
	}
	defer f.pool.Release(page)
	page = page.Context(ctx)

	if len(f.headers) > 0 {
		if _, err := page.SetExtraHeaders(f.headers); err != nil {
			return nil, fmt.Errorf("headless: setting headers: %w", err)
		}
	}

	var status int
	respHeaders := http.Header{}
	wait := page.EachEvent(func(e *proto.NetworkResponseReceived) bool {
		if e.Type != proto.NetworkResourceTypeDocument || e.Response == nil {
			return false
		}
		status = e.Response.Status
		for k, v := range e.Response.Headers {
			respHeaders.Set(k, v.Str())
		}
		return true
	})

	start := time.Now()
	if err := page.Navigate(req.URL); err != nil {
		return nil, fmt.Errorf("headless: navigating: %w", err)
	}
	wait()
	_ = page.WaitLoad()
	_ = page.WaitStable(300 * time.Millisecond) // best-effort settle for late JS hydration

	body, err := page.HTML()
	if err != nil {
		return nil, fmt.Errorf("headless: reading rendered HTML: %w", err)
	}
	finalURL := req.URL
	if info, err := page.Info(); err == nil {
		finalURL = info.URL
	}
	if status == 0 {
		status = http.StatusOK // no Document response event observed (e.g. same-doc navigation); assume success
	}
	ct := respHeaders.Get("Content-Type")
	if ct == "" {
		ct = "text/html; charset=utf-8"
	}

	return &fetch.Response{
		RequestedURL:  req.URL,
		FinalURL:      finalURL,
		StatusCode:    status,
		Headers:       respHeaders,
		Body:          []byte(body),
		ContentType:   ct,
		FetchDuration: time.Since(start),
	}, nil
}

// Close shuts down the browser process. Safe to call once.
func (f *Fetcher) Close() error {
	return f.browser.Close()
}
