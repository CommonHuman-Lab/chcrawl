package engine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/pprof"
	"strconv"
	"testing"
	"time"

	"github.com/commonhuman-lab/chcrawl/internal/config"
	"github.com/commonhuman-lab/chcrawl/internal/output"
)

func TestCrawl_DiscoversFormsDepthAndDedup(t *testing.T) {
	var mux http.ServeMux
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>
			<a href="/a">a</a>
			<a href="/b">b</a>
			<a href="http://external.invalid/x">external</a>
			<form action="/subscribe" method="post">
				<input type="hidden" name="csrf" value="tok123">
				<input type="text" name="email" required>
				<input type="submit" value="go">
			</form>
		</body></html>`))
	})
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><a href="/c">c</a><a href="/">home</a></body></html>`))
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body><a href="/c">c</a></body></html>`))
	})
	mux.HandleFunc("/c", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>leaf</body></html>`))
	})
	mux.HandleFunc("/subscribe", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>subscribed</body></html>`))
	})

	srv := httptest.NewServer(&mux)
	defer srv.Close()

	cfg, err := config.New(srv.URL+"/",
		config.WithConcurrency(4),
		config.WithPerHostConcurrency(2),
		config.WithMaxPages(20),
		config.WithMaxDepth(3),
		config.WithTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	rec := &recordingWriter{}
	eng, err := New(cfg, output.NewWriter(rec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	summary, err := eng.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if summary.RequestsMade < 5 {
		t.Errorf("expected at least 5 requests (/, /a, /b, /c, /subscribe), got %d", summary.RequestsMade)
	}
	if summary.ResponsesOK != summary.RequestsMade {
		t.Errorf("expected all requests to succeed: ok=%d made=%d", summary.ResponsesOK, summary.RequestsMade)
	}
	if summary.Forms != 1 {
		t.Errorf("expected 1 form discovered, got %d", summary.Forms)
	}
	// /c is reachable via both /a and /b — dedup must reject the second discovery.
	if summary.DuplicatesRejected < 1 {
		t.Errorf("expected at least 1 duplicate rejected (/c reached twice, and / reached again via /a), got %d", summary.DuplicatesRejected)
	}
}

func TestCrawl_MaxDepthStopsExpansion(t *testing.T) {
	var mux http.ServeMux
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<a href="/1">1</a>`))
	})
	mux.HandleFunc("/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<a href="/2">2</a>`))
	})
	mux.HandleFunc("/2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<a href="/3">3</a>`))
	})
	mux.HandleFunc("/3", func(w http.ResponseWriter, r *http.Request) {
		t.Error("/3 should never be fetched: it's beyond max depth 1 from seed")
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`ok`))
	})

	srv := httptest.NewServer(&mux)
	defer srv.Close()

	cfg, err := config.New(srv.URL+"/",
		config.WithConcurrency(2),
		config.WithPerHostConcurrency(2),
		config.WithMaxPages(20),
		config.WithMaxDepth(1),
		config.WithTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	rec := &recordingWriter{}
	eng, err := New(cfg, output.NewWriter(rec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	summary, err := eng.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// seed(depth0) -> /1(depth1) enqueued and fetched; /1's link to /2 is at
	// depth1 -> would be depth2 > maxDepth(1), so /2 must never be enqueued.
	if summary.RequestsMade != 2 {
		t.Errorf("expected exactly 2 requests (seed + /1), got %d", summary.RequestsMade)
	}
}

func TestCrawl_MaxPagesHardCap(t *testing.T) {
	handler := func(n int) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<a href="/p` + strconv.Itoa(n+1) + `">next</a>`))
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", handler(0))
	for i := 1; i <= 30; i++ {
		mux.HandleFunc("/p"+strconv.Itoa(i), handler(i))
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg, err := config.New(srv.URL+"/",
		config.WithConcurrency(3),
		config.WithPerHostConcurrency(3),
		config.WithMaxPages(5),
		config.WithMaxDepth(50),
		config.WithTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	rec := &recordingWriter{}
	eng, err := New(cfg, output.NewWriter(rec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	summary, err := eng.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if summary.ResponsesOK > 8 {
		t.Errorf("MaxPages=5 hard cap should stop the crawl close to the budget, got %d successful responses", summary.ResponsesOK)
	}
}

func TestRun_NoGoroutineLeakAfterMidCrawlCancellation(t *testing.T) {
	leakProfile := pprof.Lookup("goroutineleak")
	if leakProfile == nil {
		t.Skip("goroutineleak profile not available on this Go toolchain (added in Go 1.27)")
	}

	release := make(chan struct{})
	defer close(release) // let any still-blocked handler goroutines finish once the test is done asserting

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<a href="/1">1</a><a href="/2">2</a><a href="/3">3</a><a href="/4">4</a>`))
	})
	slow := func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`leaf`))
	}
	for _, p := range []string{"/1", "/2", "/3", "/4"} {
		mux.HandleFunc(p, slow)
	}

	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg, err := config.New(srv.URL+"/",
		config.WithConcurrency(4),
		config.WithPerHostConcurrency(4),
		config.WithMaxPages(20),
		config.WithMaxDepth(3),
		config.WithTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	rec := &recordingWriter{}
	eng, err := New(cfg, output.NewWriter(rec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Short enough to cancel while the /1../4 requests are still blocked on
	// release, forcing Run down its ctx.Done() shutdown path mid-crawl.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, err := eng.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	runtime.GC()
	if err := leakProfile.WriteTo(io.Discard, 1); err != nil {
		t.Fatalf("goroutineleak WriteTo: %v", err)
	}
	if n := leakProfile.Count(); n > 0 {
		t.Errorf("Run returned but goroutineleak profile reports %d leaked goroutine(s)", n)
	}
}

type recordingWriter struct{}

func (r *recordingWriter) Write(p []byte) (int, error) { return len(p), nil }
