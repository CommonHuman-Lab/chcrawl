// Command chcrawl is a thin CLI over the internal/engine crawler library.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/commonhuman-lab/chcrawl/auth"
	"github.com/commonhuman-lab/chcrawl/config"
	"github.com/commonhuman-lab/chcrawl/engine"
	"github.com/commonhuman-lab/chcrawl/fetch"
	"github.com/commonhuman-lab/chcrawl/output"
)

const version = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "chcrawl:", err)
		os.Exit(1)
	}
}

// headerFlag collects repeated -header "Name: Value" flags.
type headerFlag map[string]string

func (h headerFlag) String() string { return "" }

func (h headerFlag) Set(v string) error {
	name, value, ok := strings.Cut(v, ":")
	if !ok {
		return fmt.Errorf("header %q must be in \"Name: Value\" form", v)
	}
	h[strings.TrimSpace(name)] = strings.TrimSpace(value)
	return nil
}

// newAuthFetcher builds a throwaway fetcher for a login round-trip. MaxBodyBytes must be passed
// explicitly: fetch.Fetcher treats a zero value as a 1-byte cap, truncating the login response.
func newAuthFetcher(timeout time.Duration, proxy string, insecure bool, maxBodyBytes int64) (fetch.Fetcher, error) {
	return fetch.New(fetch.Config{
		Timeout:            timeout,
		Proxy:              proxy,
		InsecureSkipVerify: insecure,
		MaxBodyBytes:       maxBodyBytes,
	})
}

func boolCount(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

func reorderArgs(fs *flag.FlagSet, args []string) []string {
	flags := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.ContainsRune(name, '=') {
			continue // "-flag=value" form already carries its value
		}
		if f := fs.Lookup(name); f != nil {
			if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
				continue // boolean flags don't consume the next token
			}
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}

func run(args []string) error {
	fs := flag.NewFlagSet("chcrawl", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `chcrawl %s — high-performance web crawler and recon engine

Usage:
  chcrawl [flags] <seed-url>

The console shows a short human-readable progress stream and a final
summary. Pass -output to also write the full JSONL record stream (one
"page" record per fetched page, "error" records for failures, and a final
"summary" record with aggregate stats) to a file.

Flags:
`, version)
		fs.PrintDefaults()
	}

	var (
		concurrency       = fs.Int("concurrency", 10, "global concurrent worker count")
		perHost           = fs.Int("per-host-concurrency", 4, "max concurrent in-flight requests per host")
		maxPages          = fs.Int("max-pages", 100, "stop after this many successfully parsed pages (0 = unbounded)")
		maxDepth          = fs.Int("max-depth", 3, "maximum BFS depth from the seed URL")
		maxDuration       = fs.Duration("max-duration", 0, "stop after this long (0 = unbounded)")
		maxFrontierSize   = fs.Int("max-frontier-size", 100_000, "bounded frontier capacity (backpressure kicks in above this)")
		maxBodyBytes      = fs.Int64("max-body-bytes", 10*1024*1024, "per-response body size cap in bytes")
		maxRedirects      = fs.Int("max-redirects", 20, "maximum redirect hops to follow per request")
		timeout           = fs.Duration("timeout", 15*time.Second, "per-request timeout")
		delay             = fs.Duration("delay", 0, "fixed delay applied before every request (0 = none)")
		sameOrigin        = fs.Bool("same-origin", true, "restrict crawl to the seed URL's origin")
		includeSubdomains = fs.Bool("include-subdomains", false, "allow any subdomain of the seed URL's root domain, in addition to the seed's own host (requires -same-origin, the default)")
		insecure          = fs.Bool("insecure", false, "skip TLS certificate verification")
		proxy             = fs.String("proxy", "", "HTTP/HTTPS proxy URL")
		cookies           = fs.String("cookies", "", "cookie header string, e.g. 'a=1; b=2'")
		exclude           = fs.String("exclude", "", "comma-separated regex patterns to exclude")
		include           = fs.String("include", "", "comma-separated regex patterns; if set, only URLs matching at least one are in scope (still subject to -exclude)")
		allow             = fs.String("allow", "", "comma-separated list of domains to allow (layered on top of other scope rules); empty allows everything not denied")
		deny              = fs.String("deny", "", "comma-separated list of domains to deny; takes precedence over -allow and every other scope rule")
		legacy            = fs.Bool("legacy-mode", false, "use simpler legacy normalization/budget/retry behavior")
		respectRobots     = fs.Bool("respect-robots-txt", false, "honor robots.txt Disallow rules (off by default)")
		discoverOpenAPI   = fs.Bool("discover-openapi", false, "probe canonical OpenAPI/Swagger spec locations against the seed's origin")
		discoverSitemap   = fs.Bool("discover-sitemap", false, "discover the site's XML sitemap (robots.txt Sitemap: or /sitemap.xml) and seed the crawl with its URLs")
		recoverMaps       = fs.Bool("recover-source-maps", false, "recover original JS source via .js.map files for every JS file crawled")
		renderJS          = fs.Bool("render-js", false, "render pages with a headless browser before extraction (downloads Chromium on first run if none is found; slower than the default fetcher)")
		renderConcurrency = fs.Int("render-concurrency", 4, "max concurrent browser tabs when -render-js is set")
		renderTimeout     = fs.Duration("render-timeout", 25*time.Second, "per-page render timeout when -render-js is set")
		sortQueryParams   = fs.Bool("sort-query-params", false, "sort query params during URL normalization (off by default: order can be semantically meaningful)")
		outPath           = fs.String("output", "", "also write the full JSONL record stream to this file, or to stdout (in place of the human summary) if the path is \"-\"")
		showVersion       = fs.Bool("version", false, "print the version and exit")
		loginURL          = fs.String("login-url", "", "log in before crawling: fetch this URL's form, submit credentials, and carry the resulting session")
		loginUser         = fs.String("login-user", "", "username for -login-url")
		loginPass         = fs.String("login-pass", "", "password for -login-url")
		loginUserField    = fs.String("login-user-field", "username", "form field name for the username at -login-url")
		loginPassField    = fs.String("login-pass-field", "password", "form field name for the password at -login-url")

		bearerTokenURL     = fs.String("bearer-token-url", "", "log in before crawling: POST an OAuth2 client-credentials request to this URL and carry the resulting Authorization: Bearer header")
		bearerClientID     = fs.String("bearer-client-id", "", "client_id for -bearer-token-url")
		bearerClientSecret = fs.String("bearer-client-secret", "", "client_secret for -bearer-token-url")
		bearerGrantType    = fs.String("bearer-grant-type", "client_credentials", "OAuth2 grant_type for -bearer-token-url")
		basicUser          = fs.String("basic-user", "", "username for HTTP Basic auth (sent on every request; no login round-trip)")
		basicPass          = fs.String("basic-pass", "", "password for -basic-user (may itself contain colons)")
	)
	headers := headerFlag{}
	fs.Var(headers, "header", "extra request header \"Name: Value\" (repeatable)")

	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println("chcrawl", version)
		return nil
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("exactly one seed URL is required")
	}
	seed := fs.Arg(0)

	formActive := *loginURL != "" && *loginUser != ""
	bearerActive := *bearerTokenURL != ""
	basicActive := *basicUser != ""
	if active := boolCount(formActive, bearerActive, basicActive); active > 1 {
		return fmt.Errorf("chcrawl: only one of -login-url/-login-user, -bearer-token-url, -basic-user may be specified")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var authResult auth.Result
	switch {
	case formActive:
		loginFetcher, err := newAuthFetcher(*timeout, *proxy, *insecure, *maxBodyBytes)
		if err != nil {
			return fmt.Errorf("auth: building login fetcher: %w", err)
		}
		authResult, err = auth.FormLogin(ctx, loginFetcher, *loginURL, *loginUserField, *loginUser, *loginPassField, *loginPass, nil)
		if err != nil {
			return fmt.Errorf("auth: form login failed: %w", err)
		}
	case bearerActive:
		bearerFetcher, err := newAuthFetcher(*timeout, *proxy, *insecure, *maxBodyBytes)
		if err != nil {
			return fmt.Errorf("auth: building bearer-login fetcher: %w", err)
		}
		authResult, err = auth.BearerLogin(ctx, bearerFetcher, *bearerTokenURL, *bearerClientID, *bearerClientSecret, *bearerGrantType)
		if err != nil {
			return fmt.Errorf("auth: bearer login failed: %w", err)
		}
	case basicActive:
		h, err := auth.BasicAuthHeader(*basicUser + ":" + *basicPass)
		if err != nil {
			return fmt.Errorf("auth: basic auth: %w", err)
		}
		authResult = auth.Result{Headers: h}
	}

	// An explicit -cookies flag wins; login-derived headers always win on collision (mirrors
	// BreachSQL's __main__.py auth-merge semantics for consistency across the toolchain).
	if authResult.Cookies != "" && *cookies == "" {
		*cookies = authResult.Cookies
	}
	for k, v := range authResult.Headers {
		headers[k] = v
	}

	opts := []config.Option{
		config.WithConcurrency(*concurrency),
		config.WithPerHostConcurrency(*perHost),
		config.WithMaxPages(*maxPages),
		config.WithMaxDepth(*maxDepth),
		config.WithMaxDuration(*maxDuration),
		config.WithMaxFrontierSize(*maxFrontierSize),
		config.WithMaxBodyBytes(*maxBodyBytes),
		config.WithMaxRedirects(*maxRedirects),
		config.WithTimeout(*timeout),
		config.WithDelay(*delay),
		config.WithSameOrigin(*sameOrigin),
		config.WithIncludeSubdomains(*includeSubdomains),
		config.WithInsecureSkipVerify(*insecure),
		config.WithProxy(*proxy),
		config.WithCookies(*cookies),
		config.WithRespectRobotsTxt(*respectRobots),
		config.WithSortQueryParams(*sortQueryParams),
		config.WithDiscoverOpenAPI(*discoverOpenAPI),
		config.WithDiscoverSitemap(*discoverSitemap),
		config.WithRecoverSourceMaps(*recoverMaps),
		config.WithRenderJS(*renderJS),
		config.WithRenderConcurrency(*renderConcurrency),
		config.WithRenderTimeout(*renderTimeout),
	}
	if len(headers) > 0 {
		opts = append(opts, config.WithHeaders(headers))
	}
	if *exclude != "" {
		opts = append(opts, config.WithExcludePatterns(strings.Split(*exclude, ",")))
	}
	if *include != "" {
		opts = append(opts, config.WithIncludePatterns(strings.Split(*include, ",")))
	}
	if *allow != "" {
		opts = append(opts, config.WithAllowedDomains(strings.Split(*allow, ",")))
	}
	if *deny != "" {
		opts = append(opts, config.WithDeniedDomains(strings.Split(*deny, ",")))
	}
	if *legacy {
		opts = append(opts, config.LegacyPreset())
	}

	cfg, err := config.New(seed, opts...)
	if err != nil {
		return err
	}

	var writer output.EventWriter
	switch {
	case *outPath == "-":
		// Pure JSONL to stdout: a downstream consumer parses this stream, so no human text can mix in.
		writer = output.NewWriter(os.Stdout)
	case *outPath != "":
		f, err := os.Create(*outPath)
		if err != nil {
			return err
		}
		defer f.Close()
		writer = output.NewMultiWriter(output.NewHumanWriter(os.Stdout, 10), output.NewWriter(f))
	default:
		writer = output.NewHumanWriter(os.Stdout, 10)
	}

	eng, err := engine.New(cfg, writer)
	if err != nil {
		return err
	}
	defer eng.Close()

	_, err = eng.Run(ctx)
	return err
}
