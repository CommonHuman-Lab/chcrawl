package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/commonhuman-lab/chcrawl/config"
	"github.com/commonhuman-lab/chcrawl/fetch"
	"github.com/commonhuman-lab/chcrawl/internal/sitemap"
	"github.com/commonhuman-lab/chcrawl/output"
)

// TestSitemapSeeding_CrawlsRoutesInvisibleInHTML verifies the core value
// prop: an SPA shell with zero <a> tags still gets its real routes crawled
// when they are listed in /sitemap.xml.
func TestSitemapSeeding_CrawlsRoutesInvisibleInHTML(t *testing.T) {
	var mux http.ServeMux
	// The SPA shell: no links at all.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><html><body><div id="root"></div></body></html>`))
	})
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>http://` + r.Host + `/services</loc></url>
  <url><loc>http://` + r.Host + `/pricing</loc></url>
  <url><loc>http://external.example/offsite</loc></url>
</urlset>`))
	})
	mux.HandleFunc("/services", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>services</body></html>`))
	})
	mux.HandleFunc("/pricing", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>pricing</html>`))
	})

	srv := httptest.NewServer(&mux)
	defer srv.Close()

	cfg, err := config.New(srv.URL+"/",
		config.WithConcurrency(2),
		config.WithPerHostConcurrency(2),
		config.WithMaxPages(20),
		config.WithMaxDepth(1),
		config.WithTimeout(5*time.Second),
		config.WithDiscoverSitemap(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	writer := output.NewWriter(discardWriter{})
	eng, err := New(cfg, writer)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := eng.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if summary.SitemapURLs != 2 {
		t.Errorf("SitemapURLs = %d, want 2 (off-origin entry must be scope-dropped)", summary.SitemapURLs)
	}
	if summary.ResponsesOK < 3 {
		t.Errorf("ResponsesOK = %d, want >=3 (shell + /services + /pricing)", summary.ResponsesOK)
	}
}

// TestSitemapSeeding_RobotsDirective checks the robots.txt Sitemap: path.
func TestSitemapSeeding_RobotsDirective(t *testing.T) {
	var mux http.ServeMux
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>home</body></html>`))
	})
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("User-agent: *\nSitemap: " + "" + "\n")) // malformed on purpose
		w.Write([]byte("Sitemap: http://" + r.Host + "/map.xml\n"))
	})
	// /map.xml is the real index pointing at one child sitemap.
	mux.HandleFunc("/map.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0"?><sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><sitemap><loc>http://` + r.Host + `/child.xml</loc></sitemap></sitemapindex>`))
	})
	mux.HandleFunc("/child.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>http://` + r.Host + `/deep</loc></url></urlset>`))
	})
	mux.HandleFunc("/deep", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>deep</body></html>`))
	})

	srv := httptest.NewServer(&mux)
	defer srv.Close()

	cfg, err := config.New(srv.URL+"/",
		config.WithConcurrency(2),
		config.WithPerHostConcurrency(2),
		config.WithMaxPages(20),
		config.WithMaxDepth(1),
		config.WithTimeout(5*time.Second),
		config.WithDiscoverSitemap(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(cfg, output.NewWriter(discardWriter{}))
	if err != nil {
		t.Fatal(err)
	}
	summary, err := eng.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.SitemapURLs != 1 {
		t.Errorf("SitemapURLs = %d, want 1 (/deep via robots→index→child)", summary.SitemapURLs)
	}
}

// TestSitemapSeeding_NoSitemapPresent: absence must not break the crawl.
func TestSitemapSeeding_NoSitemapPresent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<html><body>only page</body></html>`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	cfg, err := config.New(srv.URL+"/",
		config.WithConcurrency(2),
		config.WithPerHostConcurrency(2),
		config.WithMaxPages(10),
		config.WithMaxDepth(1),
		config.WithTimeout(5*time.Second),
		config.WithDiscoverSitemap(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(cfg, output.NewWriter(discardWriter{}))
	if err != nil {
		t.Fatal(err)
	}
	summary, err := eng.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.SitemapURLs != 0 {
		t.Errorf("SitemapURLs = %d, want 0", summary.SitemapURLs)
	}
	if summary.ResponsesOK != 1 {
		t.Errorf("ResponsesOK = %d, want 1 (crawl completes normally)", summary.ResponsesOK)
	}
}

// TestSitemapSeeding_UnboundedMaxPagesDoesNotDeadlock guards against a
// regression where seedSitemap's only cap was `MaxPages > 0 && ...`, so
// MaxPages=0 (documented as "unbounded") let it push more items than the
// frontier had room for, blocking forever since no worker exists yet to
// drain it.
func TestSitemapSeeding_UnboundedMaxPagesDoesNotDeadlock(t *testing.T) {
	var mux http.ServeMux
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>home</body></html>`))
	})
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` +
			`<url><loc>http://` + r.Host + `/a</loc></url>` +
			`<url><loc>http://` + r.Host + `/b</loc></url>` +
			`<url><loc>http://` + r.Host + `/c</loc></url>` +
			`<url><loc>http://` + r.Host + `/d</loc></url>` +
			`<url><loc>http://` + r.Host + `/e</loc></url></urlset>`))
	})
	mux.HandleFunc("/a", okPage)
	mux.HandleFunc("/b", okPage)
	mux.HandleFunc("/c", okPage)
	mux.HandleFunc("/d", okPage)
	mux.HandleFunc("/e", okPage)

	srv := httptest.NewServer(&mux)
	defer srv.Close()

	cfg, err := config.New(srv.URL+"/",
		config.WithConcurrency(1),
		config.WithPerHostConcurrency(1),
		config.WithMaxPages(0), // unbounded — the case that used to deadlock
		config.WithMaxFrontierSize(3),
		config.WithMaxDepth(1),
		config.WithTimeout(5*time.Second),
		config.WithDiscoverSitemap(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := New(cfg, output.NewWriter(discardWriter{}))
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var summary *output.SummaryEvent
	go func() {
		defer close(done)
		summary, err = eng.Run(context.Background())
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run() deadlocked seeding sitemap URLs with MaxPages=0")
	}
	if err != nil {
		t.Fatal(err)
	}
	if summary.SitemapURLs > 2 {
		t.Errorf("SitemapURLs = %d, want <= 2 (capped by frontier headroom, not MaxPages)", summary.SitemapURLs)
	}
}

func okPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`<html><body>ok</body></html>`))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// compile-time check: fetch.Fetcher satisfies sitemap.Fetcher usage in engine
var _ sitemap.Fetcher = (fetch.Fetcher)(nil)
