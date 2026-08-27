package extract

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/commonhuman-lab/chcrawl/internal/fetch"
	"golang.org/x/net/html"
)

func parseAndExtract(t *testing.T, body, base string, ex Extractor) []Discovery {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("html.Parse: %v", err)
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	resp := &fetch.Response{ContentType: "text/html"}
	discoveries, err := ex.Extract(context.Background(), Input{Resp: resp, BaseURL: baseURL, Doc: doc})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return discoveries
}

func TestLinkExtractor_SkipsJavascriptMailtoAndFragmentHrefs(t *testing.T) {
	body := `<html><body>
		<a href="javascript:alert(1)">js</a>
		<a href="mailto:a@b.com">mail</a>
		<a href="#section">frag</a>
		<a href="/real">real</a>
	</body></html>`
	got := parseAndExtract(t, body, "https://example.com/", LinkExtractor{})
	if len(got) != 1 || got[0].URL != "https://example.com/real" {
		t.Errorf("expected only /real to be extracted, got %+v", got)
	}
}

func TestLinkExtractor_CodeBlockPathHeuristic(t *testing.T) {
	body := `<html><body>
		<code>/rest/basket/:bid</code>
		<code>/api/Products/{id}</code>
		<code>just some prose, not a path</code>
		<code>GET /a/b</code>
	</body></html>`
	got := parseAndExtract(t, body, "https://example.com/", LinkExtractor{})

	var codePaths []string
	for _, d := range got {
		if d.Kind == "code_path" {
			codePaths = append(codePaths, d.URL)
		}
	}
	sort.Strings(codePaths)
	want := []string{"https://example.com/api/Products/{id}", "https://example.com/rest/basket/:bid"}
	sort.Strings(want)
	if len(codePaths) != len(want) {
		t.Fatalf("expected %d code_path discoveries, got %d: %v", len(want), len(codePaths), codePaths)
	}
	for i := range want {
		if codePaths[i] != want[i] {
			t.Errorf("code_path[%d] = %q, want %q", i, codePaths[i], want[i])
		}
	}
}

func TestLinkExtractor_DataAttributesSkipTemplateBindings(t *testing.T) {
	body := `<html><body>
		<div data-href="/settings">a</div>
		<div data-url="{{dynamic}}">b</div>
	</body></html>`
	got := parseAndExtract(t, body, "https://example.com/", LinkExtractor{})
	if len(got) != 1 || got[0].URL != "https://example.com/settings" {
		t.Errorf("expected only /settings via data-href, got %+v", got)
	}
}

func TestLinkExtractor_BaseHrefOverridesResolutionWhenSameOrigin(t *testing.T) {
	body := `<html><head><base href="https://example.com/nested/"></head>
		<body><a href="child">c</a></body></html>`
	doc, _ := html.Parse(strings.NewReader(body))
	href, ok := FindBaseHref(doc)
	if !ok || href != "https://example.com/nested/" {
		t.Fatalf("FindBaseHref = %q, %v", href, ok)
	}
}
