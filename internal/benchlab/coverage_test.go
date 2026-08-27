package benchlab

import "testing"

func TestDiscoverablePaths_LinksScriptsForms(t *testing.T) {
	site := &Site{
		Name: "t",
		Seed: "/",
		Pages: []PageSpec{
			{Path: "/", Links: []string{"/about"}, ScriptSrcs: []string{"/app.js"},
				Forms: []FormSpec{{Action: "/submit", Method: "post", Fields: []string{"x"}}}},
			{Path: "/about"},
			{Path: "/app.js"},
			{Path: "/submit"},
			{Path: "/unreachable"},
		},
	}
	got := site.DiscoverablePaths(10, true)
	want := map[string]bool{"/": true, "/about": true, "/app.js": true, "/submit": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for p := range want {
		if !got[p] {
			t.Errorf("expected %q to be discoverable", p)
		}
	}
	if got["/unreachable"] {
		t.Error("/unreachable should not be discoverable — nothing links to it")
	}
}

func TestDiscoverablePaths_QueryStringsCollapseToSamePath(t *testing.T) {
	site := &Site{
		Name: "t",
		Seed: "/",
		Pages: []PageSpec{
			{Path: "/", Links: []string{"/search?q=a", "/search?q=b"}},
			{Path: "/search"},
		},
	}
	got := site.DiscoverablePaths(10, true)
	if len(got) != 2 || !got["/"] || !got["/search"] {
		t.Fatalf("expected {/, /search}, got %v", got)
	}
}

func TestDiscoverablePaths_RedirectResolved(t *testing.T) {
	site := &Site{
		Name: "t",
		Seed: "/",
		Pages: []PageSpec{
			{Path: "/", Links: []string{"/old"}},
			{Path: "/old", Redirect: "/new"},
			{Path: "/new", Links: []string{"/child"}},
			{Path: "/child"},
		},
	}
	got := site.DiscoverablePaths(10, true)
	// The redirect source path itself is still a real, fetchable URL.
	for _, p := range []string{"/", "/old", "/child"} {
		if !got[p] {
			t.Errorf("expected %q to be discoverable, got %v", p, got)
		}
	}
}

func TestDiscoverablePaths_RedirectLoopDoesNotHang(t *testing.T) {
	site := &Site{
		Name: "t",
		Seed: "/",
		Pages: []PageSpec{
			{Path: "/", Links: []string{"/loop-a"}},
			{Path: "/loop-a", Redirect: "/loop-b"},
			{Path: "/loop-b", Redirect: "/loop-a"},
		},
	}
	got := site.DiscoverablePaths(10, true)
	if got["/loop-a"] || got["/loop-b"] {
		t.Errorf("a genuine redirect loop should resolve to nothing, got %v", got)
	}
}

func TestDiscoverablePaths_RespectsMaxDepth(t *testing.T) {
	site := &Site{
		Name: "t",
		Seed: "/",
		Pages: []PageSpec{
			{Path: "/", Links: []string{"/d1"}},
			{Path: "/d1", Links: []string{"/d2"}},
			{Path: "/d2"},
		},
	}
	got := site.DiscoverablePaths(1, true)
	if !got["/"] || !got["/d1"] {
		t.Errorf("expected / and /d1 within depth 1, got %v", got)
	}
	if got["/d2"] {
		t.Errorf("/d2 is beyond max depth 1, got %v", got)
	}
}
