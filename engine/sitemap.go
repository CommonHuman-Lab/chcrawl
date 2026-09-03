package engine

import (
	"context"
	"net/url"
	"sync"

	"github.com/commonhuman-lab/chcrawl/internal/frontier"
	"github.com/commonhuman-lab/chcrawl/internal/normalize"
	"github.com/commonhuman-lab/chcrawl/internal/sitemap"
	"github.com/commonhuman-lab/chcrawl/output"
)

// seedSitemap discovers the site's XML sitemap(s) and injects their <loc> URLs into the frontier
// as depth-0 seeds, giving client-rendered sites route coverage static HTML parsing would miss.
// Must run before workers start (see Run), or drain-detection could end the crawl too early.
func (e *Engine) seedSitemap(ctx context.Context, pending *sync.WaitGroup) {
	limit := e.cfg.MaxFrontierSize - 1
	if e.cfg.MaxPages > 0 && e.cfg.MaxPages < limit {
		limit = e.cfg.MaxPages
	}
	if limit <= 0 {
		return
	}

	origin := e.seedURL.Scheme + "://" + e.seedURL.Host
	locs, err := sitemap.Discover(ctx, e.sitemapFetcher, origin, limit)
	if err != nil {
		_ = e.writer.WriteError(output.ErrorEvent{URL: origin, Stage: "sitemap", Error: err.Error()})
		return
	}
	for _, loc := range locs {
		if e.stats.sitemapURLs.Load() >= int64(limit) {
			break
		}
		e.stats.urlsDiscovered.Add(1)
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
