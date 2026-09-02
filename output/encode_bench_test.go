package output

import (
	"io"
	"testing"
	"time"

	"github.com/commonhuman-lab/chcrawl/extract"
	"github.com/commonhuman-lab/chcrawl/fetch"
)

func representativePageEvent() PageEvent {
	return PageEvent{
		Timestamp:   time.Now(),
		URL:         "https://example.com/some/realistic/path?query=1&page=2",
		FinalURL:    "https://example.com/some/realistic/path?query=1&page=2",
		Depth:       3,
		Status:      200,
		ContentType: "text/html; charset=utf-8",
		BytesRead:   48213,
		FetchMS:     142,
		RedirectChain: []fetch.RedirectHop{
			{URL: "https://example.com/", StatusCode: 301},
		},
		Discoveries: []extract.Discovery{
			{Kind: "link", URL: "https://example.com/about"},
			{Kind: "link", URL: "https://example.com/contact"},
			{Kind: "form", URL: "https://example.com/search", Method: "GET",
				Params: []extract.Param{{Name: "q", Value: ""}}},
			{Kind: "form", URL: "https://example.com/login", Method: "POST",
				Params: []extract.Param{{Name: "user", Value: ""}, {Name: "pass", Value: ""}},
				Base:   map[string]string{"csrf_token": "abc123"},
				Meta:   map[string]string{"form_id": "login-form"}},
			{Kind: "code_path", URL: "/api/v1/users/{id}"},
		},
	}
}
func BenchmarkWritePage(b *testing.B) {
	w := NewWriter(io.Discard)
	evt := representativePageEvent()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := w.WritePage(evt); err != nil {
			b.Fatal(err)
		}
	}
}
