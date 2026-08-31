//go:build headless_integration

// Excluded from `go test ./...` — launches a real Chromium (downloading one
// on first run if none is cached). Run explicitly:
//   go test -tags=headless_integration ./internal/headless/...

package headless

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/commonhuman-lab/chcrawl/internal/fetch"
)

func TestFetch_RendersJSInjectedContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><div id="root"></div>
			<script>document.getElementById('root').innerHTML = '<a href="/found">x</a>';</script>
			</body></html>`))
	}))
	defer srv.Close()

	plain, err := fetch.New(fetch.Config{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	f, err := New(Config{Timeout: 15 * time.Second, PoolSize: 1, Inner: plain})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer f.Close()

	resp, err := f.Fetch(context.Background(), fetch.Request{URL: srv.URL + "/", Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resp.Body), `href="/found"`) {
		t.Fatalf("rendered body missing JS-injected link, got: %s", resp.Body)
	}
}
