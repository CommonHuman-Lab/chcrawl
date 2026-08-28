package benchlab

import (
	"bytes"
	"context"
	"strings"
	"time"
)

// CompetitorSample is one measured iteration of one Tool against one
// workload. ActiveWall/RetryBackoff/RetryAttempts are nil unless the tool
// exposes equivalent instrumentation (today, only chcrawl does, via its
// own JSONL summary record) — never inferred or fabricated for a tool
// that doesn't report one; see parseChcrawlSummary.
type CompetitorSample struct {
	Duration      time.Duration  `json:"duration_ns"`
	Found         int            `json:"found"`
	Total         int            `json:"total"`
	Extra         int            `json:"extra"`
	Passed        bool           `json:"passed"` // Found == Total for this run
	PeakRSSBytes  uint64         `json:"peak_rss_bytes"`
	RSSAvailable  bool           `json:"rss_available"`
	RequestsMade  *int64         `json:"requests_made,omitempty"`
	ActiveWall    *time.Duration `json:"active_wall_ns,omitempty"`
	RetryBackoff  *time.Duration `json:"retry_backoff_ns,omitempty"`
	RetryAttempts *int64         `json:"retry_attempts,omitempty"`
	Err           string         `json:"err,omitempty"`
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

	// MinFound/MaxFound are the worst/best discovery counts observed
	// across all runs. The correctness table reports MinFound, not an
	// average, so an intermittent miss stays visible rather than smoothed
	// away by a run that happened to find everything.
	MinFound int `json:"min_found"`
	MaxFound int `json:"max_found"`

	PassCount int    `json:"pass_count"`
	Status    string `json:"status"` // PASS, FAIL, FLAKY, or UNAVAILABLE

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
				// No raw request count exposed by this tool — Found is
				// the closest available proxy (successful discoveries,
				// not total HTTP attempts). See RPSIsApproximate.
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
}

// competitorPerCallTimeout bounds a single tool invocation so a hung
// process can't stall the whole suite. Sized generously above the
// slowest real invocation observed locally (katana on the 25-page linear
// chain workload took ~29s).
const competitorPerCallTimeout = 60 * time.Second

// RunCompetitorMany runs tool against site warmups+runs times — each a
// fresh site.Start() and subprocess invocation — scoring every measured run
// against the same DiscoverablePaths ground truth RunComparison uses.
// Warmups are discarded entirely. If tool's binary isn't on PATH, Status is
// "UNAVAILABLE" rather than silently skipped.
func RunCompetitorMany(ctx context.Context, site *Site, maxDepth, warmups, runs int, tool Tool) *CompetitorStats {
	cs := &CompetitorStats{Tool: tool.Name, Workload: site.Name, Warmups: warmups}
	cs.GroundTruthTotal = len(site.DiscoverablePaths(maxDepth, true))
	if !tool.Available() {
		return cs
	}
	cs.Available = true

	for i := 0; i < warmups; i++ {
		runCompetitorOnce(ctx, site, maxDepth, tool)
	}
	for i := 0; i < runs; i++ {
		cs.Samples = append(cs.Samples, runCompetitorOnce(ctx, site, maxDepth, tool))
	}

	cs.finalize()
	return cs
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

	start := time.Now()
	err := cmd.Run()
	sample := CompetitorSample{Duration: time.Since(start)}
	if err != nil {
		// A nonzero exit (e.g. a tool giving up on a genuine redirect
		// loop) doesn't invalidate whatever it already printed — still
		// parsed and scored, matching RunComparison's behavior.
		sample.Err = err.Error()
	}
	if rss, ok := childPeakRSSBytes(cmd); ok {
		sample.PeakRSSBytes = rss
		sample.RSSAvailable = true
	}

	found := tool.Parse(servers.SeedURL, stdout.Bytes())
	r := &CompareResult{Total: len(ground)}
	scoreAgainstGround(r, servers.SeedURL, found, ground)
	sample.Found, sample.Total, sample.Extra = r.Found, r.Total, r.Extra
	sample.Passed = r.Found == r.Total

	if aw, bo, ra, reqs, ok := parseChcrawlSummary(stdout.Bytes()); ok {
		sample.ActiveWall = &aw
		sample.RetryBackoff = &bo
		sample.RetryAttempts = &ra
		sample.RequestsMade = &reqs
	}

	return sample
}
