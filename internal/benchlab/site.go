// Package benchlab builds fully local, deterministic HTTP targets for
// benchmarking the crawl engine, plus a pure-Go oracle computed from the
// same site specification used to build the target — so the two can never
// drift apart. Nothing in this package ever binds outside 127.0.0.1 or
// makes an external network call; every workload is self-contained.
package benchlab

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// FormSpec describes a form to embed in a page.
type FormSpec struct {
	Action string
	Method string
	Fields []string
}

// JSEndpoint is one API call the generated JS body will contain. Path must
// contain "/rest/" or "/api/" to be discoverable by JSEndpointExtractor.
type JSEndpoint struct {
	Method string
	Path   string
}

// PageSpec is one page (or JS asset, or redirect) in a synthetic site.
type PageSpec struct {
	Host           string // host label; "" means the site's first/default host
	Path           string
	Links          []string // paths (or "hostlabel:/path" for cross-host) this page links to
	Forms          []FormSpec
	ScriptSrcs     []string
	DataLinks      []string     // paths surfaced only via data-href/data-url/data-link/data-action attributes, not <a href>
	JSEndpoints    []JSEndpoint // if set, this page is served as JS containing fetch() calls for each
	Redirect       string       // if set, this page 301s to this path instead of serving content
	Status         int          // 0 = 200
	DelayMS        int
	PaddingKB      int    // filler bytes appended as an HTML comment, to reach a target body size
	BadContentType bool   // claim text/html but serve non-HTML bytes, to test parser tolerance
	RawBody        string // if set, served verbatim instead of the generated HTML (e.g. malformed/deeply-nested markup); Content-Type is still text/html unless BadContentType/JSEndpoints/ContentTypeOverride override it
	Empty          bool   // serve a zero-length 200 body instead of generated HTML
	// ContentTypeOverride, if set, replaces the default "text/html"
	// Content-Type — for exercising extractors' Applies() content-type
	// gating against a genuinely non-HTML/non-JS response.
	ContentTypeOverride string

	// FailFirstNRequests, if > 0, makes the first N requests return
	// FailStatus before the (N+1)th succeeds — for verifying a retry policy
	// recovers a genuinely flaky page. The oracle is unaffected:
	// Status/Redirect describe the page's eventual outcome.
	FailFirstNRequests int
	FailStatus         int // status served during the failing attempts; 0 defaults to 503
}

// Site is a full synthetic target: a set of hosts, each serving a set of
// pages, deterministically generated from PageSpecs.
type Site struct {
	Name  string
	Seed  string // path on the first host to start crawling from
	Hosts []string
	Pages []PageSpec
}

func (s *Site) hostsOrDefault() []string {
	if len(s.Hosts) > 0 {
		return s.Hosts
	}
	return []string{""}
}

func (s *Site) pagesByHost() map[string][]PageSpec {
	m := map[string][]PageSpec{}
	for _, p := range s.Pages {
		m[p.Host] = append(m[p.Host], p)
	}
	return m
}

// switchHandler lets a listener be bound (so its address is known) before
// the handler that will serve it — which may need to reference other
// hosts' addresses — is finalized.
type switchHandler struct{ h http.Handler }

func (s *switchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.h.ServeHTTP(w, r) }

// Servers is a running instance of a Site: one httptest.Server per host,
// all bound to 127.0.0.1, plus the seed URL to crawl from.
type Servers struct {
	SeedURL string
	byHost  map[string]*httptest.Server
}

// Close shuts down every host's server.
func (s *Servers) Close() {
	for _, srv := range s.byHost {
		srv.Close()
	}
}

// Start brings up one local httptest.Server per host in the site, wires
// cross-host links to the real (dynamically assigned) addresses, and
// returns the running Servers. All listeners are 127.0.0.1-only.
func (s *Site) Start() *Servers {
	hosts := s.hostsOrDefault()
	unstarted := map[string]*httptest.Server{}
	switches := map[string]*switchHandler{}
	baseURLs := map[string]string{}

	for _, h := range hosts {
		sw := &switchHandler{}
		srv := httptest.NewUnstartedServer(sw)
		unstarted[h] = srv
		switches[h] = sw
		baseURLs[h] = "http://" + srv.Listener.Addr().String()
	}

	byHost := s.pagesByHost()
	for _, h := range hosts {
		switches[h].h = buildMux(byHost[h], baseURLs, h)
	}
	for _, srv := range unstarted {
		srv.Start()
	}

	seedHost := hosts[0]
	return &Servers{
		SeedURL: baseURLs[seedHost] + s.Seed,
		byHost:  unstarted,
	}
}

// exactPathHandler dispatches by exact URL path only: http.ServeMux's
// classic "/" pattern silently catches every unregistered path, which would
// make a nonexistent path fall through to the root page instead of 404ing.
type exactPathHandler struct {
	pages       map[string]PageSpec
	baseURLs    map[string]string
	selfHost    string
	failCounter map[string]*atomic.Int32 // one entry per page with FailFirstNRequests > 0
}

func (h *exactPathHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := h.pages[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if c, has := h.failCounter[r.URL.Path]; has && c.Add(1) <= int32(p.FailFirstNRequests) {
		status := p.FailStatus
		if status == 0 {
			status = http.StatusServiceUnavailable
		}
		w.WriteHeader(status)
		return
	}
	servePage(w, p, h.baseURLs, h.selfHost)
}

func buildMux(pages []PageSpec, baseURLs map[string]string, selfHost string) http.Handler {
	byPath := make(map[string]PageSpec, len(pages))
	counters := map[string]*atomic.Int32{}
	for _, p := range pages {
		byPath[p.Path] = p
		if p.FailFirstNRequests > 0 {
			counters[p.Path] = new(atomic.Int32)
		}
	}
	return &exactPathHandler{pages: byPath, baseURLs: baseURLs, selfHost: selfHost, failCounter: counters}
}

func servePage(w http.ResponseWriter, p PageSpec, baseURLs map[string]string, selfHost string) {
	if p.DelayMS > 0 {
		time.Sleep(time.Duration(p.DelayMS) * time.Millisecond)
	}
	if p.Redirect != "" {
		w.Header().Set("Location", resolveLink(p.Redirect, baseURLs, selfHost))
		w.WriteHeader(http.StatusMovedPermanently)
		return
	}
	status := p.Status
	if status == 0 {
		status = http.StatusOK
	}

	if p.BadContentType {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(status)
		w.Write([]byte{0x00, 0x01, 0xFF, 0xFE, 0x00, 0x00}) // deliberately invalid markup bytes
		return
	}

	if p.Empty {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(status)
		return
	}

	if p.JSEndpoints != nil {
		w.Header().Set("Content-Type", "application/javascript")
		w.WriteHeader(status)
		w.Write([]byte(renderJS(p.JSEndpoints)))
		return
	}

	contentType := "text/html"
	if p.ContentTypeOverride != "" {
		contentType = p.ContentTypeOverride
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if p.RawBody != "" {
		w.Write([]byte(p.RawBody))
		return
	}
	w.Write([]byte(renderHTML(p, baseURLs, selfHost)))
}

// renderJS emits method-call-style code (e.g. this.http.get("/api/x")) so
// JSEndpointExtractor's method-inference heuristic — which scans the ~60
// characters before a matched path for a preceding get/post/put/patch/
// delete keyword — correctly attributes each endpoint's declared method.
func renderJS(endpoints []JSEndpoint) string {
	var b strings.Builder
	for _, ep := range endpoints {
		method := strings.ToLower(ep.Method)
		if method == "" {
			method = "get"
		}
		fmt.Fprintf(&b, "this.http.%s(%q);\n", method, ep.Path)
	}
	return b.String()
}

func renderHTML(p PageSpec, baseURLs map[string]string, selfHost string) string {
	var b strings.Builder
	b.WriteString("<html><body>\n")
	for i, link := range p.Links {
		fmt.Fprintf(&b, "<a href=%q>link%d</a>\n", resolveLink(link, baseURLs, selfHost), i)
	}
	for _, src := range p.ScriptSrcs {
		fmt.Fprintf(&b, "<script src=%q></script>\n", resolveLink(src, baseURLs, selfHost))
	}
	for i, link := range p.DataLinks {
		fmt.Fprintf(&b, "<div data-href=%q>datalink%d</div>\n", resolveLink(link, baseURLs, selfHost), i)
	}
	for i, f := range p.Forms {
		method := f.Method
		if method == "" {
			method = "get"
		}
		fmt.Fprintf(&b, "<form action=%q method=%q>\n", resolveLink(f.Action, baseURLs, selfHost), method)
		for _, field := range f.Fields {
			fmt.Fprintf(&b, "<input type=\"text\" name=%q>\n", field)
		}
		fmt.Fprintf(&b, "<input type=\"submit\" value=\"go%d\">\n</form>\n", i)
	}
	if p.PaddingKB > 0 {
		b.WriteString("<!--")
		b.WriteString(strings.Repeat("x", p.PaddingKB*1024))
		b.WriteString("-->\n")
	}
	b.WriteString("</body></html>\n")
	return b.String()
}

// resolveLink expands a "hostlabel:/path" cross-host reference into an
// absolute URL against that host's real local address; a plain "/path" is
// left relative (resolved by the browser/crawler against the current page).
func resolveLink(link string, baseURLs map[string]string, selfHost string) string {
	if i := strings.IndexByte(link, ':'); i > 0 && !strings.HasPrefix(link, "/") {
		host, path := link[:i], link[i+1:]
		if base, ok := baseURLs[host]; ok && host != selfHost {
			return base + path
		}
		return path
	}
	return link
}

// chain builds a linear sequence of pages path0 -> path1 -> ... -> pathN-1,
// each linking only to the next, for depth-heavy workloads.
func chain(prefix string, n int) []PageSpec {
	pages := make([]PageSpec, n)
	for i := 0; i < n; i++ {
		p := PageSpec{Path: prefix + strconv.Itoa(i)}
		if i+1 < n {
			p.Links = []string{prefix + strconv.Itoa(i+1)}
		}
		pages[i] = p
	}
	return pages
}

// fanout builds one page linking to n leaf siblings, for wide-site
// workloads.
func fanout(rootPath, leafPrefix string, n int) []PageSpec {
	pages := make([]PageSpec, 0, n+1)
	root := PageSpec{Path: rootPath}
	for i := 0; i < n; i++ {
		leaf := leafPrefix + strconv.Itoa(i)
		root.Links = append(root.Links, leaf)
		pages = append(pages, PageSpec{Path: leaf})
	}
	pages = append(pages, root)
	return pages
}
