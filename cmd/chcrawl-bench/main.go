// Command chcrawl-bench runs the local deterministic benchmark workloads
// in internal/benchlab and reports timing, throughput, memory, and
// discovery-correctness for each. Everything it touches is bound to
// 127.0.0.1 — no Docker, no external network, no public websites.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/commonhuman-lab/chcrawl/internal/benchlab"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "chcrawl-bench:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("chcrawl-bench", flag.ContinueOnError)
	workload := fs.String("workload", "", "run only this workload (default: all)")
	reportPath := fs.String("report", "", "write the report to this path (default: stdout)")
	compare := fs.Bool("compare", false, "score chcrawl against katana/hakrawler/gospider (any not installed are reported, not required) instead of the oracle correctness run")
	retryDisabledComparison := fs.Bool("retry-disabled-comparison", false,
		"run with retries OFF, for apples-to-apples raw crawl-speed comparison against tools that don't retry 5xx by default — NOT the production default, reported separately, never merged with the normal run")
	runs := fs.Int("runs", 30, "measured iterations per workload; -runs 1 uses the original single-run report instead of statistics")
	warmups := fs.Int("warmups", 5, "warmup iterations per workload before measurement begins (discarded, not counted in statistics or correctness; ignored when -runs <= 1)")
	metricBasis := fs.String("metric-basis", string(benchlab.BasisDuration),
		`which measured series drives the headline Median/P95/P99 columns: "duration" (production wall-clock, includes retry backoff — the default) or "active_wall" (engine time with measured backoff excluded — NOT a production number, NOT CPU time)`)
	jsonOut := fs.Bool("json", false, "write the machine-readable report instead of markdown (-runs>1 mode only)")
	internalWorker := fs.String("internal-worker", "",
		"internal use only: measure just this one workload in this process and print its statistics as JSON to stdout. Used by the parent process to isolate each workload's peak-RSS measurement in its own subprocess — see README.md")
	competitorBench := fs.Bool("competitor-bench", false,
		"run the full statistically-hardened competitor comparison (chcrawl/katana/hakrawler/gospider, -runs measured iterations each, correctness-checked every run) instead of chcrawl-only benchmarking")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *internalWorker != "" {
		return runInternalWorker(*internalWorker, *runs, *warmups, *retryDisabledComparison)
	}
	if *competitorBench {
		return runCompetitorBench(*workload, *reportPath, *runs, *warmups, *jsonOut)
	}
	if *compare {
		return runCompare(*workload, *reportPath)
	}
	if *runs <= 1 {
		return runOracle(*workload, *reportPath, *retryDisabledComparison)
	}

	basis := benchlab.MetricBasis(*metricBasis)
	if basis != benchlab.BasisDuration && basis != benchlab.BasisActiveWall {
		return fmt.Errorf("unknown -metric-basis %q (want %q or %q)", *metricBasis, benchlab.BasisDuration, benchlab.BasisActiveWall)
	}
	return runStats(*workload, *reportPath, *runs, *warmups, basis, *retryDisabledComparison, *jsonOut)
}

// runStats is the hardened, statistically-meaningful default mode: every
// workload is measured warmups+runs times in its own dedicated subprocess
// (see runInternalWorker) so peak RSS is genuinely isolated per workload,
// not a process-lifetime high-water mark inherited from an earlier,
// heavier workload — a real measurement bug this fixed (see
// benchmarks/README.md).
func runStats(workload, reportPath string, runs, warmups int, basis benchlab.MetricBasis, disableRetry, jsonOut bool) error {
	all := benchlab.Workloads()
	names := selectWorkloads(all, workload)
	if len(names) == 0 {
		return fmt.Errorf("unknown workload %q (available: %s)", workload, strings.Join(sortedKeys(all), ", "))
	}

	results := map[string]*benchlab.WorkloadStats{}
	attention := false
	for _, name := range names {
		ws, err := spawnWorker(name, runs, warmups, disableRetry)
		if err != nil {
			return fmt.Errorf("measuring %s: %w", name, err)
		}
		results[name] = ws
		if ws.Status != "PASS" {
			attention = true
		}
		s := ws.BasisStats(basis)
		fmt.Fprintf(os.Stderr, "%-24s runs=%-3d median=%-8s p95=%-8s %d/%d %s\n",
			name, ws.Runs, s.Median.Round(time.Millisecond), s.P95.Round(time.Millisecond), ws.PassCount, ws.Runs, ws.Status)
	}

	out := os.Stdout
	if reportPath != "" {
		f, err := os.Create(reportPath)
		if err != nil {
			return err
		}
		defer f.Close()
		out = f
	}

	meta := benchlab.StatsReportMeta{
		Runs:         runs,
		Warmups:      warmups,
		MetricBasis:  basis,
		DisableRetry: disableRetry,
		GeneratedAt:  time.Now(),
	}
	if jsonOut {
		if err := benchlab.WriteStatsJSON(out, meta, results); err != nil {
			return err
		}
	} else {
		benchlab.WriteStatsReport(out, meta, results)
	}

	if attention {
		return fmt.Errorf("one or more workloads did not pass on every measured run — see report for FAIL/FLAKY detail")
	}
	return nil
}

// spawnWorker re-executes this same binary (os.Args[0], valid whether
// invoked via `go run` or a built binary — go run produces a real,
// directly-executable temp binary path there) with -internal-worker set to
// exactly one workload, so that workload's peak-RSS measurement lives in
// its own dedicated process rather than sharing one process's
// never-resetting VmHWM high-water mark with every other workload.
func spawnWorker(workload string, runs, warmups int, disableRetry bool) (*benchlab.WorkloadStats, error) {
	args := []string{
		"-internal-worker", workload,
		"-runs", strconv.Itoa(runs),
		"-warmups", strconv.Itoa(warmups),
	}
	if disableRetry {
		args = append(args, "-retry-disabled-comparison")
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var ws benchlab.WorkloadStats
	if err := json.Unmarshal(out, &ws); err != nil {
		return nil, fmt.Errorf("decoding worker output: %w", err)
	}
	return &ws, nil
}

// runInternalWorker is the subprocess entry point spawned by spawnWorker.
// It measures exactly one workload and prints its WorkloadStats as JSON to
// stdout. Exit code is nonzero only for a hard error (unknown workload,
// engine/config failure) — a FAIL/FLAKY correctness result is a valid,
// successfully measured outcome, not a process failure, and still exits 0.
func runInternalWorker(workload string, runs, warmups int, disableRetry bool) error {
	site, ok := benchlab.Workloads()[workload]
	if !ok {
		return fmt.Errorf("unknown workload %q", workload)
	}
	sameOrigin := !strings.Contains(workload, "multi-host")

	// Generous per-iteration budget: w10-chaos's worst-case retry backoff
	// (3 retries, exponential, up to ~2s each pre-jitter-cap) plus overhead
	// comfortably fits in 5s; scaling by runs+warmups keeps -runs/-warmups
	// overrides from being silently truncated by a fixed timeout.
	budget := time.Duration(runs+warmups) * 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	ws, err := benchlab.RunMany(ctx, site, sameOrigin, benchlab.RunOptions{DisableRetry: disableRetry}, warmups, runs)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(ws)
}

// runOracle is the original single-run report: kept byte-for-byte as it
// was before repeated-run statistics existed, reachable via -runs 1, so
// existing scripts/CI pinned to that exact output aren't broken by the new
// default.
func runOracle(workload, reportPath string, disableRetry bool) error {
	all := benchlab.Workloads()
	selected := map[string]*benchlab.Site{}
	if workload != "" {
		site, ok := all[workload]
		if !ok {
			return fmt.Errorf("unknown workload %q (available: %s)", workload, strings.Join(sortedKeys(all), ", "))
		}
		selected[workload] = site
	} else {
		selected = all
	}

	results := map[string]*benchlab.Result{}
	failed := false
	for _, name := range sortedKeys(selected) {
		site := selected[name]
		sameOrigin := !strings.Contains(name, "multi-host")
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		result, err := benchlab.Run(ctx, site, sameOrigin, benchlab.RunOptions{DisableRetry: disableRetry})
		cancel()
		if err != nil {
			return fmt.Errorf("running %s: %w", name, err)
		}
		if !result.Passed() {
			failed = true
		}
		results[name] = result
		fmt.Fprintf(os.Stderr, "%-24s %8s  %s\n", name, result.Duration.Round(time.Millisecond), passLabel(result))
	}

	out := os.Stdout
	if reportPath != "" {
		f, err := os.Create(reportPath)
		if err != nil {
			return err
		}
		defer f.Close()
		out = f
	}
	if disableRetry {
		fmt.Fprintf(out, "# NOT the production default — retries disabled\n\n")
		fmt.Fprintf(out, "This report exists only for apples-to-apples raw crawl-speed comparison\n")
		fmt.Fprintf(out, "against tools that don't retry 5xx/429 by default. chcrawl's actual default\n")
		fmt.Fprintf(out, "behavior (3 retries, exponential backoff, 429+5xx retryable) is unchanged —\n")
		fmt.Fprintf(out, "see the normal `chcrawl-bench` report for that leaderboard.\n\n")
	}
	benchlab.WriteReport(out, results)

	if failed {
		return fmt.Errorf("one or more workloads did not match their oracle — see report for details")
	}
	return nil
}

// runCompetitorBench runs the full statistically-hardened comparison:
// chcrawl (as an external subprocess, same as the other three — the real
// CLI invocation every tool is judged by) plus katana/hakrawler/gospider,
// warmups+runs times each, against every comparison-eligible workload.
// Reuses buildChcrawlBinary, benchlab.ChcrawlTool/ExternalTools, and
// compareMaxDepth from runCompare — no separate tool definitions or depth
// policy for this mode.
func runCompetitorBench(workload, reportPath string, runs, warmups int, jsonOut bool) error {
	binPath, cleanup, err := buildChcrawlBinary()
	if err != nil {
		return fmt.Errorf("building chcrawl binary: %w", err)
	}
	defer cleanup()

	all := benchlab.Workloads()
	selected := map[string]*benchlab.Site{}
	if workload != "" {
		site, ok := all[workload]
		if !ok {
			return fmt.Errorf("unknown workload %q (available: %s)", workload, strings.Join(sortedKeys(all), ", "))
		}
		if strings.Contains(workload, "multi-host") {
			return fmt.Errorf("%q has multi-host scope semantics that aren't comparable across tools; -competitor-bench excludes it", workload)
		}
		selected[workload] = site
	} else {
		for name, site := range all {
			if strings.Contains(name, "multi-host") {
				continue
			}
			selected[name] = site
		}
	}

	tools := append([]benchlab.Tool{benchlab.ChcrawlTool(binPath)}, benchlab.ExternalTools()...)

	results := map[string]map[string]*benchlab.CompetitorStats{}
	for _, name := range sortedKeys(selected) {
		results[name] = map[string]*benchlab.CompetitorStats{}
		for _, t := range tools {
			ctx := context.Background()
			cs := benchlab.RunCompetitorMany(ctx, selected[name], compareMaxDepth, warmups, runs, t)
			results[name][t.Name] = cs
			if !cs.Available {
				fmt.Fprintf(os.Stderr, "%-24s %-10s NOT INSTALLED\n", name, t.Name)
				continue
			}
			fmt.Fprintf(os.Stderr, "%-24s %-10s runs=%-3d median=%-9s p95=%-9s min-found=%d/%d %s\n",
				name, t.Name, cs.Runs, cs.Duration.Median.Round(time.Millisecond), cs.Duration.P95.Round(time.Millisecond),
				cs.MinFound, cs.GroundTruthTotal, cs.Status)
		}
	}

	out := os.Stdout
	if reportPath != "" {
		f, err := os.Create(reportPath)
		if err != nil {
			return err
		}
		defer f.Close()
		out = f
	}

	meta := benchlab.CompetitorReportMeta{
		Runs:        runs,
		Warmups:     warmups,
		MaxDepth:    compareMaxDepth,
		GeneratedAt: time.Now(),
		Environment: benchlab.CaptureEnvironment(),
	}
	if jsonOut {
		return benchlab.WriteCompetitorJSON(out, meta, results)
	}
	benchlab.WriteCompetitorReport(out, meta, results)
	return nil
}

// compareMaxDepth is generous enough for every workload's synthetic graph
// to be fully traversed (the deepest, w2-deep-tree, is 25 pages), so
// coverage differences reflect discovery behavior, not a depth cutoff.
const compareMaxDepth = 40

// runCompare builds a throwaway chcrawl binary and scores it against
// katana/hakrawler/gospider (whichever are installed) on every
// comparison-eligible workload — every workload except the multi-host one,
// whose scope semantics are too tool-specific to score fairly (see
// benchlab.DiscoverablePaths). Every tool crawls the identical local target
// with no tool-specific advantage; where a tool's retry behavior against
// 5xx differs materially from chcrawl's (none of the three external tools
// expose a retry-count flag, so none appear to retry 5xx by default), that
// is a genuine behavioral difference between the tools being compared, not
// something this harness normalizes away.
func runCompare(workload, reportPath string) error {
	binPath, cleanup, err := buildChcrawlBinary()
	if err != nil {
		return fmt.Errorf("building chcrawl binary for comparison: %w", err)
	}
	defer cleanup()

	all := benchlab.Workloads()
	selected := map[string]*benchlab.Site{}
	if workload != "" {
		site, ok := all[workload]
		if !ok {
			return fmt.Errorf("unknown workload %q (available: %s)", workload, strings.Join(sortedKeys(all), ", "))
		}
		if strings.Contains(workload, "multi-host") {
			return fmt.Errorf("%q has multi-host scope semantics that aren't comparable across tools; -compare excludes it", workload)
		}
		selected[workload] = site
	} else {
		for name, site := range all {
			if strings.Contains(name, "multi-host") {
				continue
			}
			selected[name] = site
		}
	}

	tools := append([]benchlab.Tool{benchlab.ChcrawlTool(binPath)}, benchlab.ExternalTools()...)
	toolOrder := make([]string, len(tools))
	for i, t := range tools {
		toolOrder[i] = t.Name
	}

	byWorkload := map[string]map[string]*benchlab.CompareResult{}
	for _, name := range sortedKeys(selected) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		results := benchlab.RunComparison(ctx, selected[name], compareMaxDepth, tools)
		cancel()
		byWorkload[name] = results
		for _, t := range toolOrder {
			r := results[t]
			if !r.Available {
				fmt.Fprintf(os.Stderr, "%-24s %-12s not installed\n", name, t)
				continue
			}
			fmt.Fprintf(os.Stderr, "%-24s %-12s %3d/%-3d found (%.0f%%)  %s\n",
				name, t, r.Found, r.Total, r.Recall(), r.Duration.Round(time.Millisecond))
		}
	}

	out := os.Stdout
	if reportPath != "" {
		f, err := os.Create(reportPath)
		if err != nil {
			return err
		}
		defer f.Close()
		out = f
	}
	benchlab.WriteCompareReport(out, byWorkload, toolOrder)
	return nil
}

// buildChcrawlBinary compiles ./cmd/chcrawl into a temp file so it can be
// run as a subprocess through the same Tool interface as the external
// crawlers it's being compared against.
func buildChcrawlBinary() (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "chcrawl-bench-bin-*")
	if err != nil {
		return "", nil, err
	}
	binPath := f.Name()
	f.Close()
	os.Remove(binPath) // go build wants to create it itself

	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/chcrawl")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", nil, err
	}
	return binPath, func() { os.Remove(binPath) }, nil
}

func passLabel(r *benchlab.Result) string {
	if r.Passed() {
		return "PASS"
	}
	return "FAIL"
}

func selectWorkloads(all map[string]*benchlab.Site, workload string) []string {
	if workload == "" {
		return sortedKeys(all)
	}
	if _, ok := all[workload]; !ok {
		return nil
	}
	return []string{workload}
}

func sortedKeys(m map[string]*benchlab.Site) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
