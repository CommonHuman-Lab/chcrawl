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

	"github.com/commonhuman-lab/chcrawl/internal/config"
	"github.com/commonhuman-lab/chcrawl/internal/engine"
	"github.com/commonhuman-lab/chcrawl/internal/output"
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
		concurrency     = fs.Int("concurrency", 10, "global concurrent worker count")
		perHost         = fs.Int("per-host-concurrency", 4, "max concurrent in-flight requests per host")
		maxPages        = fs.Int("max-pages", 100, "stop after this many successfully parsed pages (0 = unbounded)")
		maxDepth        = fs.Int("max-depth", 3, "maximum BFS depth from the seed URL")
		maxDuration     = fs.Duration("max-duration", 0, "stop after this long (0 = unbounded)")
		maxFrontierSize = fs.Int("max-frontier-size", 100_000, "bounded frontier capacity (backpressure kicks in above this)")
		maxBodyBytes    = fs.Int64("max-body-bytes", 10*1024*1024, "per-response body size cap in bytes")
		maxRedirects    = fs.Int("max-redirects", 20, "maximum redirect hops to follow per request")
		timeout         = fs.Duration("timeout", 15*time.Second, "per-request timeout")
		delay           = fs.Duration("delay", 0, "fixed delay applied before every request (0 = none)")
		sameOrigin      = fs.Bool("same-origin", true, "restrict crawl to the seed URL's origin")
		insecure        = fs.Bool("insecure", false, "skip TLS certificate verification")
		proxy           = fs.String("proxy", "", "HTTP/HTTPS proxy URL")
		cookies         = fs.String("cookies", "", "cookie header string, e.g. 'a=1; b=2'")
		exclude         = fs.String("exclude", "", "comma-separated regex patterns to exclude")
		legacy          = fs.Bool("legacy-mode", false, "use simpler legacy normalization/budget/retry behavior")
		respectRobots   = fs.Bool("respect-robots-txt", false, "honor robots.txt Disallow rules (off by default)")
		discoverOpenAPI = fs.Bool("discover-openapi", false, "probe canonical OpenAPI/Swagger spec locations against the seed's origin")
		discoverSitemap = fs.Bool("discover-sitemap", false, "discover the site's XML sitemap (robots.txt Sitemap: or /sitemap.xml) and seed the crawl with its URLs")
		recoverMaps     = fs.Bool("recover-source-maps", false, "recover original JS source via .js.map files for every JS file crawled")
		sortQueryParams = fs.Bool("sort-query-params", false, "sort query params during URL normalization (off by default: order can be semantically meaningful)")
		outPath         = fs.String("output", "", "also write the full JSONL record stream to this file")
		showVersion     = fs.Bool("version", false, "print the version and exit")
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
		config.WithInsecureSkipVerify(*insecure),
		config.WithProxy(*proxy),
		config.WithCookies(*cookies),
		config.WithRespectRobotsTxt(*respectRobots),
		config.WithSortQueryParams(*sortQueryParams),
		config.WithDiscoverOpenAPI(*discoverOpenAPI),
		config.WithDiscoverSitemap(*discoverSitemap),
		config.WithRecoverSourceMaps(*recoverMaps),
	}
	if len(headers) > 0 {
		opts = append(opts, config.WithHeaders(headers))
	}
	if *exclude != "" {
		opts = append(opts, config.WithExcludePatterns(strings.Split(*exclude, ",")))
	}
	if *legacy {
		opts = append(opts, config.LegacyPreset())
	}

	cfg, err := config.New(seed, opts...)
	if err != nil {
		return err
	}

	var writer output.EventWriter = output.NewHumanWriter(os.Stdout, 10)
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			return err
		}
		defer f.Close()
		writer = output.NewMultiWriter(writer, output.NewWriter(f))
	}

	eng, err := engine.New(cfg, writer)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	_, err = eng.Run(ctx)
	return err
}
