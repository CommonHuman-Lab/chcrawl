package extract

import (
	"context"
	"net/url"
	"testing"

	"github.com/commonhuman-lab/chcrawl/fetch"
)

func extractJS(t *testing.T, src, base string, ex Extractor) []Discovery {
	t.Helper()
	baseURL, err := url.Parse(base)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	resp := &fetch.Response{ContentType: "application/javascript", Body: []byte(src)}
	got, err := ex.Extract(context.Background(), Input{Resp: resp, BaseURL: baseURL})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return got
}

func TestJSEndpointExtractor_MethodTemplateLiteral(t *testing.T) {
	src := "this.http.get(`${this.host}/rest/products/search?q=${term}`);"
	got := extractJS(t, src, "https://shop.example.com/app.js", JSEndpointExtractor{})
	if len(got) != 1 {
		t.Fatalf("expected 1 discovery, got %d: %+v", len(got), got)
	}
	want := "https://shop.example.com/rest/products/search?q=test"
	if got[0].URL != want || got[0].Method != "GET" {
		t.Errorf("got url=%q method=%q, want url=%q method=GET", got[0].URL, got[0].Method, want)
	}
}

func TestJSEndpointExtractor_StaticPathWithMethodInference(t *testing.T) {
	src := `axios.delete("/api/items/1"); fetch("/rest/orders");`
	got := extractJS(t, src, "https://example.com/app.js", JSEndpointExtractor{})

	byURL := map[string]Discovery{}
	for _, d := range got {
		byURL[d.URL] = d
	}
	if d, ok := byURL["https://example.com/api/items/1"]; !ok || d.Method != "DELETE" {
		t.Errorf("expected DELETE inferred for /api/items/1, got %+v", d)
	}
	if _, ok := byURL["https://example.com/rest/orders"]; !ok {
		t.Errorf("expected /rest/orders discovered")
	}
	// "/rest/orders" ends in an alphabetic segment with no query string,
	// so a synthetic .../1 path-param probe variant should also appear.
	if _, ok := byURL["https://example.com/rest/orders/1"]; !ok {
		t.Errorf("expected synthetic /rest/orders/1 probe variant")
	}
}

func TestJSEndpointExtractor_IgnoresPathsWithDots(t *testing.T) {
	// The static-path regex's character class has no ".", so a path
	// containing one (a real file extension, or anything else) simply
	// never matches — this is an incidental side effect of the regex,
	// not an explicit extension denylist.
	src := `fetch("/api/bundle.js"); fetch("/api/styles.css");`
	got := extractJS(t, src, "https://example.com/app.js", JSEndpointExtractor{})
	if len(got) != 0 {
		t.Errorf("expected no matches for dotted paths, got %+v", got)
	}
}

func TestJSEndpointExtractor_IndirectVariableConcatenation(t *testing.T) {
	src := `this.host = base + "/rest/user"; this.http.post(this.host + "/login", body);`
	got := extractJS(t, src, "https://example.com/app.js", JSEndpointExtractor{})
	found := false
	for _, d := range got {
		if d.URL == "https://example.com/rest/user/login" && d.Method == "POST" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected POST https://example.com/rest/user/login via indirect concat, got %+v", got)
	}
}

func TestJSEndpointExtractor_WebpackChunkDiscovery(t *testing.T) {
	src := `import("./chunk-a1b2c3.js");`
	got := extractJS(t, src, "https://example.com/static/app.js", JSEndpointExtractor{})
	found := false
	for _, d := range got {
		if d.URL == "https://example.com/static/chunk-a1b2c3.js" && d.Meta["asset"] == "js" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected chunk-a1b2c3.js resolved relative to the bundle's own URL, got %+v", got)
	}
}
