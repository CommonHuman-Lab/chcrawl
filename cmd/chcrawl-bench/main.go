// Command chcrawl-bench runs the local deterministic benchmark workloads
// in internal/benchlab and reports timing, throughput, memory, and
// discovery-correctness for each. Everything it touches is bound to
// 127.0.0.1 — no Docker, no external network, no public websites.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
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
	reportPath := fs.String("report", "", "write a markdown report to this path (default: stdout)")
	compare := fs.Bool("compare", false, "score chcrawl against katana/hakrawler/gospider (any not installed are reported, not required) instead of the oracle correctness run")
	retryDisabledComparison := fs.Bool("retry-disabled-comparison", false,
		"run the oracle suite with retries OFF, for apples-to-apples raw crawl-speed comparison against tools that don't retry 5xx by default — NOT the production default, reported separately, never merged with the normal run")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *compare {
		return runCompare(*workload, *reportPath)
	}

	return runOracle(*workload, *reportPath, *retryDisabledComparison)
}

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

// compareMaxDepth is generous enough for every workload's synthetic graph
// to be fully traversed (the deepest, w2-deep-tree, is 25 pages), so
// coverage differences reflect discovery behavior, not a depth cutoff.
const compareMaxDepth = 40

// runCompare builds a throwaway chcrawl binary and scores it against
// katana/hakrawler/gospider (whichever are installed) on every
// comparison-eligible workload — every workload except the multi-host one,
// whose scope semantics are too tool-specific to score fairly (see
// benchlab.DiscoverablePaths).
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

func sortedKeys(m map[string]*benchlab.Site) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
