package benchlab

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/commonhuman-lab/chcrawl/internal/config"
	"github.com/commonhuman-lab/chcrawl/internal/engine"
	"github.com/commonhuman-lab/chcrawl/internal/output"
)

// RunOptions controls how a workload is executed. Defaults are generous
// enough to let every workload fully traverse (no MaxPages/MaxDepth
// cutoffs mid-workload), so the oracle comparison is measuring discovery
// correctness, not budget exhaustion.
type RunOptions struct {
	MaxDepth           int
	MaxPages           int
	SameOrigin         bool
	Concurrency        int
	PerHostConcurrency int
	MaxBodyBytes       int64
}

func (o RunOptions) withDefaults() RunOptions {
	if o.MaxDepth == 0 {
		o.MaxDepth = 40
	}
	if o.MaxPages == 0 {
		o.MaxPages = 5000
	}
	if o.Concurrency == 0 {
		o.Concurrency = 10
	}
	if o.PerHostConcurrency == 0 {
		o.PerHostConcurrency = 4
	}
	if o.MaxBodyBytes == 0 {
		o.MaxBodyBytes = 64 * 1024 * 1024
	}
	return o
}

// Result is one workload's measured outcome, ready to compare against its
// Oracle and report on.
type Result struct {
	Workload       string
	Duration       time.Duration
	Summary        output.SummaryEvent
	Oracle         Oracle
	Diffs          []string
	PeakRSSBytes   uint64
	HeapAllocDelta uint64
}

// Passed reports whether every compared metric matched the oracle exactly.
func (r Result) Passed() bool { return len(r.Diffs) == 0 }

// Run starts site's local HTTP servers, crawls it with the real engine,
// and compares the result against the site's own oracle. sameOrigin
// defaults to true (RunOptions.SameOrigin's zero value) unless explicitly
// set via opts.
func Run(ctx context.Context, site *Site, sameOrigin bool, opts RunOptions) (*Result, error) {
	opts = opts.withDefaults()
	opts.SameOrigin = sameOrigin

	servers := site.Start()
	defer servers.Close()

	cfg, err := config.New(servers.SeedURL,
		config.WithConcurrency(opts.Concurrency),
		config.WithPerHostConcurrency(opts.PerHostConcurrency),
		config.WithMaxPages(opts.MaxPages),
		config.WithMaxDepth(opts.MaxDepth),
		config.WithSameOrigin(opts.SameOrigin),
		config.WithMaxBodyBytes(opts.MaxBodyBytes),
		config.WithTimeout(10*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("benchlab: building config: %w", err)
	}

	eng, err := engine.New(cfg, output.NewWriter(io.Discard))
	if err != nil {
		return nil, fmt.Errorf("benchlab: building engine: %w", err)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	start := time.Now()
	summary, err := eng.Run(ctx)
	duration := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("benchlab: running crawl: %w", err)
	}

	runtime.ReadMemStats(&after)

	oracle := site.Compute(opts.MaxDepth, opts.SameOrigin)

	r := &Result{
		Workload:       site.Name,
		Duration:       duration,
		Summary:        *summary,
		Oracle:         oracle,
		PeakRSSBytes:   peakRSSBytes(),
		HeapAllocDelta: after.TotalAlloc - before.TotalAlloc,
	}
	r.Diffs = diff(oracle, *summary)
	return r, nil
}

func diff(o Oracle, s output.SummaryEvent) []string {
	var diffs []string
	check := func(name string, want, got int64) {
		if want != got {
			diffs = append(diffs, fmt.Sprintf("%s: want %d, got %d", name, want, got))
		}
	}
	check("requests_made", int64(o.RequestsMade), s.RequestsMade)
	check("responses_ok", int64(o.ResponsesOK), s.ResponsesOK)
	check("responses_failed", int64(o.ResponsesFailed), s.ResponsesFailed)
	check("redirects_followed", int64(o.RedirectsFollowed), s.RedirectsFollowed)
	check("duplicates_rejected", int64(o.DuplicatesRejected), s.DuplicatesRejected)
	check("forms_discovered", int64(o.Forms), s.Forms)
	check("params_discovered", int64(o.Params), s.Params)
	check("js_files_discovered", int64(o.JSFiles), s.JSFiles)
	check("js_routes_discovered", int64(o.JSRoutes), s.JSRoutes)
	check("endpoints_discovered", int64(o.Endpoints), s.Endpoints)
	return diffs
}

// peakRSSBytes reads VmHWM (peak resident set size) from /proc/self/status
// on Linux. Best-effort: returns 0 if unavailable (e.g. non-Linux, or a
// restricted /proc).
func peakRSSBytes() uint64 {
	if runtime.GOOS != "linux" {
		return 0
	}
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "VmHWM:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}
