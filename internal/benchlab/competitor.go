package benchlab

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// CompetitorSample is one measured iteration of one Tool against one
// workload. ActiveWall/RetryBackoff/RetryAttempts are nil unless the tool
// exposes equivalent instrumentation (today, only chcrawl does; see
// parseChcrawlSummary) — never inferred for a tool that doesn't report one.
type CompetitorSample struct {
	Duration  time.Duration `json:"duration_ns"`
	Found     int           `json:"found"`
	Total     int           `json:"total"`
	Extra     int           `json:"extra"`
	Malformed int           `json:"malformed"` // reported strings that don't even parse as a URL
	// MissingPaths/ExtraPaths are this run's diff against ground truth — see
	// CompareResult's doc comment for what each category means.
	MissingPaths  []string       `json:"missing_paths,omitempty"`
	ExtraPaths    []string       `json:"extra_paths,omitempty"`
	Passed        bool           `json:"passed"` // Found == Total for this run
	PeakRSSBytes  uint64         `json:"peak_rss_bytes"`
	RSSAvailable  bool           `json:"rss_available"`
	RequestsMade  *int64         `json:"requests_made,omitempty"`
	ResponsesOK   *int64         `json:"responses_ok,omitempty"`
	BytesReceived *int64         `json:"bytes_received,omitempty"`
	ActiveWall    *time.Duration `json:"active_wall_ns,omitempty"`
	RetryBackoff  *time.Duration `json:"retry_backoff_ns,omitempty"`
	RetryAttempts *int64         `json:"retry_attempts,omitempty"`
	// ExitCode: 0 for a clean exit, the real code when the OS reports one,
	// -1 when the process didn't exit normally (killed, timed out, failed to start).
	ExitCode int    `json:"exit_code"`
	Err      string `json:"err,omitempty"`
}

// CompetitorStats is one Tool's full repeated-run measurement against one
// workload: every measured sample plus statistics computed from them.
type CompetitorStats struct {
	Tool             string `json:"tool"`
	Workload         string `json:"workload"`
	Available        bool   `json:"available"` // false = binary not on PATH; no samples collected
	Runs             int    `json:"runs"`
	Warmups          int    `json:"warmups"`
	GroundTruthTotal int    `json:"ground_truth_total"`

	Samples []CompetitorSample `json:"samples"`

	Duration MetricStats `json:"duration"`

	MedianRPS          float64 `json:"median_rps"` // see RPSIsApproximate
	RPSIsApproximate   bool    `json:"rps_is_approximate"`
	MedianPeakRSSBytes uint64  `json:"median_peak_rss_bytes"`
	MaxPeakRSSBytes    uint64  `json:"max_peak_rss_bytes"`
	RSSAvailable       bool    `json:"rss_available"`

	// MinFound/MaxFound are the worst/best discovery counts across all runs.
	// The correctness table reports MinFound so an intermittent miss stays
	// visible rather than smoothed away by an average.
	MinFound int `json:"min_found"`
	MaxFound int `json:"max_found"`

	PassCount int    `json:"pass_count"`
	Status    string `json:"status"` // PASS, FAIL, FLAKY, or UNAVAILABLE

	// EverMissingPaths is the union of every failing sample's missing paths.
	// MissingPathsConsistent is true when every failing sample missed the
	// exact same set (a deterministic failure) vs. varying by run (the
	// failure itself is flaky). Both are zero-valued when nothing ever failed.
	EverMissingPaths       []string `json:"ever_missing_paths,omitempty"`
	MissingPathsConsistent bool     `json:"missing_paths_consistent"`

	HasActiveWallInstrumentation bool `json:"has_active_wall_instrumentation"`
}

func (cs *CompetitorStats) finalize() {
	n := len(cs.Samples)
	cs.Runs = n
	if n == 0 {
		cs.Status = "UNAVAILABLE"
		return
	}

	durations := make([]time.Duration, n)
	rssVals := make([]uint64, n)
	rpsVals := make([]float64, n)
	pass := 0
	minFound, maxFound := cs.Samples[0].Found, cs.Samples[0].Found
	rssAvail := true
	for i, s := range cs.Samples {
		durations[i] = s.Duration
		rssVals[i] = s.PeakRSSBytes
		if !s.RSSAvailable {
			rssAvail = false
		}
		if s.Duration.Seconds() > 0 {
			if s.RequestsMade != nil {
				rpsVals[i] = float64(*s.RequestsMade) / s.Duration.Seconds()
			} else {
				// No raw request count exposed by this tool — Found is the
				// closest available proxy. See RPSIsApproximate.
				rpsVals[i] = float64(s.Found) / s.Duration.Seconds()
				cs.RPSIsApproximate = true
			}
		}
		if s.Found < minFound {
			minFound = s.Found
		}
		if s.Found > maxFound {
			maxFound = s.Found
		}
		if s.Passed {
			pass++
		}
		if s.ActiveWall != nil {
			cs.HasActiveWallInstrumentation = true
		}
	}

	cs.Duration = computeMetricStats(durations)
	cs.MedianRPS = medianFloat64(rpsVals)
	cs.MedianPeakRSSBytes = medianUint64(rssVals)
	cs.MaxPeakRSSBytes = maxUint64(rssVals)
	cs.RSSAvailable = rssAvail
	cs.MinFound = minFound
	cs.MaxFound = maxFound
	cs.PassCount = pass

	switch {
	case pass == n:
		cs.Status = "PASS"
	case pass == 0:
		cs.Status = "FAIL"
	default:
		cs.Status = "FLAKY"
	}

	cs.EverMissingPaths, cs.MissingPathsConsistent = summarizeMissingPaths(cs.Samples)
}

// summarizeMissingPaths reports the union of every failing sample's missing
// paths, and whether every failing sample missed the exact same set.
func summarizeMissingPaths(samples []CompetitorSample) (union []string, consistent bool) {
	seen := map[string]bool{}
	var firstFailingSet map[string]bool
	consistent = true
	for _, s := range samples {
		if s.Passed {
			continue
		}
		this := make(map[string]bool, len(s.MissingPaths))
		for _, p := range s.MissingPaths {
			this[p] = true
			if !seen[p] {
				seen[p] = true
				union = append(union, p)
			}
		}
		if firstFailingSet == nil {
			firstFailingSet = this
			continue
		}
		if len(this) != len(firstFailingSet) {
			consistent = false
			continue
		}
		for p := range this {
			if !firstFailingSet[p] {
				consistent = false
			}
		}
	}
	if firstFailingSet == nil {
		consistent = false // no failures at all — "consistent" is meaningless, not true
	}
	sort.Strings(union)
	return union, consistent
}

// competitorPerCallTimeout bounds a single tool invocation so a hung process
// can't stall the whole suite. Sized above the slowest real invocation
// observed locally (katana on the 25-page chain workload took ~29s).
const competitorPerCallTimeout = 60 * time.Second

// RepetitionOrder records one warmup or measured repetition's actual tool
// invocation order, from RunCompetitorInterleaved.
type RepetitionOrder struct {
	Measured  bool     `json:"measured"`
	ToolOrder []string `json:"tool_order"`
}

// InterleavedRunLog is the exact randomized schedule RunCompetitorInterleaved
// used, so a run is reproducible (same Seed reproduces the same order) and
// auditable (the real per-repetition order is on record, not just claimed).
type InterleavedRunLog struct {
	Seed        int64             `json:"seed"`
	Repetitions []RepetitionOrder `json:"repetitions"`
}

// RunCompetitorInterleaved runs every tool against site warmups+runs times
// each, shuffling the tool order independently per repetition instead of
// running one tool's full warmups+runs before the next — this spreads
// environmental drift (thermal throttling, background load) evenly across
// tools instead of confounding it with tool identity. seed determines (and,
// via InterleavedRunLog, records) the exact shuffled order for
// reproducibility. An unavailable tool is still included in the shuffle so
// its absence doesn't perturb the others' timing.
func RunCompetitorInterleaved(ctx context.Context, site *Site, maxDepth, warmups, runs int, tools []Tool, seed int64) (map[string]*CompetitorStats, *InterleavedRunLog) {
	groundTotal := len(site.DiscoverablePaths(maxDepth, true))
	stats := make(map[string]*CompetitorStats, len(tools))
	available := make(map[string]bool, len(tools))
	for _, t := range tools {
		stats[t.Name] = &CompetitorStats{
			Tool: t.Name, Workload: site.Name, Warmups: warmups,
			GroundTruthTotal: groundTotal, Available: t.Available(),
		}
		available[t.Name] = stats[t.Name].Available
	}

	rng := rand.New(rand.NewSource(seed))
	log := &InterleavedRunLog{Seed: seed}

	runRepetition := func(measured bool) {
		perm := rng.Perm(len(tools))
		names := make([]string, len(tools))
		for i, idx := range perm {
			t := tools[idx]
			names[i] = t.Name
			if !available[t.Name] {
				continue
			}
			sample := runCompetitorOnce(ctx, site, maxDepth, t)
			if measured {
				stats[t.Name].Samples = append(stats[t.Name].Samples, sample)
			}
		}
		log.Repetitions = append(log.Repetitions, RepetitionOrder{Measured: measured, ToolOrder: names})
	}

	for i := 0; i < warmups; i++ {
		runRepetition(false)
	}
	for i := 0; i < runs; i++ {
		runRepetition(true)
	}

	for _, cs := range stats {
		cs.finalize()
	}
	return stats, log
}

func runCompetitorOnce(ctx context.Context, site *Site, maxDepth int, tool Tool) CompetitorSample {
	servers := site.Start()
	defer servers.Close()
	ground := site.DiscoverablePaths(maxDepth, true)

	runCtx, cancel := context.WithTimeout(ctx, competitorPerCallTimeout)
	defer cancel()

	cmd := tool.Build(runCtx, tool.Binary, servers.SeedURL, maxDepth)
	if tool.Stdin != nil {
		cmd.Stdin = strings.NewReader(tool.Stdin(servers.SeedURL))
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	// chcrawl's stdout is the human-readable progress view; its JSONL
	// record stream (what Parse/parseChcrawlSummary need) only exists on
	// disk, via -output. Route it through a temp file instead of stdout.
	var recordFile string
	if tool.OutputToFile {
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
	sample := CompetitorSample{Duration: time.Since(start), ExitCode: exitCodeOf(err)}
	if err != nil {
		// A nonzero exit doesn't invalidate whatever the tool already
		// printed — still parsed and scored, matching RunComparison.
		sample.Err = err.Error()
	}
	if rss, ok := childPeakRSSBytes(cmd); ok {
		sample.PeakRSSBytes = rss
		sample.RSSAvailable = true
	}

	records := stdout.Bytes()
	if recordFile != "" {
		if b, rerr := os.ReadFile(recordFile); rerr == nil {
			records = b
		}
	}

	found := tool.Parse(servers.SeedURL, records)
	r := &CompareResult{Total: len(ground)}
	scoreAgainstGround(r, servers.SeedURL, found, ground)
	sample.Found, sample.Total, sample.Extra, sample.Malformed = r.Found, r.Total, r.Extra, r.Malformed
	sample.MissingPaths, sample.ExtraPaths = r.MissingPaths, r.ExtraPaths
	sample.Passed = r.Found == r.Total

	if s, ok := parseChcrawlSummary(records); ok {
		sample.ActiveWall = &s.ActiveWall
		sample.RetryBackoff = &s.RetryBackoff
		sample.RetryAttempts = &s.RetryAttempts
		sample.RequestsMade = &s.RequestsMade
		sample.ResponsesOK = &s.ResponsesOK
		sample.BytesReceived = &s.BytesReceived
	}

	return sample
}

// exitCodeOf reports a subprocess's exit status from the error cmd.Run() returned.
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
