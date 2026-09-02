package benchlab

import (
	"bytes"
	"context"
	"net/url"
	"strconv"
	"testing"

	"github.com/commonhuman-lab/chcrawl/config"
	"github.com/commonhuman-lab/chcrawl/extract"
	"github.com/commonhuman-lab/chcrawl/fetch"
	"github.com/commonhuman-lab/chcrawl/internal/dedup"
	"github.com/commonhuman-lab/chcrawl/internal/frontier"
	"github.com/commonhuman-lab/chcrawl/internal/normalize"
	"golang.org/x/net/html"
)

// Diagnostic-only benchmarks, each isolating one crawl-pipeline stage with
// real production code, no HTTP/scheduling/oracle. Helps attribute
// S1-wide-flat's cost across categories; never a primary perf number. Run:
//
//	go test ./internal/benchlab -bench BenchmarkDiag -benchtime=5x -run '^$'

// genLinksHTML renders the same HTML a page with n links produces in a real
// crawl, via renderHTML, so this parses the same markup S1-wide-flat does.
func genLinksHTML(n int) []byte {
	root := PageSpec{Path: "/"}
	root.Links = make([]string, n)
	for i := 0; i < n; i++ {
		root.Links[i] = "/leaf/" + strconv.Itoa(i)
	}
	return []byte(renderHTML(root, nil, ""))
}

func diagnosticRegistry() *extract.Registry {
	return extract.NewRegistry(
		extract.LinkExtractor{},
		extract.FormExtractor{},
		extract.JSEndpointExtractor{},
		extract.WebSocketExtractor{},
	)
}

func benchDiagParseExtract(b *testing.B, n int) {
	body := genLinksHTML(n)
	resp := &fetch.Response{Body: body, ContentType: "text/html", FinalURL: "http://127.0.0.1/"}
	base, err := url.Parse("http://127.0.0.1/")
	if err != nil {
		b.Fatal(err)
	}
	registry := diagnosticRegistry()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		doc, err := html.Parse(bytes.NewReader(resp.Body))
		if err != nil {
			b.Fatal(err)
		}
		discoveries, errs := registry.RunAll(context.Background(), extract.Input{Resp: resp, BaseURL: base, Doc: doc})
		if len(errs) > 0 {
			b.Fatal(errs)
		}
		if len(discoveries) != n {
			b.Fatalf("extracted %d discoveries, want %d", len(discoveries), n)
		}
	}
}

// BenchmarkDiagParseExtract10k/100k isolate HTML parsing + link extraction
// cost alone, at S1-wide-flat's 10k/100k link counts.
func BenchmarkDiagParseExtract10k(b *testing.B)  { benchDiagParseExtract(b, 10_000) }
func BenchmarkDiagParseExtract100k(b *testing.B) { benchDiagParseExtract(b, 100_000) }

// benchDiagDedup isolates normalize.URL + dedup.VisitedSet cost for n
// distinct URLs, single-goroutine.
func benchDiagDedup(b *testing.B, n int) {
	urls := make([]string, n)
	for i := range urls {
		urls[i] = "http://127.0.0.1/leaf/" + strconv.Itoa(i)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		set := dedup.New()
		for _, u := range urls {
			norm := normalize.URL(u, config.StrictMode, false)
			set.MarkIfNew(norm)
		}
		if set.Len() != n {
			b.Fatalf("Len() = %d, want %d", set.Len(), n)
		}
	}
}

func BenchmarkDiagDedup10k(b *testing.B)  { benchDiagDedup(b, 10_000) }
func BenchmarkDiagDedup100k(b *testing.B) { benchDiagDedup(b, 100_000) }

// benchDiagFrontierPushPop isolates pure frontier.Frontier channel
// throughput for n items, sequential push then pop — a floor on how much
// wall time the frontier itself could plausibly account for.
func benchDiagFrontierPushPop(b *testing.B, n int) {
	ctx := context.Background()
	items := make([]frontier.Item, n)
	for i := range items {
		items[i] = frontier.Item{URL: "http://127.0.0.1/leaf/" + strconv.Itoa(i), Depth: 1}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f := frontier.New(n + 1)
		for _, item := range items {
			if err := f.Push(ctx, item); err != nil {
				b.Fatal(err)
			}
		}
		for j := 0; j < n; j++ {
			if _, ok, err := f.Pop(ctx); err != nil || !ok {
				b.Fatalf("Pop %d: ok=%v err=%v", j, ok, err)
			}
		}
		f.Close()
	}
}

func BenchmarkDiagFrontierPushPop10k(b *testing.B)  { benchDiagFrontierPushPop(b, 10_000) }
func BenchmarkDiagFrontierPushPop100k(b *testing.B) { benchDiagFrontierPushPop(b, 100_000) }
