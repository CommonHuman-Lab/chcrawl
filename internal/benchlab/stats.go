package benchlab

import (
	"math"
	"sort"
	"time"
)

// Sample is one measured iteration of a workload: its own timing, retry
// telemetry, throughput, memory, and correctness outcome. Warmup iterations
// are never turned into Samples — see RunMany.
type Sample struct {
	Duration      time.Duration `json:"duration_ns"`
	ActiveWall    time.Duration `json:"active_wall_ns"` // Duration minus retry backoff for this run; see output.SummaryEvent.ActiveWallMS
	Backoff       time.Duration `json:"backoff_ns"`
	RetryAttempts int64         `json:"retry_attempts"`
	RequestsMade  int64         `json:"requests_made"`
	RPS           float64       `json:"rps"`
	PeakRSSBytes  uint64        `json:"peak_rss_bytes"`
	Passed        bool          `json:"passed"`
	Diffs         []string      `json:"diffs,omitempty"`
}

// MetricStats summarizes a set of duration samples. Every field is computed
// from the individual per-run samples directly, never from an
// already-aggregated total — an aggregate (e.g. sum-of-durations /
// sum-of-runs) cannot recover a distribution's percentiles or spread.
type MetricStats struct {
	Min    time.Duration `json:"min_ns"`
	Max    time.Duration `json:"max_ns"`
	Median time.Duration `json:"median_ns"`
	Mean   time.Duration `json:"mean_ns"`
	P90    time.Duration `json:"p90_ns"`
	P95    time.Duration `json:"p95_ns"`
	P99    time.Duration `json:"p99_ns"`
	StdDev time.Duration `json:"stddev_ns"`
}

// percentile returns the p-th percentile (0 < p <= 100) of sorted (which
// must already be sorted ascending) using the nearest-rank method:
// rank = ceil(p/100 * n), 1-indexed and clamped to [1, n]. This is the same
// method common load-testing tools (wrk, hey, ab) use, and is well-defined
// for any sample size — but with a small N (this suite defaults to 30),
// P99's nearest rank is n itself, i.e. P99 == Max. That's an honest
// property of small-sample tail estimation, not a defect in the method:
// resolving a true P99 requires hundreds of samples, and this benchmark
// intentionally stays fast enough to run in a CI-sized time budget instead.
func percentile(sorted []time.Duration, p float64) time.Duration {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	rank := int(math.Ceil(p / 100 * float64(n)))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return sorted[rank-1]
}

// sampleStdDev is the standard deviation using the n-1 (Bessel-corrected)
// denominator, the conventional choice when the samples are treated as a
// sample of a larger population of possible runs rather than the entire
// population. Returns 0 for fewer than 2 samples (variance is undefined).
func sampleStdDev(sorted []time.Duration, mean time.Duration) time.Duration {
	if len(sorted) < 2 {
		return 0
	}
	var sumSq float64
	meanF := float64(mean)
	for _, s := range sorted {
		d := float64(s) - meanF
		sumSq += d * d
	}
	return time.Duration(math.Sqrt(sumSq / float64(len(sorted)-1)))
}

func computeMetricStats(samples []time.Duration) MetricStats {
	if len(samples) == 0 {
		return MetricStats{}
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var sum time.Duration
	for _, s := range sorted {
		sum += s
	}
	mean := sum / time.Duration(len(sorted))

	return MetricStats{
		Min:    sorted[0],
		Max:    sorted[len(sorted)-1],
		Median: percentile(sorted, 50),
		Mean:   mean,
		P90:    percentile(sorted, 90),
		P95:    percentile(sorted, 95),
		P99:    percentile(sorted, 99),
		StdDev: sampleStdDev(sorted, mean),
	}
}

func medianUint64(vals []uint64) uint64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := append([]uint64(nil), vals...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

func maxUint64(vals []uint64) uint64 {
	var m uint64
	for _, v := range vals {
		if v > m {
			m = v
		}
	}
	return m
}

func medianFloat64(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	return sorted[len(sorted)/2]
}

// WorkloadStats is one workload's full repeated-run measurement: every
// measured sample plus the statistics and correctness verdict derived from
// them. Warmup iterations (Warmups) are run but never become Samples.
type WorkloadStats struct {
	Workload string `json:"workload"`
	Runs     int    `json:"runs"` // requested measured-run count (== len(Samples) on success)
	Warmups  int    `json:"warmups"`

	Samples []Sample `json:"samples"`

	Duration   MetricStats `json:"duration"`    // stats over each sample's wall-clock Duration (includes retry backoff)
	ActiveWall MetricStats `json:"active_wall"` // stats over each sample's ActiveWall (wall-clock minus retry backoff)

	MedianRPS          float64 `json:"median_rps"`
	MedianPeakRSSBytes uint64  `json:"median_peak_rss_bytes"`
	MaxPeakRSSBytes    uint64  `json:"max_peak_rss_bytes"`

	// PassCount/Status reflect correctness across every measured sample —
	// never just the first. Status is "PASS" (all runs matched the oracle),
	// "FAIL" (none did), or "FLAKY" (some did, some didn't): a real,
	// non-deterministic discrepancy that must never be averaged away.
	PassCount int    `json:"pass_count"`
	Status    string `json:"status"`
	// FailureExamples holds up to maxFailureExamples distinct diff sets
	// from failing samples, for debugging a FAIL/FLAKY workload without
	// dumping every failing iteration's full diff list.
	FailureExamples [][]string `json:"failure_examples,omitempty"`
}

const maxFailureExamples = 3

// finalize computes every derived field from ws.Samples. Called once after
// all measured samples for a workload have been collected.
func (ws *WorkloadStats) finalize() {
	n := len(ws.Samples)
	ws.Runs = n

	durations := make([]time.Duration, n)
	actives := make([]time.Duration, n)
	rpsVals := make([]float64, n)
	rssVals := make([]uint64, n)
	pass := 0
	for i, s := range ws.Samples {
		durations[i] = s.Duration
		actives[i] = s.ActiveWall
		rpsVals[i] = s.RPS
		rssVals[i] = s.PeakRSSBytes
		if s.Passed {
			pass++
		} else if len(ws.FailureExamples) < maxFailureExamples {
			ws.FailureExamples = append(ws.FailureExamples, s.Diffs)
		}
	}

	ws.Duration = computeMetricStats(durations)
	ws.ActiveWall = computeMetricStats(actives)
	ws.MedianRPS = medianFloat64(rpsVals)
	ws.MedianPeakRSSBytes = medianUint64(rssVals)
	ws.MaxPeakRSSBytes = maxUint64(rssVals)

	ws.PassCount = pass
	switch {
	case n == 0:
		ws.Status = "FAIL"
	case pass == n:
		ws.Status = "PASS"
	case pass == 0:
		ws.Status = "FAIL"
	default:
		ws.Status = "FLAKY"
	}
}
