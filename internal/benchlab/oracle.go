package benchlab

import (
	"net/url"
	"strings"
)

// Oracle is the expected-correct outcome of crawling a Site, computed by
// walking the same PageSpec graph used to build the HTTP target — so the
// target and the oracle can never drift apart, the way a hand-written
// expected-count comment easily could. Field names and semantics mirror
// output.SummaryEvent so a runner can compare them directly.
type Oracle struct {
	PagesVisited       int
	Forms              int
	Params             int
	Endpoints          int
	JSFiles            int
	JSRoutes           int
	RequestsMade       int
	ResponsesOK        int
	ResponsesFailed    int
	RedirectsFollowed  int
	DuplicatesRejected int
}

type key struct{ host, path string }

// maxRedirectHops mirrors the engine's default MaxRedirects.
const maxRedirectHops = 20

// pathOnly strips a query string, matching how a real HTTP server routes
// by path alone — "/search?q=a" and "/search?q=b" are different frontier
// identities (dedup is by full normalized URL) but the same routed page.
func pathOnly(p string) string {
	if i := strings.IndexByte(p, '?'); i != -1 {
		return p[:i]
	}
	return p
}

// resolveRedirects follows p.Redirect chains (within one host, matching
// how the synthetic site's links are structured) up to maxRedirectHops,
// returning the terminal PageSpec and hop count, or ok=false on a loop or
// an unresolvable target — the same outcome a real too-many-redirects
// error produces in the engine.
func lookup(pages map[key]PageSpec, k key) (PageSpec, bool) {
	p, ok := pages[key{host: k.host, path: pathOnly(k.path)}]
	return p, ok
}

func resolveRedirects(pages map[key]PageSpec, start key) (final PageSpec, hops int, ok bool) {
	seen := map[key]bool{}
	cur := start
	for hops = 0; hops <= maxRedirectHops; hops++ {
		p, exists := lookup(pages, cur)
		if !exists {
			return PageSpec{}, hops, false
		}
		if p.Redirect == "" {
			return p, hops, true
		}
		if seen[cur] {
			return PageSpec{}, hops, false
		}
		seen[cur] = true
		cur = key{host: cur.host, path: p.Redirect}
	}
	return PageSpec{}, hops, false
}

// Compute walks the site's link graph the same way the real engine's
// pipeline does: enqueue-time dedup, scope checked before depth, a page's
// own content only explored if its resolved final status is < 400 and it
// was reached within maxDepth.
func (s *Site) Compute(maxDepth int, sameOrigin bool) Oracle {
	pages := map[key]PageSpec{}
	for _, p := range s.Pages {
		pages[key{host: p.Host, path: p.Path}] = p
	}

	hosts := s.hostsOrDefault()
	seedHost := hosts[0]
	seed := key{host: seedHost, path: s.Seed}

	var o Oracle
	visited := map[key]bool{seed: true}
	type frontierItem struct {
		k     key
		depth int
	}
	queue := []frontierItem{{k: seed, depth: 0}}

	inScope := func(k key) bool {
		if !sameOrigin {
			return true
		}
		return k.host == seedHost
	}

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		final, hops, ok := resolveRedirects(pages, item.k)
		o.RequestsMade++
		if !ok {
			o.ResponsesFailed++
			continue
		}
		status := final.Status
		if status == 0 {
			status = 200
		}
		if status >= 400 {
			o.ResponsesFailed++
			continue
		}
		o.ResponsesOK++
		o.RedirectsFollowed += hops
		// Mirrors pipeline.go: the query-param count comes from the
		// originally requested URL (item.k.path), not the redirect-resolved
		// final URL.
		if i := strings.IndexByte(item.k.path, '?'); i != -1 {
			if q, err := url.ParseQuery(item.k.path[i+1:]); err == nil {
				o.Params += len(q)
			}
		}

		var jsRouteTargets []string
		if final.JSEndpoints != nil {
			o.JSFiles++
			for _, ep := range final.JSEndpoints {
				jsRouteTargets = append(jsRouteTargets, jsRouteVariants(ep.Path)...)
			}
			o.JSRoutes += len(jsRouteTargets)
			o.Endpoints += len(jsRouteTargets)
		} else if !final.BadContentType {
			o.PagesVisited++
			o.Forms += len(final.Forms)
			for _, f := range final.Forms {
				o.Params += len(f.Fields)
			}
		}

		if item.depth >= maxDepth {
			continue
		}

		var children []string
		children = append(children, final.Links...)
		children = append(children, final.ScriptSrcs...)
		for _, f := range final.Forms {
			children = append(children, f.Action)
		}
		// JS-mined endpoints (base + any synthetic probe variant) are
		// followed as GET targets too, exactly like the real engine's
		// pipeline (every Discovery.Kind == "link", regardless of which
		// extractor found it, goes through the same enqueue path).
		children = append(children, jsRouteTargets...)

		for _, link := range children {
			childKey := parseLinkKey(link, item.k.host)
			if !inScope(childKey) {
				continue
			}
			if visited[childKey] {
				o.DuplicatesRejected++
				continue
			}
			visited[childKey] = true
			queue = append(queue, frontierItem{k: childKey, depth: item.depth + 1})
		}
	}

	return o
}

// jsRouteVariants mirrors JSEndpointExtractor's static-path regex: a
// matched path with no query string that ends in a purely alphabetic
// segment also gets a synthetic ".../1" path-param probe variant emitted
// alongside it.
func jsRouteVariants(path string) []string {
	variants := []string{path}
	if strings.Contains(path, "?") {
		return variants
	}
	segs := strings.Split(strings.TrimRight(path, "/"), "/")
	if len(segs) == 0 {
		return variants
	}
	last := segs[len(segs)-1]
	if last != "" && trailingAlpha(last) {
		variants = append(variants, path+"/1")
	}
	return variants
}

func trailingAlpha(s string) bool {
	for _, r := range s {
		if r < 'A' || (r > 'Z' && r < 'a') || r > 'z' {
			return false
		}
	}
	return true
}

// parseLinkKey mirrors resolveLink's "hostlabel:/path" convention for
// cross-host references.
func parseLinkKey(link, selfHost string) key {
	for i := 0; i < len(link); i++ {
		if link[i] == ':' {
			return key{host: link[:i], path: link[i+1:]}
		}
		if link[i] == '/' {
			break
		}
	}
	return key{host: selfHost, path: link}
}
