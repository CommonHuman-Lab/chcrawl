package benchlab

import "strconv"

// Workloads returns every named benchmark workload. Every one of them is
// entirely self-contained and served from 127.0.0.1-only local HTTP
// servers started fresh per run — nothing here ever touches the network
// or depends on external state, so results are reproducible run to run.
func Workloads() map[string]*Site {
	return map[string]*Site{
		"w1-small-static":        w1SmallStatic(),
		"w2-deep-tree":           w2DeepTree(),
		"w3-wide-site":           w3WideSite(),
		"w4-redirect-heavy":      w4RedirectHeavy(),
		"w5-js-discovery":        w5JSDiscovery(),
		"w6-duplicate-hell":      w6DuplicateHell(),
		"w7-large-responses":     w7LargeResponses(),
		"w8-parameter-discovery": w8ParameterDiscovery(),
		"w9-multi-host-scope":    w9MultiHostScope(),
		"w10-chaos":              w10Chaos(),
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
	// A 6-hop redirect chain ending in real content.
	for i := 0; i < 6; i++ {
		next := "/r" + strconv.Itoa(i+1)
		if i == 5 {
			next = "/final"
		}
		pages = append(pages, PageSpec{Path: "/r" + strconv.Itoa(i), Redirect: next})
	}
	pages = append(pages, PageSpec{Path: "/final", Links: []string{"/final/child"}})
	pages = append(pages, PageSpec{Path: "/final/child"})
	// A genuine redirect loop: must fail (too-many-redirects), never hang.
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
