package engine

import (
	"bytes"
	"context"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/commonhuman-lab/chcrawl/internal/extract"
	"github.com/commonhuman-lab/chcrawl/internal/fetch"
	"github.com/commonhuman-lab/chcrawl/internal/frontier"
	"github.com/commonhuman-lab/chcrawl/internal/normalize"
	"github.com/commonhuman-lab/chcrawl/internal/output"
	"golang.org/x/net/html"
)

func (e *Engine) worker(ctx context.Context, cancel context.CancelFunc, pending, workers *sync.WaitGroup) {
	defer workers.Done()
	for {
		item, ok, err := e.frontier.Pop(ctx)
		if err != nil || !ok {
			return
		}
		e.process(ctx, cancel, pending, item)
		pending.Done()
	}
}

func (e *Engine) process(ctx context.Context, cancel context.CancelFunc, pending *sync.WaitGroup, item frontier.Item) {
	target, err := url.Parse(item.URL)
	if err != nil {
		return
	}

	if e.robots != nil && !e.robots.Allowed(ctx, target) {
		e.stats.robotsDisallowed.Add(1)
		_ = e.writer.WriteError(output.ErrorEvent{URL: item.URL, Stage: "robots", Error: "disallowed by robots.txt"})
		return
	}

	if err := e.hosts.Acquire(ctx, target.Host); err != nil {
		return
	}
	releaseHost := sync.OnceFunc(func() { e.hosts.Release(target.Host) })
	defer releaseHost()

	e.stats.requestsMade.Add(1)
	resp, err := e.fetcher.Fetch(ctx, fetch.Request{URL: item.URL, Method: "GET"})
	if err != nil {
		e.stats.responsesFailed.Add(1)
		_ = e.writer.WriteError(output.ErrorEvent{URL: item.URL, Stage: "fetch", Error: err.Error()})
		return
	}

	e.stats.redirectsFollowed.Add(int64(len(resp.RedirectChain)))
	if resp.RetryAttempts > 0 {
		e.stats.retryAttempts.Add(int64(resp.RetryAttempts))
		e.stats.retryBackoffNS.Add(int64(resp.RetryDelay))
	}

	if resp.StatusCode >= 400 {
		e.stats.responsesFailed.Add(1)
		_ = e.writer.WriteError(output.ErrorEvent{
			URL:           item.URL,
			Stage:         "fetch",
			Error:         "http status " + strconv.Itoa(resp.StatusCode),
			RetryAttempts: resp.RetryAttempts,
			RetryDelayMS:  resp.RetryDelay.Milliseconds(),
		})
		return
	}
	e.stats.responsesOK.Add(1)
	if strings.Contains(strings.ToLower(resp.ContentType), "javascript") {
		e.stats.jsFiles.Add(1)
	}

	releaseHost()

	if !e.admitPageBudget() {
		cancel()
		return
	}

	discoveries, _ := e.extractDiscoveries(ctx, resp, target)

	e.stats.params.Add(int64(len(target.Query())))

	for _, d := range discoveries {
		e.recordDiscoveryStats(d)
		if d.URL == "" {
			continue
		}
		e.stats.urlsDiscovered.Add(1)
		e.maybeEnqueueChild(ctx, pending, item, d.URL, d.Kind)
	}

	evt := output.PageEvent{
		Timestamp:     time.Now(),
		URL:           item.URL,
		FinalURL:      resp.FinalURL,
		Depth:         item.Depth,
		Status:        resp.StatusCode,
		ContentType:   resp.ContentType,
		BytesRead:     len(resp.Body),
		Truncated:     resp.Truncated,
		RedirectChain: resp.RedirectChain,
		Discoveries:   discoveries,
		FetchMS:       resp.FetchDuration.Milliseconds(),
		RetryAttempts: resp.RetryAttempts,
		RetryDelayMS:  resp.RetryDelay.Milliseconds(),
	}
	_ = e.writer.WritePage(evt)
}

// admitPageBudget atomically checks the MaxPages hard cap. It returns false
// once the budget is exhausted, in which case the caller must trigger
// engine-wide cancellation immediately rather than waiting for any
// in-flight work to finish, so the cap can't be overshot.
func (e *Engine) admitPageBudget() bool {
	n := e.stats.pagesInBudget.Add(1)
	return n <= int64(e.cfg.MaxPages)
}

func (e *Engine) extractDiscoveries(ctx context.Context, resp *fetch.Response, requested *url.URL) ([]extract.Discovery, *html.Node) {
	finalURL, err := url.Parse(resp.FinalURL)
	if err != nil {
		finalURL = requested
	}

	var doc *html.Node
	if strings.Contains(strings.ToLower(resp.ContentType), "html") && len(resp.Body) > 0 {
		doc, _ = html.Parse(bytes.NewReader(resp.Body))
	}

	baseURL := finalURL
	if doc != nil {
		if href, ok := extract.FindBaseHref(doc); ok {
			if resolved, err := finalURL.Parse(href); err == nil && strings.EqualFold(resolved.Host, finalURL.Host) {
				baseURL = resolved
			}
		}
	}

	in := extract.Input{Resp: resp, BaseURL: baseURL, Doc: doc}
	discoveries, errs := e.registry.RunAll(ctx, in)
	for _, err := range errs {
		_ = e.writer.WriteError(output.ErrorEvent{URL: requested.String(), Stage: "extract", Error: err.Error()})
	}
	return discoveries, doc
}

// recordDiscoveryStats counts things *found on a page* (endpoints, forms,
// JS-derived routes). Counting actual JS files fetched happens separately
// in process(), keyed off the response's real Content-Type rather than how
// the file was discovered — a page can link to a .js file via a plain
// <a href>, not just <script src>, and that should still count.
func (e *Engine) recordDiscoveryStats(d extract.Discovery) {
	switch d.Kind {
	case "form":
		e.stats.forms.Add(1)
		e.stats.params.Add(int64(len(d.Params)))
	case "code_path":
		e.stats.endpoints.Add(1)
	case "link":
		if d.Meta["source"] == "js_endpoint" {
			e.stats.endpoints.Add(1)
			e.stats.jsRoutes.Add(1)
		}
	case "source_map":
		if n, err := strconv.Atoi(d.Meta["sources_recovered"]); err == nil {
			e.stats.sourceMapsRecovered.Add(int64(n))
		}
	}
}

func (e *Engine) maybeEnqueueChild(ctx context.Context, pending *sync.WaitGroup, parent frontier.Item, childURL, via string) {
	if parent.Depth >= e.cfg.MaxDepth {
		return
	}
	u, err := url.Parse(childURL)
	if err != nil {
		return
	}
	if !e.scope.InScope(u, e.seedURL) {
		return
	}
	norm := normalize.FromParsed(u, e.cfg.Canonicalization, e.cfg.SortQueryParams)
	if !e.dedup.MarkIfNew(norm) {
		e.stats.duplicatesRejected.Add(1)
		return
	}
	e.stats.urlsUnique.Add(1)
	e.stats.urlsInScope.Add(1)
	e.enqueue(ctx, pending, frontier.Item{
		URL:           norm,
		ParentURL:     parent.URL,
		Depth:         parent.Depth + 1,
		DiscoveredVia: via,
	})
}
