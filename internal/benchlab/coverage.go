package benchlab

// DiscoverablePaths returns the set of URL paths a well-behaved crawler
// could reach from the seed by following links, script srcs, and form
// actions (redirect-resolved), within maxDepth and in scope — the ground
// truth external tools' coverage is scored against.
//
// Deduped by path only (query-string dedup conventions differ across
// crawlers). Only single-host sites are supported — multi-host scope
// semantics are too tool-specific to score fairly.
func (s *Site) DiscoverablePaths(maxDepth int, sameOrigin bool) map[string]bool {
	pages := map[key]PageSpec{}
	for _, p := range s.Pages {
		pages[key{host: p.Host, path: p.Path}] = p
	}

	hosts := s.hostsOrDefault()
	seedHost := hosts[0]
	seed := key{host: seedHost, path: s.Seed}

	found := map[string]bool{}
	visited := map[key]bool{seed: true}
	type item struct {
		k     key
		depth int
	}
	queue := []item{{k: seed, depth: 0}}

	inScope := func(k key) bool {
		if !sameOrigin {
			return true
		}
		return k.host == seedHost
	}

	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]

		final, _, ok := resolveRedirects(pages, it.k)
		if !ok {
			continue
		}
		status := final.Status
		if status == 0 {
			status = 200
		}
		if status >= 400 {
			continue
		}
		found[pathOnly(it.k.path)] = true

		if it.depth >= maxDepth {
			continue
		}

		var children []string
		children = append(children, final.Links...)
		children = append(children, final.ScriptSrcs...)
		for _, f := range final.Forms {
			children = append(children, f.Action)
		}

		for _, link := range children {
			childKey := parseLinkKey(link, it.k.host)
			if !inScope(childKey) {
				continue
			}
			if visited[childKey] {
				continue
			}
			visited[childKey] = true
			queue = append(queue, item{k: childKey, depth: it.depth + 1})
		}
	}

	return found
}
