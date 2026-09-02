// Package config defines the crawl configuration surface for chcrawl.
package config

import (
	"fmt"
	"net/url"
	"regexp"
	"time"

	"github.com/commonhuman-lab/chcrawl/internal/retry"
)

// CanonicalizationMode controls how aggressively URLs are normalized before
// dedup/scope checks.
type CanonicalizationMode int

const (
	// StrictMode strips default ports and normalizes percent-encoding case,
	// in addition to the LegacyMode behaviors. This is the chcrawl default.
	StrictMode CanonicalizationMode = iota
	// LegacyMode applies a simpler normalization: lowercase scheme+host,
	// strip trailing slash (root pinned to "/"), strip fragment, query
	// string left untouched.
	LegacyMode
)

// Options is the single consolidated configuration surface for a crawl run.
type Options struct {
	SeedURL string

	// Concurrency bounds.
	Concurrency        int
	PerHostConcurrency int
	MaxFrontierSize    int

	// Crawl bounds.
	MaxPages int // 0 = unbounded

	MaxDepth                 int
	MaxDuration              time.Duration
	CountErrorsAgainstBudget bool

	// Scope.
	SameOrigin      bool
	ExcludePatterns []*regexp.Regexp

	// URL canonicalization.
	Canonicalization CanonicalizationMode
	SortQueryParams  bool

	// HTTP client.
	Timeout             time.Duration
	Proxy               string
	Headers             map[string]string
	Cookies             string
	InsecureSkipVerify  bool
	Delay               time.Duration
	MaxBodyBytes        int64
	MaxRedirects        int
	AllowedContentTypes []string

	// Policy toggles.
	RespectRobotsTxt  bool
	RetryPolicy       retry.Policy
	RecoverSourceMaps bool
	DiscoverOpenAPI   bool
	DiscoverSitemap   bool

	// Headless rendering.
	RenderJS          bool
	RenderConcurrency int
	RenderTimeout     time.Duration
}

// Option mutates an Options struct during construction.
type Option func(*Options)

func WithConcurrency(n int) Option           { return func(o *Options) { o.Concurrency = n } }
func WithPerHostConcurrency(n int) Option    { return func(o *Options) { o.PerHostConcurrency = n } }
func WithMaxFrontierSize(n int) Option       { return func(o *Options) { o.MaxFrontierSize = n } }
func WithMaxPages(n int) Option              { return func(o *Options) { o.MaxPages = n } }
func WithMaxDepth(n int) Option              { return func(o *Options) { o.MaxDepth = n } }
func WithMaxDuration(d time.Duration) Option { return func(o *Options) { o.MaxDuration = d } }
func WithCountErrorsAgainstBudget(b bool) Option {
	return func(o *Options) { o.CountErrorsAgainstBudget = b }
}
func WithSameOrigin(b bool) Option { return func(o *Options) { o.SameOrigin = b } }
func WithExcludePatterns(patterns []string) Option {
	return func(o *Options) {
		for _, p := range patterns {
			if re, err := regexp.Compile(p); err == nil {
				o.ExcludePatterns = append(o.ExcludePatterns, re)
			}
		}
	}
}
func WithCanonicalization(m CanonicalizationMode) Option {
	return func(o *Options) { o.Canonicalization = m }
}
func WithSortQueryParams(b bool) Option      { return func(o *Options) { o.SortQueryParams = b } }
func WithTimeout(d time.Duration) Option     { return func(o *Options) { o.Timeout = d } }
func WithProxy(p string) Option              { return func(o *Options) { o.Proxy = p } }
func WithHeaders(h map[string]string) Option { return func(o *Options) { o.Headers = h } }
func WithCookies(c string) Option            { return func(o *Options) { o.Cookies = c } }
func WithInsecureSkipVerify(b bool) Option {
	return func(o *Options) { o.InsecureSkipVerify = b }
}
func WithDelay(d time.Duration) Option   { return func(o *Options) { o.Delay = d } }
func WithMaxBodyBytes(n int64) Option    { return func(o *Options) { o.MaxBodyBytes = n } }
func WithMaxRedirects(n int) Option      { return func(o *Options) { o.MaxRedirects = n } }
func WithRespectRobotsTxt(b bool) Option { return func(o *Options) { o.RespectRobotsTxt = b } }
func WithRetryPolicy(p retry.Policy) Option {
	return func(o *Options) { o.RetryPolicy = p }
}
func WithRecoverSourceMaps(b bool) Option { return func(o *Options) { o.RecoverSourceMaps = b } }
func WithDiscoverOpenAPI(b bool) Option   { return func(o *Options) { o.DiscoverOpenAPI = b } }
func WithDiscoverSitemap(b bool) Option   { return func(o *Options) { o.DiscoverSitemap = b } }

func WithRenderJS(b bool) Option               { return func(o *Options) { o.RenderJS = b } }
func WithRenderConcurrency(n int) Option       { return func(o *Options) { o.RenderConcurrency = n } }
func WithRenderTimeout(d time.Duration) Option { return func(o *Options) { o.RenderTimeout = d } }

// defaults returns an Options populated with chcrawl's default values.
func defaults() *Options {
	return &Options{
		Concurrency:              10,
		PerHostConcurrency:       4,
		MaxFrontierSize:          100_000,
		MaxPages:                 100,
		MaxDepth:                 3,
		CountErrorsAgainstBudget: false,
		SameOrigin:               true,
		Canonicalization:         StrictMode,
		SortQueryParams:          false,
		Timeout:                  15 * time.Second,
		InsecureSkipVerify:       false,
		MaxBodyBytes:             10 * 1024 * 1024, // 10MiB
		MaxRedirects:             20,
		AllowedContentTypes:      []string{"html", "javascript", "json", "xml"},
		RespectRobotsTxt:         false,
		RetryPolicy:              retry.NewDefault(),
		RenderConcurrency:        4,
		RenderTimeout:            25 * time.Second,
	}
}

// New builds a validated Options from the seed URL and the given Option
// functions.
func New(seedURL string, opts ...Option) (*Options, error) {
	o := defaults()
	o.SeedURL = seedURL
	for _, opt := range opts {
		opt(o)
	}
	if o.Delay < 0 {
		o.Delay = 0
	}
	if err := o.Validate(); err != nil {
		return nil, err
	}
	return o, nil
}

// Validate fails fast on nonsensical configuration combinations.
func (o *Options) Validate() error {
	if o.SeedURL == "" {
		return fmt.Errorf("config: seed URL is required")
	}
	u, err := url.Parse(o.SeedURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("config: invalid seed URL %q", o.SeedURL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("config: seed URL scheme must be http or https, got %q", u.Scheme)
	}
	if o.Concurrency < 1 {
		return fmt.Errorf("config: Concurrency must be >= 1")
	}
	if o.PerHostConcurrency < 1 {
		return fmt.Errorf("config: PerHostConcurrency must be >= 1")
	}
	if o.PerHostConcurrency > o.Concurrency {
		return fmt.Errorf("config: PerHostConcurrency (%d) must not exceed Concurrency (%d)", o.PerHostConcurrency, o.Concurrency)
	}
	if o.MaxPages < 0 {
		return fmt.Errorf("config: MaxPages must be >= 0 (0 = unbounded)")
	}
	if o.MaxDepth < 0 {
		return fmt.Errorf("config: MaxDepth must be >= 0")
	}
	if o.MaxBodyBytes < 1 {
		return fmt.Errorf("config: MaxBodyBytes must be >= 1")
	}
	if o.MaxFrontierSize < 1 {
		return fmt.Errorf("config: MaxFrontierSize must be >= 1")
	}
	if o.RenderJS && o.RenderConcurrency < 1 {
		return fmt.Errorf("config: RenderConcurrency must be >= 1 when RenderJS is set")
	}
	if o.RenderJS && o.RenderTimeout <= 0 {
		return fmt.Errorf("config: RenderTimeout must be > 0 when RenderJS is set")
	}
	return nil
}

// LegacyPreset switches canonicalization and retry-relevant fields to their
// simpler LegacyMode behavior.
func LegacyPreset() Option {
	return func(o *Options) {
		o.Canonicalization = LegacyMode
		o.SortQueryParams = false
		o.CountErrorsAgainstBudget = true
		o.RetryPolicy = retry.NewLegacy()
	}
}
