package benchlab

import (
	"bytes"
	"context"
	"net/url"
	"strings"
	"time"
)

// CompareResult is one tool's measured coverage against a workload's
// DiscoverablePaths ground truth.
type CompareResult struct {
	Tool      string
	Available bool
	Duration  time.Duration
	Found     int // ground-truth paths the tool actually reported
	Total     int // size of the ground-truth set
	Extra     int // paths reported that aren't in the ground truth
	Err       error
}

// Recall is the percentage of genuinely discoverable pages the tool found.
func (r CompareResult) Recall() float64 {
	if r.Total == 0 {
		return 0
	}
	return float64(r.Found) / float64(r.Total) * 100
}

// compareTimeout bounds each individual tool invocation so one hung or
// slow external process can't stall the rest of the comparison suite.
const compareTimeout = 30 * time.Second

// RunComparison starts site's local target once, then runs every tool
// against it and scores each one's reported URLs against the same
// DiscoverablePaths ground truth. Only single-host workloads are
// supported — see DiscoverablePaths.
func RunComparison(ctx context.Context, site *Site, maxDepth int, tools []Tool) map[string]*CompareResult {
	servers := site.Start()
	defer servers.Close()

	ground := site.DiscoverablePaths(maxDepth, true)

	results := make(map[string]*CompareResult, len(tools))
	for _, t := range tools {
		results[t.Name] = runOneTool(ctx, t, servers.SeedURL, maxDepth, ground)
	}
	return results
}

func runOneTool(ctx context.Context, t Tool, seedURL string, maxDepth int, ground map[string]bool) *CompareResult {
	r := &CompareResult{Tool: t.Name, Total: len(ground)}
	if !t.Available() {
		return r
	}
	r.Available = true

	runCtx, cancel := context.WithTimeout(ctx, compareTimeout)
	defer cancel()

	cmd := t.Build(runCtx, t.Binary, seedURL, maxDepth)
	if t.Stdin != nil {
		cmd.Stdin = strings.NewReader(t.Stdin(seedURL))
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	start := time.Now()
	err := cmd.Run()
	r.Duration = time.Since(start)
	// A crawler CLI exiting non-zero (e.g. on a redirect loop it gave up
	// on) doesn't invalidate whatever it already printed to stdout, so
	// output is still parsed and scored even when err != nil.
	if err != nil {
		r.Err = err
	}

	found := t.Parse(seedURL, stdout.Bytes())
	scoreAgainstGround(r, seedURL, found, ground)
	return r
}

func scoreAgainstGround(r *CompareResult, seedURL string, found, ground map[string]bool) {
	base, err := url.Parse(seedURL)
	if err != nil {
		return
	}
	paths := map[string]bool{}
	for u := range found {
		pu, err := url.Parse(u)
		if err != nil {
			continue
		}
		if pu.Host != base.Host {
			continue // out of scope
		}
		p := pu.Path
		if p == "" {
			p = "/"
		}
		paths[p] = true
	}
	for p := range paths {
		if ground[p] {
			r.Found++
		} else {
			r.Extra++
		}
	}
}
