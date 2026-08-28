package benchlab

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// ScaleReportMeta carries the run parameters and machine context for a
// large-scale report, so the report is self-describing.
type ScaleReportMeta struct {
	GeneratedAt               time.Time     `json:"generated_at"`
	Environment               Environment   `json:"environment"`
	ConcurrencyLevels         []int         `json:"concurrency_levels,omitempty"`
	ConcurrencyWorkloadFamily string        `json:"concurrency_workload_family,omitempty"`
	ConcurrencyWorkloadScale  int           `json:"concurrency_workload_scale,omitempty"`
	WallClockCeiling          time.Duration `json:"wall_clock_ceiling_ns"`
	PeakRSSCeilingBytes       uint64        `json:"peak_rss_ceiling_bytes"`
}

// ScaleReport is the full large-scale benchmark result: every family's
// ascending-scale sweep, the two fixed-size canonicalization demonstrations
// (reported separately, never merged into the scaling tables), and an
// optional concurrency sweep.
type ScaleReport struct {
	Meta                      ScaleReportMeta         `json:"meta"`
	Families                  []ScaleFamilyResult     `json:"families"`
	QueryCanonicalizationDemo *WorkloadStats          `json:"query_canonicalization_demo,omitempty"`
	DefaultBodyCapDemo        *WorkloadStats          `json:"default_body_cap_demo,omitempty"`
	ConcurrencySweep          *ConcurrencySweepResult `json:"concurrency_sweep,omitempty"`
	// ProfilingNotes holds hand-written pprof findings, keyed by family name,
	// populated manually only where a result showed a scaling discontinuity.
	ProfilingNotes map[string]string `json:"profiling_notes,omitempty"`
	// TopologyConclusion is hand-authored prose interpreting the S1/S1b/S1c
	// topology comparison — not mechanically derivable from the tables alone.
	TopologyConclusion string `json:"topology_conclusion,omitempty"`
}

// WriteScaleJSON writes the full machine-readable report, including every
// collected sample, not just summary statistics.
func WriteScaleJSON(w io.Writer, report *ScaleReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// WriteScaleReport renders the human-readable large-scale report.
func WriteScaleReport(w io.Writer, report *ScaleReport) {
	fmt.Fprintf(w, "# CHCrawl Scalability Benchmark\n\n")
	writeScaleEnvironment(w, report.Meta)
	writeScaleMethodology(w, report.Meta)

	for _, fam := range report.Families {
		fmt.Fprintf(w, "## %s\n\n", fam.Family)
		writeScaleFamilyTable(w, fam)
		writeScaleFamilySummary(w, fam)
		if note, ok := report.ProfilingNotes[fam.Family]; ok && note != "" {
			fmt.Fprintf(w, "**Profiling note:**\n\n%s\n\n", note)
		}
	}

	writeTopologyComparison(w, report)

	if report.QueryCanonicalizationDemo != nil {
		fmt.Fprintf(w, "## S3b: query-parameter-order canonicalization demo (non-default config)\n\n")
		fmt.Fprintf(w, "NOT part of the S3 scaling matrix above, and NOT chcrawl's production\n")
		fmt.Fprintf(w, "default. `SortQueryParams` is off by default, so `/q?a=1&b=2` and\n")
		fmt.Fprintf(w, "`/q?b=2&a=1` are genuinely distinct URLs to chcrawl out of the box — see\n")
		fmt.Fprintf(w, "the S3 table's variant forms, which use only default-canonicalized\n")
		fmt.Fprintf(w, "equivalences (trailing slash, fragment). This demo exists solely to show\n")
		fmt.Fprintf(w, "that query-order canonicalization *is* implemented and available behind an\n")
		fmt.Fprintf(w, "explicit opt-in, exactly like the existing retry-disabled-comparison mode.\n\n")
		writeSingleWorkloadTable(w, report.QueryCanonicalizationDemo)
	}

	if report.DefaultBodyCapDemo != nil {
		fmt.Fprintf(w, "## S4b: production-default MaxBodyBytes cap demo\n\n")
		fmt.Fprintf(w, "The S4 table above measures every body size (including 25MB) with\n")
		fmt.Fprintf(w, "MaxBodyBytes enlarged so no size is truncated. This row instead measures\n")
		fmt.Fprintf(w, "the same 25MB body at chcrawl's actual production-default cap (10MiB), to\n")
		fmt.Fprintf(w, "show the cap's real effect rather than only ever measuring around it.\n\n")
		writeSingleWorkloadTable(w, report.DefaultBodyCapDemo)
	}

	if report.ConcurrencySweep != nil {
		fmt.Fprintf(w, "## Concurrency scaling\n\n")
		writeConcurrencySweepTable(w, report.Meta, report.ConcurrencySweep)
	}

	writeScaleObservations(w, report)
	writeScaleLimitations(w, report)
}

func writeScaleEnvironment(w io.Writer, meta ScaleReportMeta) {
	e := meta.Environment
	fmt.Fprintf(w, "## Environment\n\n")
	fmt.Fprintf(w, "```text\n")
	fmt.Fprintf(w, "Generated:     %s\n", meta.GeneratedAt.Format(time.RFC3339))
	fmt.Fprintf(w, "OS / Arch:     %s / %s\n", e.OS, e.Arch)
	if e.KernelVersion != "" {
		fmt.Fprintf(w, "Kernel:        %s\n", e.KernelVersion)
	}
	if e.CPUModel != "" {
		fmt.Fprintf(w, "CPU:           %s\n", e.CPUModel)
	}
	fmt.Fprintf(w, "Logical CPUs:  %d\n", e.LogicalCPUs)
	if e.TotalRAMBytes > 0 {
		fmt.Fprintf(w, "RAM:           %.1f GB\n", float64(e.TotalRAMBytes)/(1024*1024*1024))
	}
	if e.GoVersion != "" {
		fmt.Fprintf(w, "Go version:    %s\n", e.GoVersion)
	}
	if e.GitCommit != "" {
		fmt.Fprintf(w, "Git commit:    %s\n", e.GitCommit)
	}
	fmt.Fprintf(w, "Practicality ceilings: wall-clock %s, peak RSS %s (a scale exceeding\n",
		meta.WallClockCeiling, fmtBytes(meta.PeakRSSCeilingBytes))
	fmt.Fprintf(w, "either on a single probe run is excluded, not force-run — see below)\n")
	fmt.Fprintf(w, "```\n\n")
}

func writeScaleMethodology(w io.Writer, meta ScaleReportMeta) {
	fmt.Fprintf(w, "## Methodology\n\n")
	fmt.Fprintf(w, "This suite reuses the exact same infrastructure as the small-workload\n")
	fmt.Fprintf(w, "(w1-w10) benchmark: `Site`/`PageSpec` synthetic targets served from fresh,\n")
	fmt.Fprintf(w, "127.0.0.1-only `httptest.Server`s, a pure-Go oracle computed from the same\n")
	fmt.Fprintf(w, "graph the target is built from (so target and oracle cannot drift apart),\n")
	fmt.Fprintf(w, "and the same `RunMany`/statistics/percentile machinery. w1-w10 are\n")
	fmt.Fprintf(w, "unchanged and continue to pass; this is an addition, not a replacement.\n\n")
	fmt.Fprintf(w, "- **Run-count tiers.** Exhaustive 30-run measurement is only practical at\n")
	fmt.Fprintf(w, "  the smallest scale in each family. Runs/warmups shrink by tier position\n")
	fmt.Fprintf(w, "  (ascending scale index within a family): 30/5, 15/3, 5/2, 3/1. This is a\n")
	fmt.Fprintf(w, "  fixed, documented policy, not a silent reduction — every row below states\n")
	fmt.Fprintf(w, "  its own actual Runs/Warmups explicitly.\n")
	fmt.Fprintf(w, "- **Practicality probe.** Before committing to the tiered measurement at a\n")
	fmt.Fprintf(w, "  given scale, one throwaway single-run probe checks wall-clock time and\n")
	fmt.Fprintf(w, "  peak RSS against the ceilings above. Exceeding either excludes that scale\n")
	fmt.Fprintf(w, "  *and every larger scale in the same family* (cost only grows from there),\n")
	fmt.Fprintf(w, "  with the actual probe numbers recorded as the reason — never a silent gap.\n")
	fmt.Fprintf(w, "- **Percentiles** use the nearest-rank method (`ceil(p/100 * n)`, "+
		"1-indexed), identical to the small-workload suite. At the smallest run tier "+
		"(3 runs), P95 and P99 both equal Max — an honest property of tiny-N tail "+
		"estimation, not a defect.\n")
	fmt.Fprintf(w, "- **Correctness** is checked on every measured run, never averaged away. A "+
		"scale reported PASS/FLAKY/FAIL means exactly what it does in the small-workload "+
		"suite (see stats_report.go's methodology) — a correctness failure invalidates "+
		"the performance number for that run, it is not hidden behind it.\n")
	fmt.Fprintf(w, "- **Peak RSS isolation.** Each (family, scale) measurement runs in its own "+
		"dedicated re-exec'd subprocess (`-internal-scale-worker`), exactly like the "+
		"small-workload suite's `-internal-worker` — so peak RSS is never inherited from "+
		"an earlier, heavier measurement sharing one process.\n")
	fmt.Fprintf(w, "- **Scaling ratios** compare only *consecutive measured* points within a "+
		"family (never a global fit across the whole range) and are labeled "+
		"sub-linear/roughly-linear/super-linear using a ±25%% band — a descriptive aid "+
		"for reading an already-computed ratio, not a statistical model. Two points "+
		"cannot establish linearity, and this report does not claim it from them.\n")
	fmt.Fprintf(w, "- **Per-family scale axis differs**, documented per section: S1/S3/S5/S6 "+
		"scale by unique-URL count; S2 scales by chain depth (capped at 10,000 — "+
		"see the S2 section); S4 scales by response body size in KB, with a fixed "+
		"link count, since body size (not URL count) is what it isolates.\n")
	fmt.Fprintf(w, "- **Profiling** was applied only where a scaling result actually showed "+
		"a discontinuity worth explaining — see the relevant family's summary and, "+
		"where present, an inline profiling note. Scales that scaled as expected were "+
		"not profiled just because profiling tooling exists.\n\n")
}

// discoveredURLCount returns the oracle's ResponsesOK for (family, scale) —
// distinct from the raw Scale number, which for S4 is a body size in KB and
// for S6 includes dead/erroring pages never successfully crawled. Rebuilds
// the workload and recomputes its oracle; cheap and deterministic, not a re-measurement.
func discoveredURLCount(family string, scale int) (int, bool) {
	w, err := BuildScaleWorkload(family, scale)
	if err != nil {
		return 0, false
	}
	opts := w.Opts.withDefaults()
	// Scale-family workloads are single-host and always measured with sameOrigin=true.
	o := w.Site.Compute(opts.MaxDepth, true, opts.Canonicalization, opts.SortQueryParams)
	return o.ResponsesOK, true
}

func writeScaleFamilyTable(w io.Writer, fam ScaleFamilyResult) {
	fmt.Fprintf(w, "| scale | runs | warmups | median | p95 | p99 | urls/sec | requests/sec | peak RSS | correctness |\n")
	fmt.Fprintf(w, "|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|\n")
	for _, p := range fam.Points {
		if p.Excluded {
			fmt.Fprintf(w, "| %s | — | — | — | — | — | — | — | — | **EXCLUDED** — %s |\n",
				ScaleLabel(p.Scale), p.Reason)
			continue
		}
		ws := p.Stats
		urlsPerSec := 0.0
		if ws.Duration.Median > 0 {
			if n, ok := discoveredURLCount(fam.Family, p.Scale); ok {
				urlsPerSec = float64(n) / ws.Duration.Median.Seconds()
			}
		}
		fmt.Fprintf(w, "| %s | %d | %d | %s | %s | %s | %.1f | %.1f | %s | %d/%d %s |\n",
			ScaleLabel(p.Scale), ws.Runs, ws.Warmups,
			fmtDur(ws.Duration.Median), fmtDur(ws.Duration.P95), fmtDur(ws.Duration.P99),
			urlsPerSec, ws.MedianRPS, fmtBytes(ws.MedianPeakRSSBytes),
			ws.PassCount, ws.Runs, ws.Status)
	}
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "`urls/sec` is the oracle's known discoverable-and-successfully-fetched page\n")
	fmt.Fprintf(w, "count divided by median duration — distinct from `requests/sec` (chcrawl's own\n")
	fmt.Fprintf(w, "measured HTTP request counter), which also counts any retried/failed\n")
	fmt.Fprintf(w, "attempts. For S4 in particular, Scale is a body size in KB, not a URL\n")
	fmt.Fprintf(w, "count — `urls/sec` here is *not* derived from that number.\n\n")
}

func writeScaleFamilySummary(w io.Writer, fam ScaleFamilyResult) {
	ratios := ComputeScalingRatios(fam.Points)
	if len(ratios) == 0 {
		fmt.Fprintf(w, "_Fewer than two measured scales — no scaling ratio to report._\n\n")
		return
	}
	fmt.Fprintf(w, "**Scaling efficiency:**\n\n")
	fmt.Fprintf(w, "| from -> to | scale x | runtime x (median) | classification | peak RSS x (median) | ΔRSS / 1,000 units |\n")
	fmt.Fprintf(w, "|---|---:|---:|---|---:|---:|\n")
	for _, r := range ratios {
		// find matching points for the RSS delta helper
		var a, b ScalePoint
		found := 0
		for j := range fam.Points {
			if fam.Points[j].Excluded {
				continue
			}
			if fam.Points[j].Scale == r.FromScale {
				a = fam.Points[j]
				found++
			}
			if fam.Points[j].Scale == r.ToScale {
				b = fam.Points[j]
				found++
			}
		}
		deltaStr := "n/a"
		if found == 2 {
			if delta, ok := DeltaRSSPer1000(a, b); ok {
				deltaStr = fmt.Sprintf("%.2f MB", delta/(1024*1024))
			}
		}
		fmt.Fprintf(w, "| %s -> %s | %.1fx | %.2fx | %s | %.2fx | %s |\n",
			ScaleLabel(r.FromScale), ScaleLabel(r.ToScale), r.ScaleRatio, r.MedianDurationRatio,
			r.Classification, r.MedianRSSRatio, deltaStr)
	}
	fmt.Fprintf(w, "\n")
}

// topologyFamilyOrder and topologyLabels drive writeTopologyComparison,
// answering whether S1's super-linear scaling comes from its
// giant-document topology or from elsewhere in the crawl pipeline.
var topologyFamilyOrder = []string{"S1-wide-flat", "S1b-wide-distributed", "S1c-balanced-tree"}

var topologyLabels = map[string]string{
	"S1-wide-flat":         "single root document, all links on one page",
	"S1b-wide-distributed": "100 hub pages, ~n/100 links per hub",
	"S1c-balanced-tree":    "balanced 10-ary tree, 10 links per page",
}

func writeTopologyComparison(w io.Writer, report *ScaleReport) {
	byFamily := map[string]ScaleFamilyResult{}
	for _, fam := range report.Families {
		byFamily[fam.Family] = fam
	}
	present := 0
	for _, name := range topologyFamilyOrder {
		if _, ok := byFamily[name]; ok {
			present++
		}
	}
	if present == 0 {
		return
	}

	fmt.Fprintf(w, "## Topology-controlled scaling\n\n")
	fmt.Fprintf(w, "S1-wide-flat's 10k->100k transition showed super-linear runtime growth\n")
	fmt.Fprintf(w, "(see its scaling-efficiency table above), and its own CPU/heap profile\n")
	fmt.Fprintf(w, "pointed at giant-document HTML parsing and allocation as a plausible\n")
	fmt.Fprintf(w, "cause — but a single profile of one topology can't distinguish \"this\n")
	fmt.Fprintf(w, "topology's document size is the cause\" from \"this URL count is the\n")
	fmt.Fprintf(w, "cause, regardless of topology.\" S1b-wide-distributed and S1c-balanced-tree\n")
	fmt.Fprintf(w, "hold the total unique-URL scale approximately fixed while changing *only*\n")
	fmt.Fprintf(w, "the topology: S1b keeps the same shallow \"discover everything from one hop\"\n")
	fmt.Fprintf(w, "shape but caps any single document at ~n/100 links; S1c replaces the shallow\n")
	fmt.Fprintf(w, "shape entirely with a balanced tree where discovery is staggered across many\n")
	fmt.Fprintf(w, "more, much smaller pages.\n\n")

	fmt.Fprintf(w, "| workload | scale | topology | median | p95 | urls/sec | requests/sec | peak RSS | correctness |\n")
	fmt.Fprintf(w, "|---|---:|---|---:|---:|---:|---:|---:|---|\n")
	for _, name := range topologyFamilyOrder {
		fam, ok := byFamily[name]
		if !ok {
			continue
		}
		for _, p := range fam.Points {
			if p.Excluded {
				fmt.Fprintf(w, "| %s | %s | %s | — | — | — | — | — | **EXCLUDED** — %s |\n",
					name, ScaleLabel(p.Scale), topologyLabels[name], p.Reason)
				continue
			}
			ws := p.Stats
			urlsPerSec := 0.0
			if ws.Duration.Median > 0 {
				if n, ok := discoveredURLCount(name, p.Scale); ok {
					urlsPerSec = float64(n) / ws.Duration.Median.Seconds()
				}
			}
			fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %.1f | %.1f | %s | %d/%d %s |\n",
				name, ScaleLabel(p.Scale), topologyLabels[name],
				fmtDur(ws.Duration.Median), fmtDur(ws.Duration.P95),
				urlsPerSec, ws.MedianRPS, fmtBytes(ws.MedianPeakRSSBytes),
				ws.PassCount, ws.Runs, ws.Status)
		}
	}
	fmt.Fprintf(w, "\n")

	writeTopologySameScaleRatios(w, byFamily)

	if report.TopologyConclusion != "" {
		fmt.Fprintf(w, "### Conclusion\n\n%s\n\n", report.TopologyConclusion)
	}
}

// writeTopologySameScaleRatios computes "S1a 100k / S1b 100k" style
// comparisons at matching scale points. S1c's achieved totals (11,111/111,111)
// are ~11% larger than 10k/100k, so a per-1,000-URLs normalized runtime is
// reported alongside the raw ratio to avoid distorting the comparison.
func writeTopologySameScaleRatios(w io.Writer, byFamily map[string]ScaleFamilyResult) {
	pointAt := func(family string, scale int) *ScalePoint {
		fam, ok := byFamily[family]
		if !ok {
			return nil
		}
		for i := range fam.Points {
			if fam.Points[i].Scale == scale && !fam.Points[i].Excluded {
				return &fam.Points[i]
			}
		}
		return nil
	}
	closestPoint := func(family string) *ScalePoint {
		fam, ok := byFamily[family]
		if !ok || len(fam.Points) == 0 {
			return nil
		}
		// Largest measured point (last in ascending order) — used only for
		// S1c, whose scale list has no exact 10k/100k match.
		for i := len(fam.Points) - 1; i >= 0; i-- {
			if !fam.Points[i].Excluded {
				return &fam.Points[i]
			}
		}
		return nil
	}

	fmt.Fprintf(w, "### Same-scale ratios\n\n")
	fmt.Fprintf(w, "| comparison | scale(s) | runtime ratio | peak RSS ratio |\n")
	fmt.Fprintf(w, "|---|---|---:|---:|\n")

	a100k := pointAt("S1-wide-flat", 100_000)
	b100k := pointAt("S1b-wide-distributed", 100_000)
	if a100k != nil && b100k != nil {
		fmt.Fprintf(w, "| S1a 100k / S1b 100k | 100,000 / 100,000 | %.2fx | %.2fx |\n",
			ratioDuration(a100k.Stats.Duration.Median, b100k.Stats.Duration.Median),
			ratioUint64(a100k.Stats.MedianPeakRSSBytes, b100k.Stats.MedianPeakRSSBytes))
	}
	a10k := pointAt("S1-wide-flat", 10_000)
	b10k := pointAt("S1b-wide-distributed", 10_000)
	if a10k != nil && b10k != nil {
		fmt.Fprintf(w, "| S1a 10k / S1b 10k | 10,000 / 10,000 | %.2fx | %.2fx |\n",
			ratioDuration(a10k.Stats.Duration.Median, b10k.Stats.Duration.Median),
			ratioUint64(a10k.Stats.MedianPeakRSSBytes, b10k.Stats.MedianPeakRSSBytes))
	}
	cBig := closestPoint("S1c-balanced-tree")
	if a100k != nil && cBig != nil {
		fmt.Fprintf(w, "| S1a 100k / S1c %s | 100,000 / %s (~11%% larger) | %.2fx | %.2fx |\n",
			ScaleLabel(cBig.Scale), ScaleLabel(cBig.Scale),
			ratioDuration(a100k.Stats.Duration.Median, cBig.Stats.Duration.Median),
			ratioUint64(a100k.Stats.MedianPeakRSSBytes, cBig.Stats.MedianPeakRSSBytes))
	}
	fmt.Fprintf(w, "\n")

	// Per-1,000-URLs normalized runtime, so S1c's larger scale doesn't
	// distort the comparison against S1a/S1b's exact 100k.
	fmt.Fprintf(w, "Normalized (median ms per 1,000 discovered URLs, largest measured scale "+
		"per family):\n\n")
	fmt.Fprintf(w, "| workload | scale | median ms/1,000 URLs |\n")
	fmt.Fprintf(w, "|---|---:|---:|\n")
	for _, name := range topologyFamilyOrder {
		p := closestPoint(name)
		if p == nil {
			continue
		}
		n, ok := discoveredURLCount(name, p.Scale)
		if !ok || n == 0 {
			continue
		}
		perThousand := p.Stats.Duration.Median.Seconds() * 1000 / (float64(n) / 1000)
		fmt.Fprintf(w, "| %s | %s | %.2f ms |\n", name, ScaleLabel(p.Scale), perThousand)
	}
	fmt.Fprintf(w, "\n")
}

func writeSingleWorkloadTable(w io.Writer, ws *WorkloadStats) {
	fmt.Fprintf(w, "| runs | warmups | median | p95 | p99 | rps (median) | peak RSS | correctness |\n")
	fmt.Fprintf(w, "|---:|---:|---:|---:|---:|---:|---:|---|\n")
	fmt.Fprintf(w, "| %d | %d | %s | %s | %s | %.1f | %s | %d/%d %s |\n\n",
		ws.Runs, ws.Warmups, fmtDur(ws.Duration.Median), fmtDur(ws.Duration.P95), fmtDur(ws.Duration.P99),
		ws.MedianRPS, fmtBytes(ws.MedianPeakRSSBytes), ws.PassCount, ws.Runs, ws.Status)
}

func writeConcurrencySweepTable(w io.Writer, meta ScaleReportMeta, sweep *ConcurrencySweepResult) {
	fmt.Fprintf(w, "Representative workload: **%s @ scale %s**, held fixed while only\n", sweep.Family, ScaleLabel(sweep.Scale))
	fmt.Fprintf(w, "`Concurrency` (worker-pool size) varies. PerHostConcurrency is left at its\n")
	fmt.Fprintf(w, "usual default. Higher concurrency is not assumed to be faster — this table\n")
	fmt.Fprintf(w, "is how the saturation point (if any, within this workload's single-host\n")
	fmt.Fprintf(w, "target) is found rather than assumed.\n\n")
	fmt.Fprintf(w, "| concurrency | runs | median | p95 | urls/sec | peak RSS | correctness |\n")
	fmt.Fprintf(w, "|---:|---:|---:|---:|---:|---:|---|\n")
	discovered, _ := discoveredURLCount(sweep.Family, sweep.Scale)
	for _, lvl := range sweep.Levels {
		ws := lvl.Stats
		urlsPerSec := 0.0
		if ws.Duration.Median > 0 {
			urlsPerSec = float64(discovered) / ws.Duration.Median.Seconds()
		}
		fmt.Fprintf(w, "| %d | %d | %s | %s | %.1f | %s | %d/%d %s |\n",
			lvl.Concurrency, ws.Runs, fmtDur(ws.Duration.Median), fmtDur(ws.Duration.P95),
			urlsPerSec, fmtBytes(ws.MedianPeakRSSBytes), ws.PassCount, ws.Runs, ws.Status)
	}
	fmt.Fprintf(w, "\n")
}

func writeScaleObservations(w io.Writer, report *ScaleReport) {
	fmt.Fprintf(w, "## Scaling observations\n\n")
	labels := map[string]string{
		"S1-wide-flat":        "Frontier/dedup/scheduling scaling (fan-out)",
		"S2-deep-chain":       "Depth-tracking/sequential-scheduling scaling",
		"S3-high-duplication": "Dedup/canonicalization scaling under heavy duplication",
		"S4-large-html":       "Response-body/HTML-parsing/allocation scaling",
		"S5-parameter-heavy":  "Query-parsing/parameter-extraction scaling",
		"S6-mixed-realistic":  "Composite realistic-site scaling",
	}
	for _, fam := range report.Families {
		label := labels[fam.Family]
		if label == "" {
			label = fam.Family
		}
		ratios := ComputeScalingRatios(fam.Points)
		fmt.Fprintf(w, "**%s:** ", label)
		if len(ratios) == 0 {
			fmt.Fprintf(w, "fewer than two measured scales — no observation to draw.\n\n")
			continue
		}
		last := ratios[len(ratios)-1]
		var excludedNote string
		for _, p := range fam.Points {
			if p.Excluded {
				excludedNote = fmt.Sprintf(" %s was excluded (%s) and not measured.", ScaleLabel(p.Scale), p.Reason)
				break
			}
		}
		fmt.Fprintf(w, "runtime scaled %s from %s to %s (%.2fx runtime for %.1fx scale).%s\n\n",
			last.Classification, ScaleLabel(last.FromScale), ScaleLabel(last.ToScale),
			last.MedianDurationRatio, last.ScaleRatio, excludedNote)
	}
	if report.ConcurrencySweep != nil {
		fmt.Fprintf(w, "**Concurrency scaling:** see the table above for the measured "+
			"saturation curve on %s @ scale %s; no single concurrency level is assumed "+
			"optimal beyond what was actually measured.\n\n",
			report.ConcurrencySweep.Family, ScaleLabel(report.ConcurrencySweep.Scale))
	}
}

func writeScaleLimitations(w io.Writer, report *ScaleReport) {
	fmt.Fprintf(w, "## Limitations\n\n")
	fmt.Fprintf(w, "- Synthetic workloads only — no real-world site's link graph, response-time\n")
	fmt.Fprintf(w, "  distribution, or content mix is reproduced exactly; each family isolates\n")
	fmt.Fprintf(w, "  one architectural dimension deliberately, at the cost of realism (S6\n")
	fmt.Fprintf(w, "  aside, which composes several dimensions at once but is still synthetic).\n")
	fmt.Fprintf(w, "- Run-count tiers mean the largest scale in each family is measured with far\n")
	fmt.Fprintf(w, "  fewer samples than the smallest — its percentiles (especially P95/P99) are\n")
	fmt.Fprintf(w, "  correspondingly less statistically resolved; see each family's Runs column.\n")
	fmt.Fprintf(w, "- Scaling-ratio classifications compare only two points at a time and do not\n")
	fmt.Fprintf(w, "  fit or claim any particular growth curve (linear, log-linear, quadratic).\n")
	var anyExcluded bool
	for _, fam := range report.Families {
		for _, p := range fam.Points {
			if p.Excluded {
				anyExcluded = true
			}
		}
	}
	if anyExcluded {
		fmt.Fprintf(w, "- One or more scales were excluded as impractical on the benchmark machine\n")
		fmt.Fprintf(w, "  — see the EXCLUDED rows above for the specific scale, family, and reason\n")
		fmt.Fprintf(w, "  (with the actual probe wall-clock/RSS numbers that triggered exclusion).\n")
		fmt.Fprintf(w, "  This is a statement about this machine's practical ceiling, not a claim\n")
		fmt.Fprintf(w, "  about chcrawl's absolute limits.\n")
	}
	fmt.Fprintf(w, "- This benchmark measures chcrawl only. A competitor comparison at these\n")
	fmt.Fprintf(w, "  scales was deliberately deferred — see the task brief this suite was built\n")
	fmt.Fprintf(w, "  from — until the workloads and oracle were validated at small scale first.\n")
	if report.TopologyConclusion != "" {
		fmt.Fprintf(w, "- **This host is a shared, actively-used development machine** (browser, IDE,\n")
		fmt.Fprintf(w, "  and other processes competing for CPU) running the Linux `powersave`\n")
		fmt.Fprintf(w, "  cpufreq governor rather than `performance`. This produced measurable,\n")
		fmt.Fprintf(w, "  sustained (not single-sample) run-to-run variance at the largest scales —\n")
		fmt.Fprintf(w, "  discovered and investigated via repeated independent measurement rather\n")
		fmt.Fprintf(w, "  than assumed away; see \"Topology-controlled scaling\" for the specific\n")
		fmt.Fprintf(w, "  numbers and reasoning. Absolute timings in this report should be read as\n")
		fmt.Fprintf(w, "  representative of this machine under real, uncontrolled desktop load, not\n")
		fmt.Fprintf(w, "  as a dedicated, isolated benchmark server's numbers.\n")
	}
}
