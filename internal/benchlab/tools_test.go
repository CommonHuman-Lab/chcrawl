package benchlab

import "testing"

func TestParseChcrawl(t *testing.T) {
	out := []byte(`{"type":"page","url":"http://127.0.0.1:1/","depth":0}
{"type":"error","url":"http://127.0.0.1:1/broken","stage":"fetch","error":"boom"}
{"type":"page","url":"http://127.0.0.1:1/about","depth":1}
{"type":"summary","seed":"http://127.0.0.1:1/"}
`)
	got := parseChcrawl("http://127.0.0.1:1/", out)
	want := map[string]bool{"http://127.0.0.1:1/": true, "http://127.0.0.1:1/about": true}
	assertURLSet(t, got, want)
}

func TestParseKatana(t *testing.T) {
	out := []byte(`{"timestamp":"x","request":{"method":"GET","endpoint":"http://127.0.0.1:1/"}}
{"timestamp":"x","request":{"method":"GET","endpoint":"http://127.0.0.1:1/about.html"}}
not json, ignored
`)
	got := parseKatana("http://127.0.0.1:1/", out)
	want := map[string]bool{"http://127.0.0.1:1/": true, "http://127.0.0.1:1/about.html": true}
	assertURLSet(t, got, want)
}

func TestParseHakrawler(t *testing.T) {
	out := []byte(`{"Source":"href","URL":"http://127.0.0.1:1/about.html"}
{"Source":"script","URL":"http://127.0.0.1:1/app.js"}
`)
	got := parseHakrawler("http://127.0.0.1:1/", out)
	want := map[string]bool{
		"http://127.0.0.1:1/":           true, // seed always counted, hakrawler never re-emits it
		"http://127.0.0.1:1/about.html": true,
		"http://127.0.0.1:1/app.js":     true,
	}
	assertURLSet(t, got, want)
}

func TestParseGospider(t *testing.T) {
	out := []byte(`[url] - [code-200] - http://127.0.0.1:1/
[form] - http://127.0.0.1:1/
[javascript] - http://127.0.0.1:1/app.js
[url] - [code-200] - http://127.0.0.1:1/about.html
[linkfinder] - [from: http://127.0.0.1:1/app.js] - /api/users/1
`)
	got := parseGospider("http://127.0.0.1:1/", out)
	want := map[string]bool{
		"http://127.0.0.1:1/":            true,
		"http://127.0.0.1:1/app.js":      true,
		"http://127.0.0.1:1/about.html":  true,
		"http://127.0.0.1:1/api/users/1": true,
	}
	assertURLSet(t, got, want)
}

func TestParseGospider_RelativeLinkfinderPathResolvedAgainstBase(t *testing.T) {
	out := []byte(`[linkfinder] - [from: http://127.0.0.1:1/deep/app.js] - /rest/orders
`)
	got := parseGospider("http://127.0.0.1:1/", out)
	if !got["http://127.0.0.1:1/rest/orders"] {
		t.Errorf("expected relative linkfinder path resolved against seed origin, got %v", got)
	}
}

func assertURLSet(t *testing.T, got, want map[string]bool) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for u := range want {
		if !got[u] {
			t.Errorf("missing expected URL %q in %v", u, got)
		}
	}
}
