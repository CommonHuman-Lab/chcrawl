// Package engine wires the frontier, fetcher, extractors, scope/dedup
// policy, and output writer into a running crawl.
package engine

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/commonhuman-lab/chcrawl/internal/config"
	"github.com/commonhuman-lab/chcrawl/internal/dedup"
	"github.com/commonhuman-lab/chcrawl/internal/extract"
	"github.com/commonhuman-lab/chcrawl/internal/fetch"
	"github.com/commonhuman-lab/chcrawl/internal/frontier"
	"github.com/commonhuman-lab/chcrawl/internal/normalize"
	"github.com/commonhuman-lab/chcrawl/internal/output"
	"github.com/commonhuman-lab/chcrawl/internal/robots"
	"github.com/commonhuman-lab/chcrawl/internal/scope"
)

type Engine struct {
	cfg            *config.Options
	seedURL        *url.URL
	frontier       frontier.Frontier
	fetcher        fetch.Fetcher
	scope          scope.Policy
	dedup          dedup.VisitedSet
	registry       *extract.Registry
	writer         output.EventWriter
	stats          *Stats
	hosts          *hostLimiter
	robots         *robots.Checker // nil unless cfg.RespectRobotsTxt
	openapiFetcher fetch.Fetcher   // nil unless cfg.DiscoverOpenAPI
	sitemapFetcher fetch.Fetcher   // nil unless cfg.DiscoverSitemap
}

// New builds an Engine ready to run a single crawl of cfg.SeedURL.
func New(cfg *config.Options, writer output.EventWriter) (*Engine, error) {
	seedURL, err := url.Parse(cfg.SeedURL)
	if err != nil {
		return nil, fmt.Errorf("engine: invalid seed URL: %w", err)
	}

	fetcher, err := fetch.New(fetch.Config{
		Timeout:             cfg.Timeout,
		Proxy:               cfg.Proxy,
		Headers:             cfg.Headers,
		Cookies:             cfg.Cookies,
		InsecureSkipVerify:  cfg.InsecureSkipVerify,
		MaxBodyBytes:        cfg.MaxBodyBytes,
		MaxRedirects:        cfg.MaxRedirects,
		AllowedContentTypes: cfg.AllowedContentTypes,
		RetryPolicy:         cfg.RetryPolicy,
		Delay:               cfg.Delay,
	})
	if err != nil {
		return nil, fmt.Errorf("engine: building fetcher: %w", err)
	}

	policies := []scope.Policy{}
	if cfg.SameOrigin {
		policies = append(policies, scope.ExactOriginScope{})
	}
	if len(cfg.ExcludePatterns) > 0 {
		policies = append(policies, scope.RegexScope{Exclude: cfg.ExcludePatterns})
	}
	scopePolicy := scope.Policy(scope.CompositeScope{Policies: policies})

	extractors := []extract.Extractor{
		extract.LinkExtractor{},
		extract.FormExtractor{},
		extract.JSEndpointExtractor{},
		extract.WebSocketExtractor{},
	}
	if cfg.RecoverSourceMaps {
		// .js.map files are commonly served with an uncommon or missing
		// content-type (application/octet-stream, or nothing at all), which
		// the crawl fetcher's content-type allowlist would otherwise
		// silently discard.
		sourceMapFetcher, err := fetch.New(fetch.Config{
			Timeout:            cfg.Timeout,
			Proxy:              cfg.Proxy,
			Headers:            cfg.Headers,
			InsecureSkipVerify: cfg.InsecureSkipVerify,
			MaxBodyBytes:       cfg.MaxBodyBytes,
			MaxRedirects:       cfg.MaxRedirects,
		})
		if err != nil {
			return nil, fmt.Errorf("engine: building source-map fetcher: %w", err)
		}
		extractors = append(extractors, extract.SourceMapExtractor{Fetcher: sourceMapFetcher})
	}
	registry := extract.NewRegistry(extractors...)

	var robotsChecker *robots.Checker
	if cfg.RespectRobotsTxt {
		// robots.txt is text/plain, which the crawl fetcher's content-type
		// allowlist would otherwise discard, so it gets its own fetcher
		// with an unrestricted allowlist.
		robotsFetcher, err := fetch.New(fetch.Config{
			Timeout:            cfg.Timeout,
			Proxy:              cfg.Proxy,
			Headers:            cfg.Headers,
			InsecureSkipVerify: cfg.InsecureSkipVerify,
			MaxBodyBytes:       cfg.MaxBodyBytes,
			MaxRedirects:       cfg.MaxRedirects,
		})
		if err != nil {
			return nil, fmt.Errorf("engine: building robots.txt fetcher: %w", err)
		}
		robotsChecker = robots.New(robotsFetcher, "*")
	}

	var openapiFetcher fetch.Fetcher
	if cfg.DiscoverOpenAPI {
		// OpenAPI specs are commonly served as YAML (text/yaml,
		// application/x-yaml, or even text/plain), none of which match the
		// crawl fetcher's default content-type allowlist.
		openapiFetcher, err = fetch.New(fetch.Config{
			Timeout:            cfg.Timeout,
			Proxy:              cfg.Proxy,
			Headers:            cfg.Headers,
			InsecureSkipVerify: cfg.InsecureSkipVerify,
			MaxBodyBytes:       cfg.MaxBodyBytes,
			MaxRedirects:       cfg.MaxRedirects,
		})
		if err != nil {
			return nil, fmt.Errorf("engine: building OpenAPI discovery fetcher: %w", err)
		}
	}

	var sitemapFetcher fetch.Fetcher
	if cfg.DiscoverSitemap {
		// Sitemap XML is commonly served as application/xml or text/xml,
		// and robots.txt as text/plain — neither guaranteed to match the
		// crawl fetcher's content-type allowlist, so it gets its own
		// unrestricted fetcher (mirrors the robots/openapi pattern).
		sitemapFetcher, err = fetch.New(fetch.Config{
			Timeout:            cfg.Timeout,
			Proxy:              cfg.Proxy,
			Headers:            cfg.Headers,
			InsecureSkipVerify: cfg.InsecureSkipVerify,
			MaxBodyBytes:       cfg.MaxBodyBytes,
			MaxRedirects:       cfg.MaxRedirects,
		})
		if err != nil {
			return nil, fmt.Errorf("engine: building sitemap discovery fetcher: %w", err)
		}
	}

	return &Engine{
		cfg:            cfg,
		seedURL:        seedURL,
		frontier:       frontier.New(cfg.MaxFrontierSize),
		fetcher:        fetcher,
		scope:          scopePolicy,
		dedup:          dedup.New(),
		registry:       registry,
		writer:         writer,
		stats:          &Stats{},
		hosts:          newHostLimiter(cfg.PerHostConcurrency),
		robots:         robotsChecker,
		openapiFetcher: openapiFetcher,
		sitemapFetcher: sitemapFetcher,
	}, nil
}

// Run drives the crawl to completion (frontier drained, MaxPages hit,
// MaxDuration elapsed, or ctx cancelled by the caller) and returns the
// final summary.
func (e *Engine) Run(callerCtx context.Context) (*output.SummaryEvent, error) {
	start := time.Now()
	ctx, cancel := context.WithCancel(callerCtx)
	defer cancel()

	if e.cfg.MaxDuration > 0 {
		timer := time.AfterFunc(e.cfg.MaxDuration, cancel)
		defer timer.Stop()
	}

	var pending sync.WaitGroup
	var workers sync.WaitGroup

	seedKey := normalize.URL(e.cfg.SeedURL, e.cfg.Canonicalization, e.cfg.SortQueryParams)
	e.dedup.MarkIfNew(seedKey)
	e.stats.urlsUnique.Add(1)
	e.stats.urlsInScope.Add(1)
	e.enqueue(ctx, &pending, frontier.Item{URL: seedKey, Depth: 0, DiscoveredVia: "seed"})

	// Sitemap seeding runs before workers start: injected items are part of
	// the initial pending set, so the drained-channel can't close while
	// sitemap URLs are still in flight from discovery.
	if e.cfg.DiscoverSitemap && e.sitemapFetcher != nil {
		e.seedSitemap(ctx, &pending)
	}

	for i := 0; i < e.cfg.Concurrency; i++ {
		workers.Add(1)
		go e.worker(ctx, cancel, &pending, &workers)
	}

	drained := make(chan struct{})
	go func() {
		pending.Wait()
		close(drained)
	}()

	partial := false
	select {
	case <-drained:
	case <-ctx.Done():
		partial = true
	}

	cancel()
	workers.Wait()
	e.frontier.Close()

	// Uses callerCtx, not the crawl's own (possibly already-cancelled-by-
	// MaxPages/MaxDuration) derived ctx — an internal budget trigger
	// shouldn't also skip this bonus discovery step, only genuine caller
	// cancellation (e.g. Ctrl-C) should.
	if e.cfg.DiscoverOpenAPI && callerCtx.Err() == nil {
		e.discoverOpenAPI(callerCtx)
	}

	summary := e.stats.Snapshot(e.cfg.SeedURL, start, partial)
	if err := e.writer.WriteSummary(summary); err != nil {
		return &summary, err
	}
	return &summary, nil
}

// enqueue registers pending work for item and pushes it to the frontier. If
// the push fails (frontier full past ctx deadline, or shutting down), the
// pending count is corrected immediately so termination detection can't
// hang waiting for an item that was never actually queued.
func (e *Engine) enqueue(ctx context.Context, pending *sync.WaitGroup, item frontier.Item) {
	pending.Add(1)
	if err := e.frontier.Push(ctx, item); err != nil {
		pending.Done()
	}
}
