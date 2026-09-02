package headless

import (
	"context"
	"testing"

	"github.com/commonhuman-lab/chcrawl/fetch"
)

type fakeFetcher struct {
	calls []string
	resp  *fetch.Response
	err   error
}

func (f *fakeFetcher) Fetch(ctx context.Context, req fetch.Request) (*fetch.Response, error) {
	f.calls = append(f.calls, req.URL)
	return f.resp, f.err
}

func TestFetch_AssetsBypassBrowser(t *testing.T) {
	inner := &fakeFetcher{resp: &fetch.Response{StatusCode: 200, Body: []byte("body")}}
	f := &Fetcher{inner: inner} // no pool/browser set — must not be touched

	resp, err := f.Fetch(context.Background(), fetch.Request{URL: "https://x.example/app.js"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if len(inner.calls) != 1 || inner.calls[0] != "https://x.example/app.js" {
		t.Fatalf("inner.calls = %v, want one call to the .js URL", inner.calls)
	}
}
