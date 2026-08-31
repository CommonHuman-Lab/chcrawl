// Package sitemap discovers and parses XML sitemaps for seed enrichment.
//
// When a site publishes a sitemap (linked from robots.txt or at the
// conventional /sitemap.xml location), its <loc> URLs are injected into the
// crawl frontier as additional seeds. This gives crawlers full route
// coverage on client-rendered SPAs where links are invisible to static
// HTML parsing.
package sitemap

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"strings"

	"github.com/commonhuman-lab/chcrawl/internal/fetch"
)

const maxSitemapBytes = 8 * 1024 * 1024 // 8 MiB
const maxSitemapDocs = 50               // sitemap index children to expand

// Location is one URL entry from a sitemap.
type Location struct {
	URL string
}

// sitemapDoc handles both <urlset> (url entries) and <sitemapindex> (nested
// sitemap refs) with a single struct. Whether a document is an index is
// decided by its root element name (XMLName), not by which child slice
// ended up populated — a malformed document containing both <url> and
// <sitemap> children must not have its <url> entries silently dropped.
type sitemapDoc struct {
	XMLName xml.Name
	URLs    []sitemapEntry `xml:"url"`
	Smaps   []sitemapEntry `xml:"sitemap"`
}

type sitemapEntry struct {
	Loc string `xml:"loc"`
}

// Fetcher is the subset of fetch.Fetcher this package needs.
type Fetcher interface {
	Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error)
}

// Discover finds the site's sitemap(s) and returns their <loc> URLs.
//
// Discovery order:
//  1. Sitemap: directives in robots.txt (authoritative if present)
//  2. /sitemap.xml at the origin
//
// A sitemap index is expanded one level (up to maxSitemapDocs children).
// limit caps the number of locations returned (0 = unbounded); once it's
// reached, no further sitemap documents are fetched. This keeps the caller
// from paying for (and blocking on) documents whose entries it will just
// discard.
// Returns (nil, nil) when no sitemap exists — that is not an error.
func Discover(ctx context.Context, f Fetcher, origin string, limit int) ([]Location, error) {
	origin = strings.TrimRight(origin, "/")

	// 1. robots.txt Sitemap: directives
	sitemapURLs := fromRobots(ctx, f, origin)

	// 2. conventional location
	if len(sitemapURLs) == 0 {
		sitemapURLs = []string{origin + "/sitemap.xml"}
	}

	var locs []Location
	seen := map[string]bool{}
	queue := append([]string(nil), sitemapURLs...)

	for i := 0; i < len(queue) && i < maxSitemapDocs+1; i++ {
		if limit > 0 && len(locs) >= limit {
			break
		}
		smURL := queue[i]
		if seen[smURL] {
			continue
		}
		seen[smURL] = true

		body, isIndex, err := fetchSitemap(ctx, f, smURL)
		if err != nil {
			continue // probe failure is not fatal — keep trying the rest
		}
		if isIndex {
			queue = append(queue, body...)
			continue
		}
		for _, u := range body {
			locs = append(locs, Location{URL: u})
		}
	}
	return locs, nil
}

// fetchSitemap parses one sitemap document. Returns (locs, isIndex, err).
func fetchSitemap(ctx context.Context, f Fetcher, smURL string) ([]string, bool, error) {
	resp, err := f.Fetch(ctx, fetch.Request{URL: smURL, Method: "GET"})
	if err != nil {
		return nil, false, err
	}
	if resp.StatusCode >= 400 {
		return nil, false, fmt.Errorf("sitemap %s: status %d", smURL, resp.StatusCode)
	}
	body := resp.Body
	if len(body) > maxSitemapBytes {
		return nil, false, fmt.Errorf("sitemap %s: exceeds %d byte limit", smURL, maxSitemapBytes)
	}
	var doc sitemapDoc
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, false, fmt.Errorf("sitemap %s: parse: %w", smURL, err)
	}
	isIndex := strings.EqualFold(doc.XMLName.Local, "sitemapindex")
	var out []string
	raw := doc.URLs
	if isIndex {
		raw = doc.Smaps
	}
	for _, entry := range raw {
		loc := strings.TrimSpace(entry.Loc)
		if loc == "" {
			continue
		}
		u, err := url.Parse(loc)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		out = append(out, loc)
	}
	return out, isIndex, nil
}

// fromRobots reads robots.txt and extracts Sitemap: directives.
func fromRobots(ctx context.Context, f Fetcher, origin string) []string {
	resp, err := f.Fetch(ctx, fetch.Request{URL: origin + "/robots.txt", Method: "GET"})
	if err != nil || resp.StatusCode >= 400 {
		return nil
	}
	body := strings.TrimPrefix(string(resp.Body), string(rune(0xFEFF))) // strip UTF-8 BOM if present
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if len(line) >= 9 && strings.EqualFold(line[:8], "sitemap:") {
			sm := strings.TrimSpace(line[8:])
			if strings.HasPrefix(sm, "http://") || strings.HasPrefix(sm, "https://") {
				out = append(out, sm)
			}
		}
	}
	return out
}
