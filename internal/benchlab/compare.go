package benchlab

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// CompareResult is one tool's measured coverage against a workload's
// DiscoverablePaths ground truth. Malformed/MissingPaths/ExtraPaths are
// each a distinct failure mode, not different names for the same thing:
// a malformed entry never became a comparable path at all (couldn't be
// url.Parse'd); a missing path is a real ground-truth page the tool never
// reported; an extra path is a real path the tool reported that isn't in
// the ground truth (could be a genuine over-discovery, or the tool
// normalizing a URL differently than the oracle does — this harness
// doesn't try to tell those apart, since what counts as "just
// normalization" vs. "a different page" is tool-specific).
type CompareResult struct {
	Tool         string
	Available    bool
	Duration     time.Duration
	Found        int      // ground-truth paths the tool actually reported
	Total        int      // size of the ground-truth set
	Extra        int      // paths reported that aren't in the ground truth
	Malformed    int      // strings the tool reported that don't even parse as a URL
	MissingPaths []string // ground-truth paths never reported — sorted
	ExtraPaths   []string // reported paths not in ground truth — sorted
	Err          error
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

// RunComparison runs every tool against its own fresh instance of site's
// local target — not one shared instance, since that would let one tool's
// requests change server-side state (e.g. a PageSpec.FailFirstNRequests
// counter) before the next tool runs. Only single-host workloads are
// supported — see DiscoverablePaths.
func RunComparison(ctx context.Context, site *Site, maxDepth int, tools []Tool) map[string]*CompareResult {
	ground := site.DiscoverablePaths(maxDepth, true)

	results := make(map[string]*CompareResult, len(tools))
	for _, t := range tools {
		servers := site.Start()
		results[t.Name] = runOneTool(ctx, t, servers.SeedURL, maxDepth, ground)
		servers.Close()
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

	// See competitor.go's runCompetitorOnce: chcrawl's JSONL record stream
	// only exists via -output, never on stdout.
	var recordFile string
	if t.OutputToFile {
		f, ferr := os.CreateTemp("", "benchlab-chcrawl-out-*.jsonl")
		if ferr == nil {
			recordFile = f.Name()
			f.Close()
			cmd.Args = append(cmd.Args, "-output", recordFile)
			defer os.Remove(recordFile)
		}
	}

	start := time.Now()
	err := cmd.Run()
	r.Duration = time.Since(start)
	// A nonzero exit doesn't invalidate whatever the tool already printed,
	// so output is still parsed and scored even when err != nil.
	if err != nil {
		r.Err = err
	}

	records := stdout.Bytes()
	if recordFile != "" {
		if b, rerr := os.ReadFile(recordFile); rerr == nil {
			records = b
		}
	}

	found := t.Parse(seedURL, records)
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
			r.Malformed++
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
			r.ExtraPaths = append(r.ExtraPaths, p)
		}
	}
	for p := range ground {
		if !paths[p] {
			r.MissingPaths = append(r.MissingPaths, p)
		}
	}
	sort.Strings(r.ExtraPaths)
	sort.Strings(r.MissingPaths)
}
