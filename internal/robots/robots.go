// Package robots implements an opt-in robots.txt policy gate: an explicit,
// off-by-default policy appropriate for a pentest tool, but available for
// callers who want it.
package robots

import (
	"context"
	"net/url"
	"strings"
	"sync"

	"github.com/commonhuman-lab/chcrawl/internal/fetch"
)

type rule struct {
	path  string
	allow bool
}

type ruleset struct {
	rules []rule
}

// allowed applies the standard longest-prefix-match algorithm, with ties
// broken in favor of Allow (matching Google's documented robots.txt
// interpretation).
func (r *ruleset) allowed(path string) bool {
	bestLen := -1
	bestAllow := true
	for _, ru := range r.rules {
		if !strings.HasPrefix(path, ru.path) {
			continue
		}
		l := len(ru.path)
		if l > bestLen || (l == bestLen && ru.allow && !bestAllow) {
			bestLen = l
			bestAllow = ru.allow
		}
	}
	return bestAllow
}

type hostEntry struct {
	once   sync.Once
	groups map[string]*ruleset
}

// Checker fetches and caches robots.txt once per host, then answers
// Allowed() queries against the cached rules.
type Checker struct {
	fetcher   fetch.Fetcher
	userAgent string

	mu    sync.Mutex
	hosts map[string]*hostEntry
}

// New builds a Checker. fetcher should be configured to accept text/plain
// bodies (robots.txt is not HTML/JS/JSON/XML, so the crawl fetcher's
// content-type allowlist would otherwise discard it).
func New(fetcher fetch.Fetcher, userAgent string) *Checker {
	if userAgent == "" {
		userAgent = "*"
	}
	return &Checker{fetcher: fetcher, userAgent: strings.ToLower(userAgent), hosts: map[string]*hostEntry{}}
}

// Allowed reports whether target may be fetched per the host's robots.txt.
// A fetch failure or missing robots.txt is treated as "allow everything"
// (the conventional interpretation).
func (c *Checker) Allowed(ctx context.Context, target *url.URL) bool {
	entry := c.entryFor(target.Host)
	entry.once.Do(func() {
		entry.groups = c.fetchAndParse(ctx, target.Scheme, target.Host)
	})

	path := target.Path
	if path == "" {
		path = "/"
	}
	rs := selectGroup(entry.groups, c.userAgent)
	if rs == nil {
		return true
	}
	return rs.allowed(path)
}

func (c *Checker) entryFor(host string) *hostEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.hosts[host]
	if !ok {
		e = &hostEntry{}
		c.hosts[host] = e
	}
	return e
}

func (c *Checker) fetchAndParse(ctx context.Context, scheme, host string) map[string]*ruleset {
	resp, err := c.fetcher.Fetch(ctx, fetch.Request{URL: scheme + "://" + host + "/robots.txt", Method: "GET"})
	if err != nil || resp.StatusCode >= 400 {
		return nil
	}
	return parse(string(resp.Body))
}

func selectGroup(groups map[string]*ruleset, userAgent string) *ruleset {
	if groups == nil {
		return nil
	}
	if rs, ok := groups[userAgent]; ok {
		return rs
	}
	return groups["*"]
}

// parse implements the subset of the robots.txt format relevant to
// crawling: User-agent groups with Allow/Disallow directives. Sitemap and
// Crawl-delay directives are ignored.
func parse(body string) map[string]*ruleset {
	groups := map[string]*ruleset{}
	var currentAgents []string
	groupOpen := false

	for _, line := range strings.Split(body, "\n") {
		if i := strings.IndexByte(line, '#'); i != -1 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])

		switch key {
		case "user-agent":
			agent := strings.ToLower(val)
			if groupOpen {
				currentAgents = []string{agent}
				groupOpen = false
			} else {
				currentAgents = append(currentAgents, agent)
			}
			if _, ok := groups[agent]; !ok {
				groups[agent] = &ruleset{}
			}
		case "allow", "disallow":
			groupOpen = true
			if val == "" {
				continue // empty Disallow means "allow everything"; no rule needed
			}
			for _, a := range currentAgents {
				groups[a].rules = append(groups[a].rules, rule{path: val, allow: key == "allow"})
			}
		}
	}
	return groups
}
