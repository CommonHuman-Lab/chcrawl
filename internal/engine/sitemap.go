package engine

import (
	"context"
	"net/url"
	"sync"

	"github.com/commonhuman-lab/chcrawl/internal/frontier"
	"github.com/commonhuman-lab/chcrawl/internal/normalize"
	"github.com/commonhuman-lab/chcrawl/internal/output"
	"github.com/commonhuman-lab/chcrawl/internal/sitemap"
)

// seedSitemap discovers the site's XML sitemap(s) and injects their <loc>
// URLs into the frontier as depth-0 seeds (DiscoveredVia: "sitemap"), so
// client-rendered sites get full route coverage even though their links are
// invisible to static HTML parsing.
//
// Runs BEFORE workers start (inside Run), so drain-detection can't race the
// injection and end the crawl before sitemap pages are ever queued. Scope
// and dedup are enforced per URL exactly like discovered children: a sitemap
// listing off-origin URLs has those entries dropped.
func (e *Engine) seedSitemap(ctx context.Context, pending *sync.WaitGroup) {
	origin := e.seedURL.Scheme + "://" + e.seedURL.Host
	locs, err := sitemap.Discover(ctx, e.sitemapFetcher, origin)
	if err != nil {
		_ = e.writer.WriteError(output.ErrorEvent{URL: origin, Stage: "sitemap", Error: err.Error()})
		return
	}
	for _, loc := range locs {
		// The crawl budget stops at MaxPages anyway; injecting more seeds
		// than that could block Push on the bounded frontier before any
		// worker exists to drain it. Stop at the budget.
		if e.cfg.MaxPages > 0 && e.stats.sitemapURLs.Load() >= int64(e.cfg.MaxPages) {
			break
		}
		u, err := url.Parse(loc.URL)
		if err != nil {
			continue
		}
		if !e.scope.InScope(u, e.seedURL) {
			continue
		}
		norm := normalize.FromParsed(u, e.cfg.Canonicalization, e.cfg.SortQueryParams)
		if !e.dedup.MarkIfNew(norm) {
			e.stats.duplicatesRejected.Add(1)
			continue
		}
		e.stats.urlsUnique.Add(1)
		e.stats.urlsInScope.Add(1)
		e.stats.sitemapURLs.Add(1)
		e.enqueue(ctx, pending, frontier.Item{
			URL:           norm,
			ParentURL:     e.cfg.SeedURL,
			Depth:         0,
			DiscoveredVia: "sitemap",
		})
	}
}
