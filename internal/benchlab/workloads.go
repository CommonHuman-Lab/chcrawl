package benchlab

import (
	"strconv"
	"strings"
)

// Workloads returns every named benchmark workload. Every one of them is
// entirely self-contained and served from 127.0.0.1-only local HTTP
// servers started fresh per run — nothing here ever touches the network
// or depends on external state, so results are reproducible run to run.
func Workloads() map[string]*Site {
	return map[string]*Site{
		"w1-small-static":          w1SmallStatic(),
		"w2-deep-tree":             w2DeepTree(),
		"w3-wide-site":             w3WideSite(),
		"w4-redirect-heavy":        w4RedirectHeavy(),
		"w5-js-discovery":          w5JSDiscovery(),
		"w6-duplicate-hell":        w6DuplicateHell(),
		"w7-large-responses":       w7LargeResponses(),
		"w8-parameter-discovery":   w8ParameterDiscovery(),
		"w9-multi-host-scope":      w9MultiHostScope(),
		"w10-chaos":                w10Chaos(),
		"w11-single-page":          w11SinglePage(),
		"w12-branching-graph":      w12BranchingGraph(),
		"w13-url-normalization":    w13URLNormalization(),
		"w14-http-status-variety":  w14HTTPStatusVariety(),
		"w15-markup-edge-cases":    w15MarkupEdgeCases(),
		"w16-intermittent-failure": w16IntermittentFailure(),
		"w17-medium-scale":         w17MediumScale(),
		"w18-uneven-latency":       w18UnevenLatency(),
		"w19-duplication-stress":   w19DuplicationStress(),
		"w20-multi-host-scale":     w20MultiHostScale(),
	}
}

func w1SmallStatic() *Site {
	return &Site{
		Name: "w1-small-static",
		Seed: "/",
		Pages: []PageSpec{
			{Path: "/", Links: []string{"/about", "/contact"}},
			{Path: "/about", Links: []string{"/team"}},
			{Path: "/team"},
			{Path: "/contact", Forms: []FormSpec{{Action: "/contact/submit", Method: "post", Fields: []string{"name", "email", "message"}}}},
			{Path: "/contact/submit"},
		},
	}
}

func w2DeepTree() *Site {
	const depth = 25
	pages := chain("/d/", depth)
	pages[0].Path = "/" // seed at root, rest of the chain unchanged
	return &Site{Name: "w2-deep-tree", Seed: "/", Pages: pages}
}

func w3WideSite() *Site {
	return &Site{Name: "w3-wide-site", Seed: "/", Pages: fanout("/", "/leaf/", 150)}
}

func w4RedirectHeavy() *Site {
	pages := []PageSpec{
		{Path: "/", Links: []string{"/r0", "/loop-a"}},
	}
	for i := 0; i < 6; i++ {
		next := "/r" + strconv.Itoa(i+1)
		if i == 5 {
			next = "/final"
		}
		pages = append(pages, PageSpec{Path: "/r" + strconv.Itoa(i), Redirect: next})
	}
	pages = append(pages, PageSpec{Path: "/final", Links: []string{"/final/child"}})
	pages = append(pages, PageSpec{Path: "/final/child"})
	// Genuine redirect loop: must fail (too-many-redirects), never hang.
	pages = append(pages, PageSpec{Path: "/loop-a", Redirect: "/loop-b"})
	pages = append(pages, PageSpec{Path: "/loop-b", Redirect: "/loop-a"})
	return &Site{Name: "w4-redirect-heavy", Seed: "/", Pages: pages}
}

func w5JSDiscovery() *Site {
	return &Site{
		Name: "w5-js-discovery",
		Seed: "/",
		Pages: []PageSpec{
			{Path: "/", ScriptSrcs: []string{"/app.js"}, Links: []string{"/chat"}},
			{
				Path: "/app.js",
				JSEndpoints: []JSEndpoint{
					{Method: "GET", Path: "/api/items/1"},  // resolves to a real page below
					{Method: "GET", Path: "/api/items/2"},  // does not exist -> fails
					{Method: "POST", Path: "/rest/orders"}, // does not exist -> fails
				},
			},
			{Path: "/api/items/1"},
			{Path: "/chat"},
		},
	}
}

func w6DuplicateHell() *Site {
	shared := []string{"/shared/a", "/shared/b", "/shared/c"}
	pages := []PageSpec{
		{Path: "/", Links: []string{"/p0", "/p1", "/p2", "/p3", "/p4"}},
	}
	for i := 0; i < 5; i++ {
		pages = append(pages, PageSpec{
			Path:  "/p" + strconv.Itoa(i),
			Links: append(append([]string{}, shared...), "/"), // every page also links back to root
		})
	}
	for _, p := range shared {
		pages = append(pages, PageSpec{Path: p})
	}
	return &Site{Name: "w6-duplicate-hell", Seed: "/", Pages: pages}
}

func w7LargeResponses() *Site {
	return &Site{
		Name: "w7-large-responses",
		Seed: "/",
		Pages: []PageSpec{
			{Path: "/", Links: []string{"/big", "/huge"}},
			{Path: "/big", PaddingKB: 512, Links: []string{"/leaf"}},
			{Path: "/huge", PaddingKB: 4096},
			{Path: "/leaf"},
		},
	}
}

func w8ParameterDiscovery() *Site {
	return &Site{
		Name: "w8-parameter-discovery",
		Seed: "/",
		Pages: []PageSpec{
			{
				Path: "/",
				Links: []string{
					"/search?q=alpha&sort=asc",
					"/search?q=beta&sort=desc",
					"/search?q=alpha&sort=asc", // exact duplicate of the first, same normalized key
				},
				Forms: []FormSpec{
					{Action: "/filter", Method: "get", Fields: []string{"category", "min_price", "max_price", "in_stock"}},
					{Action: "/subscribe", Method: "post", Fields: []string{"email"}},
				},
			},
			{Path: "/search", Forms: []FormSpec{{Action: "/search/advanced", Method: "get", Fields: []string{"q", "page", "limit"}}}},
			{Path: "/filter"},
			{Path: "/subscribe"},
			{Path: "/search/advanced"},
		},
	}
}

func w9MultiHostScope() *Site {
	return &Site{
		Name:  "w9-multi-host-scope",
		Seed:  "/",
		Hosts: []string{"a", "b"},
		Pages: []PageSpec{
			{Host: "a", Path: "/", Links: []string{"/local1", "b:/remote1"}},
			{Host: "a", Path: "/local1", Links: []string{"/local2"}},
			{Host: "a", Path: "/local2"},
			{Host: "b", Path: "/remote1", Links: []string{"/remote2"}},
			{Host: "b", Path: "/remote2"},
		},
	}
}

func w10Chaos() *Site {
	pages := []PageSpec{
		{Path: "/", Links: []string{"/loop-a", "/error", "/badtype", "/slow", "/deep0", "/deep0"}},
		{Path: "/loop-a", Redirect: "/loop-b"},
		{Path: "/loop-b", Redirect: "/loop-a"},
		{Path: "/error", Status: 500},
		{Path: "/badtype", BadContentType: true},
		{Path: "/slow", DelayMS: 50, Links: []string{"/slow/child"}},
		{Path: "/slow/child"},
	}
	deep := chain("/deep", 10)
	pages = append(pages, deep...)
	return &Site{Name: "w10-chaos", Seed: "/", Pages: pages}
}

// w11SinglePage tests seed-only termination: a seed with zero outbound links.
func w11SinglePage() *Site {
	return &Site{Name: "w11-single-page", Seed: "/", Pages: []PageSpec{{Path: "/"}}}
}

// w12BranchingGraph combines a multi-level branch, a cross-link from one
// branch into a page reachable from the other (must dedup), and a real
// multi-hop cycle (branch-b/2 -> cycle/x -> cycle/y -> branch-b/2), distinct
// from w6's simpler leaf-links-back-to-root loop.
func w12BranchingGraph() *Site {
	return &Site{
		Name: "w12-branching-graph",
		Seed: "/",
		Pages: []PageSpec{
			{Path: "/", Links: []string{"/branch-a", "/branch-b"}},
			{Path: "/branch-a", Links: []string{"/branch-a/1", "/branch-a/2"}},
			{Path: "/branch-a/1", Links: []string{"/branch-b/1"}}, // cross-link into the other branch
			{Path: "/branch-a/2"},
			{Path: "/branch-b", Links: []string{"/branch-b/1", "/branch-b/2"}},
			{Path: "/branch-b/1"},
			{Path: "/branch-b/2", Links: []string{"/cycle/x"}},
			{Path: "/cycle/x", Links: []string{"/cycle/y"}},
			{Path: "/cycle/y", Links: []string{"/branch-b/2"}}, // closes the cycle
		},
	}
}

// w13URLNormalization links to the same target page four different literal
// ways — trailing slash, percent-encoded segment, a fragment, and the
// canonical form — verifying normalize.URL and dedup collapse them to one
// page, not four.
func w13URLNormalization() *Site {
	return &Site{
		Name: "w13-url-normalization",
		Seed: "/",
		Pages: []PageSpec{
			{Path: "/", Links: []string{
				"/target",         // canonical form
				"/target/",        // trailing-slash variant
				"/tar%67et",       // percent-encoded variant (%67 decodes to 'g')
				"/target#section", // fragment variant
			}},
			{Path: "/target"},
		},
	}
}

// w14HTTPStatusVariety covers terminal-status breadth w10-chaos never
// exercises: 410/404 are NOT in retry.Default's RetryableStatus set, so they
// fail immediately with no backoff; 429 IS retryable but never recovers
// here, so it fails only after the full MaxRetries backoff cost. /empty and
// the two ContentTypeOverride pages test that a non-HTML/non-JS content type
// is skipped by every extractor's Applies() gate, not mis-parsed.
func w14HTTPStatusVariety() *Site {
	return &Site{
		Name: "w14-http-status-variety",
		Seed: "/",
		Pages: []PageSpec{
			{Path: "/", Links: []string{"/gone", "/missing", "/throttled", "/empty", "/plain-text", "/binary-content"}},
			{Path: "/gone", Status: 410},      // non-retryable: fails immediately
			{Path: "/missing", Status: 404},   // non-retryable: fails immediately
			{Path: "/throttled", Status: 429}, // retryable but never recovers: fails only after full backoff
			{Path: "/empty", Empty: true},
			{Path: "/plain-text", ContentTypeOverride: "text/plain", RawBody: "plain text, not HTML: /should-not-be-found"},
			{Path: "/binary-content", ContentTypeOverride: "application/octet-stream", RawBody: "\x00\x01\xFF\xFE"},
		},
	}
}

// nestedWrapper wraps inner in depth levels of <div> nesting, deep enough to
// sanity-check the recursive walk() functions in internal/extract.
func nestedWrapper(depth int, inner string) string {
	var b strings.Builder
	b.WriteString("<html><body>\n")
	for i := 0; i < depth; i++ {
		b.WriteString("<div>")
	}
	b.WriteString(inner)
	for i := 0; i < depth; i++ {
		b.WriteString("</div>")
	}
	b.WriteString("\n</body></html>")
	return b.String()
}

// w15MarkupEdgeCases exercises tag-soup recovery (unclosed tags, a stray
// </body> mid-document), deep nesting (a link 300 <div> levels deep), and
// data-attribute-only discovery (a page reachable ONLY via data-href) — none
// covered elsewhere in benchlab.
func w15MarkupEdgeCases() *Site {
	return &Site{
		Name: "w15-markup-edge-cases",
		Seed: "/",
		Pages: []PageSpec{
			{Path: "/", Links: []string{"/malformed", "/nested"}, DataLinks: []string{"/data-only"}},
			{
				Path:  "/malformed",
				Links: []string{"/recovered"}, // must match the <a href> actually embedded in RawBody below
				RawBody: "<html><body><p>unclosed paragraph<div>unclosed div" +
					`<a href="/recovered">link</a><span>stray span` +
					"<table><tr><td>broken table\n</body></html>",
			},
			{Path: "/recovered"},
			{Path: "/nested", Links: []string{"/deep-target"}, RawBody: nestedWrapper(300, `<a href="/deep-target">deep</a>`)},
			{Path: "/deep-target"},
			{Path: "/data-only", DataLinks: []string{"/only-via-data-attr"}},
			{Path: "/only-via-data-attr"},
		},
	}
}

// w16IntermittentFailure is the only workload verifying retry actually
// *recovers* a page: /flaky fails its first 2 requests (503) and succeeds on
// the 3rd, within retry.Default's MaxRetries=3, so a correctly-retrying
// crawler reaches its child; /permanently-down fails every request as a
// same-workload control that must end up ResponsesFailed.
func w16IntermittentFailure() *Site {
	return &Site{
		Name: "w16-intermittent-failure",
		Seed: "/",
		Pages: []PageSpec{
			{Path: "/", Links: []string{"/flaky", "/permanently-down"}},
			{Path: "/flaky", FailFirstNRequests: 2, FailStatus: 503, Links: []string{"/flaky/child"}},
			{Path: "/flaky/child"},
			{Path: "/permanently-down", Status: 503},
		},
	}
}

// w17MediumScale sits between w3-wide-site's 151 pages and the S1-S6
// chcrawl-only scale suite's 10k/100k — large enough to widen the 3-tool
// gap beyond what w3 shows, small enough that repeated external-process
// runs stay practical.
func w17MediumScale() *Site {
	return &Site{Name: "w17-medium-scale", Seed: "/", Pages: fanout("/", "/leaf/", 1000)}
}

// w18UnevenLatency tests whether a tool's concurrency model lets fast pages
// on one origin proceed independently of a handful of slow ones, or
// serializes behind them — a same-origin-scoped, tool-agnostic proxy for
// "one slow host among many fast hosts" that stays fair across all 3 tools
// (unlike a genuine cross-origin test, where each tool's differing
// subdomain/scope-follow semantics would confound the comparison — see
// w9-multi-host-scope's exclusion from -competitor-bench for the same
// reason). 3 endpoints sleep 300ms each; the other 27 respond immediately.
// A crawler whose concurrency serializes around any in-flight slow request
// finishes closer to 3*300ms; one that keeps fast requests flowing
// alongside the slow ones finishes much closer to the fast tail alone.
func w18UnevenLatency() *Site {
	const total = 30
	const slow = 3
	root := PageSpec{Path: "/"}
	pages := make([]PageSpec, 0, total+1)
	for i := 0; i < total; i++ {
		leaf := "/leaf/" + strconv.Itoa(i)
		root.Links = append(root.Links, leaf)
		p := PageSpec{Path: leaf}
		if i < slow {
			p.DelayMS = 300
		}
		pages = append(pages, p)
	}
	pages = append(pages, root)
	return &Site{Name: "w18-uneven-latency", Seed: "/", Pages: pages}
}

// w19DuplicationStress extends w13-url-normalization's idea (four textual
// variants of one URL) to real volume: 300 links across only 20 distinct
// target pages, cycling through 5 href patterns per target (canonical,
// trailing-slash, a distinct #fragment per occurrence, a fixed query
// string, and an empty query string), each repeated 3x. Fragments always
// collapse (normalize.FromParsed strips them unconditionally) and repeated
// identical hrefs always collapse (exact-duplicate discovery, the most
// common real-world dedup case); trailing-slash and query-bearing variants
// are deliberately real, separately-routed pages here (the server has no
// route for the trailing-slash form, so it 404s — the oracle expects
// exactly that, not a silent collapse), so this exercises both genuine
// normalization-driven dedup and a crawler's ability to still stay correct
// when a "variant" isn't actually the same resource.
func w19DuplicationStress() *Site {
	const distinctTargets = 20
	const variantsPerTarget = 15
	root := PageSpec{Path: "/"}
	pages := make([]PageSpec, 0, distinctTargets+1)
	for i := 0; i < distinctTargets; i++ {
		target := "/target" + strconv.Itoa(i)
		for v := 0; v < variantsPerTarget; v++ {
			root.Links = append(root.Links, duplicateVariant(target, v))
		}
		pages = append(pages, PageSpec{Path: target})
	}
	pages = append(pages, root)
	return &Site{Name: "w19-duplication-stress", Seed: "/", Pages: pages}
}

// duplicateVariant returns the v-th textual variant of target that a
// correct normalization pass collapses back to the same page.
func duplicateVariant(target string, v int) string {
	switch v % 5 {
	case 0:
		return target // canonical
	case 1:
		return target + "/" // trailing slash
	case 2:
		return target + "#frag" + strconv.Itoa(v) // fragment (always stripped)
	case 3:
		return target + "?a=1&b=2" // query present, order/canonicalization-sensitive
	default:
		return target + "?" // empty query string
	}
}

// w20MultiHostScale is a genuine cross-origin workload — chcrawl's own
// hostLimiter, dedup, and frontier are all keyed by full host:port, and
// every httptest.Server in this Site's 8 Hosts binds 127.0.0.1 on an
// independent port, so from the engine's perspective these are 8 real,
// distinct hosts, not a same-origin simulation. Named with "multi-host" so
// -competitor-bench excludes it (hakrawler/gospider's cross-origin/subdomain
// flag semantics differ enough from chcrawl's own that a 3-tool comparison
// here wouldn't be fair — same reasoning as w9-multi-host-scope's
// exclusion) and so the oracle/stats runners in cmd/chcrawl-bench
// automatically run it with SameOrigin=false, letting chcrawl actually
// cross hosts. This is a chcrawl-only measurement of whether its own
// concurrency model (in particular hostLimiter.mu, the one genuinely
// global — if cheap — lock touched on every Acquire/Release regardless of
// host.
func w20MultiHostScale() *Site {
	const numHosts = 8
	const leavesPerHost = 20
	hosts := make([]string, numHosts)
	for i := range hosts {
		hosts[i] = "h" + strconv.Itoa(i)
	}

	seedRoot := PageSpec{Host: hosts[0], Path: "/"}
	pages := make([]PageSpec, 0, 1+numHosts*(leavesPerHost+1))
	for _, h := range hosts {
		seedRoot.Links = append(seedRoot.Links, h+":/root")
	}
	pages = append(pages, seedRoot)

	for hi, h := range hosts {
		hostRoot := PageSpec{Host: h, Path: "/root"}
		for j := 0; j < leavesPerHost; j++ {
			leafPath := "/leaf/" + strconv.Itoa(j)
			hostRoot.Links = append(hostRoot.Links, leafPath)
			leaf := PageSpec{Host: h, Path: leafPath}
			if hi == 0 && j < 3 {
				leaf.DelayMS = 300
			}
			pages = append(pages, leaf)
		}
		pages = append(pages, hostRoot)
	}
	return &Site{Name: "w20-multi-host-scale", Seed: "/", Hosts: hosts, Pages: pages}
}
