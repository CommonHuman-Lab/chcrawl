package benchlab

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
)

// MetricBasis selects which per-sample series drives a StatsReport's
// headline columns. Both bases come from the same measured runs against the
// real retry.Policy — this only re-slices which series is presented.
type MetricBasis string

const (
	// BasisDuration is the production number: wall-clock time including retry backoff. Default.
	BasisDuration MetricBasis = "duration"
	// BasisActiveWall is engine time with measured retry backoff subtracted — NOT CPU time,
	// NOT a production number. Isolates crawl-engine performance from resilience-policy waiting.
	BasisActiveWall MetricBasis = "active_wall"
)

// BasisStats returns ws.Duration or ws.ActiveWall depending on basis, exported
// so callers can stay consistent with the report without duplicating the selection.
func (ws *WorkloadStats) BasisStats(basis MetricBasis) MetricStats {
	if basis == BasisActiveWall {
		return ws.ActiveWall
	}
	return ws.Duration
}

// StatsReportMeta carries the run parameters that apply to every workload
// in a report, so the report is self-describing without cross-referencing
// how it was invoked.
type StatsReportMeta struct {
	Runs         int         `json:"runs"`
	Warmups      int         `json:"warmups"`
	MetricBasis  MetricBasis `json:"metric_basis"`
	DisableRetry bool        `json:"disable_retry"`
	GeneratedAt  time.Time   `json:"generated_at"`
}

// WriteStatsJSON writes the full machine-readable report, including every
// collected sample so downstream tooling can recompute derived statistics.
func WriteStatsJSON(w io.Writer, meta StatsReportMeta, results map[string]*WorkloadStats) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Meta      StatsReportMeta           `json:"meta"`
		Workloads map[string]*WorkloadStats `json:"workloads"`
	}{meta, results})
}

// WriteStatsReport renders the human-readable multi-run report: a headline
// table in meta.MetricBasis, a full per-workload stats breakdown for both
// bases, and a methodology section.
func WriteStatsReport(w io.Writer, meta StatsReportMeta, results map[string]*WorkloadStats) {
	names := make([]string, 0, len(results))
	for name := range results {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Fprintf(w, "# chcrawl benchmark report\n\n")
	fmt.Fprintf(w, "```text\n")
	fmt.Fprintf(w, "CHCrawl Benchmark\n")
	fmt.Fprintf(w, "Environment:  127.0.0.1-only, no external network\n")
	fmt.Fprintf(w, "Generated:    %s\n", meta.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "Runs:         %d\n", meta.Runs)
	fmt.Fprintf(w, "Warmups:      %d (discarded, not in statistics)\n", meta.Warmups)
	if meta.DisableRetry {
		fmt.Fprintf(w, "Retry policy: DISABLED — NOT the production default, comparison-only\n")
	} else {
		fmt.Fprintf(w, "Retry policy: production default (unchanged)\n")
	}
	fmt.Fprintf(w, "Metric basis: %s\n", basisLabel(meta.MetricBasis))
	fmt.Fprintf(w, "```\n\n")

	fmt.Fprintf(w, "## Headline (%s)\n\n", basisLabel(meta.MetricBasis))
	fmt.Fprintf(w, "| workload | runs | median | p95 | p99 | rps (median) | backoff (median) | active (median) | correctness | status |\n")
	fmt.Fprintf(w, "|---|---:|---:|---:|---:|---:|---:|---:|---|---|\n")
	var attention []string
	for _, name := range names {
		ws := results[name]
		s := ws.BasisStats(meta.MetricBasis)
		if ws.Status != "PASS" {
			attention = append(attention, name)
		}
		medianBackoff, medianActive := medianBackoffActive(ws)
		fmt.Fprintf(w, "| %s | %d | %s | %s | %s | %.1f | %s | %s | %d/%d | %s |\n",
			name, ws.Runs, fmtDur(s.Median), fmtDur(s.P95), fmtDur(s.P99),
			ws.MedianRPS, fmtDur(medianBackoff), fmtDur(medianActive),
			ws.PassCount, ws.Runs, ws.Status)
	}
	fmt.Fprintf(w, "\n")

	fmt.Fprintf(w, "w10-chaos in particular: wall time (duration median above, production\n")
	fmt.Fprintf(w, "default) is dominated by deliberate retry backoff against a permanently\n")
	fmt.Fprintf(w, "failing endpoint, not crawler execution time — its active (median) column\n")
	fmt.Fprintf(w, "is the actual engine work: milliseconds, not seconds. Both numbers are\n")
	fmt.Fprintf(w, "real and both are reported; neither is hidden.\n\n")

	fmt.Fprintf(w, "## Full statistics\n\n")
	for _, name := range names {
		ws := results[name]
		fmt.Fprintf(w, "### %s\n\n", name)
		fmt.Fprintf(w, "| basis | min | median | mean | p90 | p95 | p99 | max | stddev |\n")
		fmt.Fprintf(w, "|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
		writeMetricRow(w, "duration (wall, incl. backoff)", ws.Duration)
		writeMetricRow(w, "active_wall (excl. backoff)", ws.ActiveWall)
		fmt.Fprintf(w, "\n")
		fmt.Fprintf(w, "requests/sec (median): %.1f · peak RSS (median): %s · peak RSS (max): %s · correctness: %d/%d (%s)\n\n",
			ws.MedianRPS, fmtBytes(ws.MedianPeakRSSBytes), fmtBytes(ws.MaxPeakRSSBytes), ws.PassCount, ws.Runs, ws.Status)
	}

	if len(attention) > 0 {
		fmt.Fprintf(w, "## Correctness failures\n\n")
		fmt.Fprintf(w, "The following workloads did not pass on every measured run. FLAKY means\n")
		fmt.Fprintf(w, "some runs matched the oracle and some didn't — a real, non-deterministic\n")
		fmt.Fprintf(w, "discrepancy, not something to average away.\n\n")
		for _, name := range attention {
			ws := results[name]
			fmt.Fprintf(w, "### %s — %s (%d/%d passed)\n\n", name, ws.Status, ws.PassCount, ws.Runs)
			for i, diffs := range ws.FailureExamples {
				fmt.Fprintf(w, "Example failure %d:\n", i+1)
				for _, d := range diffs {
					fmt.Fprintf(w, "- %s\n", d)
				}
			}
			fmt.Fprintf(w, "\n")
		}
	}

	writeMethodology(w)
}

func writeMetricRow(w io.Writer, label string, s MetricStats) {
	fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
		label, fmtDur(s.Min), fmtDur(s.Median), fmtDur(s.Mean), fmtDur(s.P90),
		fmtDur(s.P95), fmtDur(s.P99), fmtDur(s.Max), fmtDur(s.StdDev))
}

// medianBackoffActive recomputes the pair directly from the samples rather
// than from the independently-sorted duration/active_wall MetricStats, so
// backoff+active always come from the same observed run.
func medianBackoffActive(ws *WorkloadStats) (backoff, active time.Duration) {
	n := len(ws.Samples)
	if n == 0 {
		return 0, 0
	}
	sorted := append([]Sample(nil), ws.Samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Duration < sorted[j].Duration })
	mid := sorted[n/2]
	return mid.Backoff, mid.ActiveWall
}

func basisLabel(b MetricBasis) string {
	if b == BasisActiveWall {
		return "active_wall — engine time with measured retry backoff excluded; NOT a production number, NOT CPU time"
	}
	return "duration — production wall-clock time, includes retry backoff exactly as a real crawl would experience it"
}

func fmtDur(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}

func fmtBytes(b uint64) string {
	return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
}

func writeMethodology(w io.Writer) {
	fmt.Fprintf(w, "## Methodology\n\n")
	fmt.Fprintf(w, "- **Percentiles** use the nearest-rank method (`ceil(p/100 * n)`, "+
		"1-indexed) computed from the individual per-run samples, never from "+
		"an aggregate. With the default 30 runs, P99's nearest rank is the "+
		"sample count itself, i.e. P99 == Max — an honest property of "+
		"small-N tail estimation, not a bug; resolving a true P99 needs "+
		"hundreds of runs, which this suite intentionally does not attempt.\n")
	fmt.Fprintf(w, "- **Standard deviation** is sample stddev (n-1 denominator).\n")
	fmt.Fprintf(w, "- **active_wall** is wall-clock duration minus measured retry backoff for "+
		"that same run. It is NOT CPU time. It is exact only when at most one "+
		"fetch is retrying at a time; concurrent overlapping retries can make "+
		"cumulative backoff exceed wall-clock duration, in which case "+
		"active_wall floors at 0 rather than going negative.\n")
	fmt.Fprintf(w, "- **Retry jitter is not eliminated.** Backoff delays draw from the shared "+
		"`math/rand` source, exactly as a production crawl would. Running many "+
		"iterations back-to-back samples the real distribution of backoff "+
		"delays rather than a single lucky or unlucky draw.\n")
	fmt.Fprintf(w, "- **Warmups** run the full real path (fresh servers, fresh engine) and are "+
		"discarded entirely, including from correctness accounting. For an "+
		"ahead-of-time-compiled Go binary there's no JIT warm-up to amortize; "+
		"warmups here mainly let OS-level TCP/port churn and the Go GC pacer "+
		"settle before measurement starts. Their effect is smaller than for a "+
		"JIT'd runtime — included anyway since it's cheap and can only help.\n")
	fmt.Fprintf(w, "- **Peak RSS** (`VmHWM` from `/proc/self/status`) is a process-lifetime "+
		"high-water mark that never resets. Reading it after several workloads "+
		"share one process silently inflates every lighter workload that runs "+
		"after a heavier one — confirmed on this suite before this fix (w7's "+
		"~51MB peak was still being reported, unchanged, for w8 and w9, which "+
		"individually use far less). Each workload's statistics are now "+
		"collected in a dedicated subprocess (see cmd/chcrawl-bench) so its "+
		"peak RSS is genuinely its own, not inherited from an earlier workload.\n")
	fmt.Fprintf(w, "- **Isolation**: every measured sample starts fresh local httptest servers "+
		"and a fresh engine (no shared dedup/frontier state across iterations); "+
		"server start/stop and `runtime.GC()` baseline calls happen outside "+
		"the timed region. DNS and filesystem-cache effects don't apply — "+
		"targets are the literal IP 127.0.0.1 with no disk I/O on the crawl "+
		"path. Subprocess spawn overhead (one per workload) also happens "+
		"outside every timed region.\n")
	fmt.Fprintf(w, "- **Correctness** is checked on every measured run against the same Oracle "+
		"used by the single-run mode (see oracle.go) — never only the first. "+
		"See \"Correctness failures\" above for any workload that didn't pass "+
		"100%%.\n")
	fmt.Fprintf(w, "- **100%% local**: every workload is served from a fresh `httptest.Server` "+
		"bound to 127.0.0.1; nothing in this report can reach the network.\n")
}
