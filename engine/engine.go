// Package engine wires the frontier, fetcher, extractors, scope/dedup policy, and output writer into a running crawl.
package engine

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"sync"
	"time"

	"github.com/commonhuman-lab/chcrawl/config"
	"github.com/commonhuman-lab/chcrawl/extract"
	"github.com/commonhuman-lab/chcrawl/fetch"
	"github.com/commonhuman-lab/chcrawl/internal/dedup"
	"github.com/commonhuman-lab/chcrawl/internal/frontier"
	"github.com/commonhuman-lab/chcrawl/internal/headless"
	"github.com/commonhuman-lab/chcrawl/internal/normalize"
	"github.com/commonhuman-lab/chcrawl/internal/robots"
	"github.com/commonhuman-lab/chcrawl/internal/scope"
	"github.com/commonhuman-lab/chcrawl/output"
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
	closer         io.Closer       // nil unless cfg.RenderJS (closes the headless browser)
}

// newAuxFetcher builds an unrestricted fetcher (no content-type allowlist) for a side-channel
// probe (robots.txt, OpenAPI, sitemaps, source maps). purpose only names the wrapped error.
func newAuxFetcher(cfg *config.Options, purpose string) (fetch.Fetcher, error) {
	f, err := fetch.New(fetch.Config{
		Timeout:            cfg.Timeout,
		Proxy:              cfg.Proxy,
		Headers:            cfg.Headers,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		MaxBodyBytes:       cfg.MaxBodyBytes,
		MaxRedirects:       cfg.MaxRedirects,
	})
	if err != nil {
		return nil, fmt.Errorf("engine: building %s fetcher: %w", purpose, err)
	}
	return f, nil
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

	var closer io.Closer
	if cfg.RenderJS {
		hf, err := headless.New(headless.Config{
			Timeout:            cfg.RenderTimeout,
			PoolSize:           cfg.RenderConcurrency,
			Inner:              fetcher,
			Headers:            cfg.Headers,
			Cookies:            cfg.Cookies,
			Proxy:              cfg.Proxy,
			InsecureSkipVerify: cfg.InsecureSkipVerify,
		})
		if err != nil {
			return nil, fmt.Errorf("engine: building headless fetcher: %w", err)
		}
		fetcher = hf
		closer = hf
	}

	policies := []scope.Policy{}
	switch {
	case cfg.SameOrigin && cfg.IncludeSubdomains:
		// Hostname() strips the port; SubdomainScope.InScope only strips it off the candidate host.
		policies = append(policies, scope.SubdomainScope{RootDomain: seedURL.Hostname()})
	case cfg.SameOrigin:
		policies = append(policies, scope.ExactOriginScope{})
	}
	if len(cfg.AllowedDomains) > 0 || len(cfg.DeniedDomains) > 0 {
		policies = append(policies, scope.AllowDenyScope{Allow: cfg.AllowedDomains, Deny: cfg.DeniedDomains})
	}
	if len(cfg.ExcludePatterns) > 0 || len(cfg.IncludePatterns) > 0 {
		policies = append(policies, scope.RegexScope{Include: cfg.IncludePatterns, Exclude: cfg.ExcludePatterns})
	}
	scopePolicy := scope.Policy(scope.CompositeScope{Policies: policies})

	extractors := []extract.Extractor{
		extract.LinkExtractor{},
		extract.FormExtractor{},
		extract.JSEndpointExtractor{},
		extract.WebSocketExtractor{},
	}
	if cfg.RecoverSourceMaps {
		// .js.map files often serve with an uncommon or missing content-type, which the crawl
		// fetcher's allowlist would otherwise silently discard.
		sourceMapFetcher, err := newAuxFetcher(cfg, "source-map")
		if err != nil {
			return nil, err
		}
		extractors = append(extractors, extract.SourceMapExtractor{Fetcher: sourceMapFetcher})
	}
	registry := extract.NewRegistry(extractors...)

	var robotsChecker *robots.Checker
	if cfg.RespectRobotsTxt {
		// robots.txt is text/plain, which the crawl fetcher's allowlist would otherwise discard.
		robotsFetcher, err := newAuxFetcher(cfg, "robots.txt")
		if err != nil {
			return nil, err
		}
		robotsChecker = robots.New(robotsFetcher, "*")
	}

	var openapiFetcher fetch.Fetcher
	if cfg.DiscoverOpenAPI {
		// OpenAPI specs are commonly served as YAML, which doesn't match the crawl fetcher's allowlist.
		openapiFetcher, err = newAuxFetcher(cfg, "OpenAPI discovery")
		if err != nil {
			return nil, err
		}
	}

	var sitemapFetcher fetch.Fetcher
	if cfg.DiscoverSitemap {
		// Sitemap XML isn't guaranteed to match the crawl fetcher's content-type allowlist either.
		sitemapFetcher, err = newAuxFetcher(cfg, "sitemap discovery")
		if err != nil {
			return nil, err
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
		closer:         closer,
		openapiFetcher: openapiFetcher,
		sitemapFetcher: sitemapFetcher,
	}, nil
}

// Run drives the crawl to completion (frontier drained, a budget hit, or ctx cancelled) and returns the final summary.
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

	// Must run before workers start: injected items join the initial pending set, so drain can't
	// close while sitemap URLs are still being discovered.
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

	// Uses callerCtx, not the derived ctx: an internal budget trigger shouldn't skip this bonus
	// step too — only genuine caller cancellation (e.g. Ctrl-C) should.
	if e.cfg.DiscoverOpenAPI && callerCtx.Err() == nil {
		e.discoverOpenAPI(callerCtx)
	}

	summary := e.stats.Snapshot(e.cfg.SeedURL, start, partial)
	if err := e.writer.WriteSummary(summary); err != nil {
		return &summary, err
	}
	return &summary, nil
}

// enqueue registers pending work for item and pushes it to the frontier; if the push fails, the
// pending count is corrected immediately so termination detection can't hang on an unqueued item.
func (e *Engine) enqueue(ctx context.Context, pending *sync.WaitGroup, item frontier.Item) {
	pending.Add(1)
	if err := e.frontier.Push(ctx, item); err != nil {
		pending.Done()
	}
}

// Close releases engine resources (currently just the headless browser when RenderJS was used) —
// no-op otherwise. Callers should defer this after a successful New().
func (e *Engine) Close() error {
	if e.closer == nil {
		return nil
	}
	return e.closer.Close()
}
