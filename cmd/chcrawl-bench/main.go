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
	scaleBench := fs.Bool("scale-bench", false,
		"run the large-scale scalability suite (S1-S6: 100 to 100,000+ URLs) instead of the small-workload suite")
	scaleFamily := fs.String("scale-family", "", "run only this scale family (default: all six; see benchlab.ScaleFamilies)")
	scaleConcurrencySweep := fs.Bool("scale-concurrency-sweep", true,
		"include the concurrency scaling sweep (1/4/10/25/50/100 workers) in -scale-bench")
	internalScaleWorker := fs.String("internal-scale-worker", "",
		"internal use only: measure exactly one (family, scale) in this process and print WorkloadStats JSON to stdout — see README.md")
	internalScaleValue := fs.Int("internal-scale-value", 0, "internal use only: the scale value paired with -internal-scale-worker")
	internalScaleRuns := fs.Int("internal-scale-runs", 1, "internal use only")
	internalScaleWarmups := fs.Int("internal-scale-warmups", 0, "internal use only")
	internalScaleConcurrency := fs.Int("internal-scale-concurrency", 0, "internal use only: override RunOptions.Concurrency (0 = the workload's own default)")
	scaleRenderFrom := fs.String("scale-render-from", "",
		"render the markdown report from a previously-written -scale-bench -json output file instead of re-running the (expensive) measurement — -report still selects the output path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *scaleRenderFrom != "" {
		return runScaleRenderFrom(*scaleRenderFrom, *reportPath)
	}
	if *internalScaleWorker != "" {
		return runInternalScaleWorker(*internalScaleWorker, *internalScaleValue, *internalScaleRuns, *internalScaleWarmups, *internalScaleConcurrency)
	}
	if *internalWorker != "" {
		return runInternalWorker(*internalWorker, *runs, *warmups, *retryDisabledComparison)
	}
	if *scaleBench {
		return runScaleBench(*scaleFamily, *reportPath, *scaleConcurrencySweep, *jsonOut)
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

// runStats measures each workload warmups+runs times in its own subprocess
// (see runInternalWorker) so peak RSS is isolated per workload rather than
// inherited from an earlier, heavier one.
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

// spawnWorker re-execs this binary with -internal-worker set to exactly one
// workload, isolating that workload's peak-RSS measurement in its own process.
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

// runInternalWorker is the subprocess entry point spawned by spawnWorker. A
// FAIL/FLAKY correctness result is a valid outcome and still exits 0; only a
// hard error (unknown workload, engine/config failure) is nonzero.
func runInternalWorker(workload string, runs, warmups int, disableRetry bool) error {
	site, ok := benchlab.Workloads()[workload]
	if !ok {
		return fmt.Errorf("unknown workload %q", workload)
	}
	sameOrigin := !strings.Contains(workload, "multi-host")

	// 5s/iteration comfortably covers w10-chaos's worst-case retry backoff.
	budget := time.Duration(runs+warmups) * 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	ws, err := benchlab.RunMany(ctx, site, sameOrigin, benchlab.RunOptions{DisableRetry: disableRetry}, warmups, runs)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(ws)
}

// runInternalScaleWorker is the subprocess entry point for the large-scale
// suite, spawned by spawnScaleWorker. Same peak-RSS isolation as runInternalWorker.
func runInternalScaleWorker(family string, scale, runs, warmups, concurrency int) error {
	var w benchlab.ScaleWorkload
	switch family {
	// These two demo workloads aren't part of the (family, scale) matrix, so
	// they're recognized by name here instead of a second flag surface.
	case "s3-query-canonicalization-demo":
		w = benchlab.S3QueryCanonicalizationDemo()
	case "s4-default-bodycap-demo":
		w = benchlab.S4DefaultBodyCapDemo()
	default:
		var err error
		w, err = benchlab.BuildScaleWorkload(family, scale)
		if err != nil {
			return err
		}
	}
	opts := w.Opts
	if concurrency > 0 {
		opts.Concurrency = concurrency
		// PerHostConcurrency must never exceed the narrowed worker-pool size,
		// or config validation aborts the run after everything else already ran.
		effectivePerHost := opts.PerHostConcurrency
		if effectivePerHost == 0 {
			effectivePerHost = 4 // config.defaults()' PerHostConcurrency
		}
		if effectivePerHost > concurrency {
			opts.PerHostConcurrency = concurrency
		}
	}
	// Generous hang-guard only; the practicality ceilings live in runScaleBench.
	budget := time.Duration(runs+warmups+2) * 3 * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	ws, err := benchlab.RunMany(ctx, w.Site, true, opts, warmups, runs)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(ws)
}

// spawnScaleWorker re-execs this binary with -internal-scale-worker set to
// measure exactly one (family, scale), with timeout as a hard ceiling on top
// of the subprocess's own internal budget.
func spawnScaleWorker(family string, scale, runs, warmups, concurrency int, timeout time.Duration) (*benchlab.WorkloadStats, time.Duration, error) {
	args := []string{
		"-internal-scale-worker", family,
		"-internal-scale-value", strconv.Itoa(scale),
		"-internal-scale-runs", strconv.Itoa(runs),
		"-internal-scale-warmups", strconv.Itoa(warmups),
	}
	if concurrency > 0 {
		args = append(args, "-internal-scale-concurrency", strconv.Itoa(concurrency))
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Stderr = os.Stderr
	start := time.Now()
	out, err := cmd.Output()
	wall := time.Since(start)
	if err != nil {
		return nil, wall, err
	}
	var ws benchlab.WorkloadStats
	if err := json.Unmarshal(out, &ws); err != nil {
		return nil, wall, fmt.Errorf("decoding scale worker output: %w", err)
	}
	return &ws, wall, nil
}

// Set well above the practicality ceilings so a too-slow scale is caught by
// the ceiling check (an honest EXCLUDED reason), not an opaque process kill.
const (
	scaleProbeTimeout = 5 * time.Minute
	scaleTierTimeout  = 20 * time.Minute
)

// scaleConcurrencyLevels is the fixed sweep used by -scale-concurrency-sweep.
var scaleConcurrencyLevels = []int{1, 4, 10, 25, 50, 100}

// scaleProfilingNotes holds hand-written pprof findings for the workloads
// whose scaling showed a discontinuity worth explaining.
var scaleProfilingNotes = map[string]string{
	"S1-wide-flat": "" +
		"S1's 10k->100k transition shows the sharpest super-linear jump of any\n" +
		"family (see the scaling-efficiency row above). An initial standalone\n" +
		"profile of just this workload (`go test ./internal/benchlab -bench\n" +
		"BenchmarkZZS1WideFlat100k -cpuprofile -memprofile`) found\n" +
		"`golang.org/x/net/html.ParseWithOptions` at ~29% of cumulative CPU\n" +
		"samples and `html.NewTokenizerFragment` at ~22% of allocated bytes —\n" +
		"consistent with S1's single 100,000-link root document being an\n" +
		"expensive parse. Taken alone, that would suggest giant-document parsing\n" +
		"is the cause. It is NOT: see \"Topology-controlled scaling\" below.\n" +
		"S1b-wide-distributed removes the giant document entirely (no page holds\n" +
		"more than ~1,000 links) and — across a 3-trial reproducibility check\n" +
		"described in the topology conclusion below (needed because of\n" +
		"observed run-to-run environmental variance on this shared,\n" +
		"powersave-governor machine) — still shows nearly identical\n" +
		"super-linear growth (16.63x vs S1a's 16.32x runtime for 10x scale in\n" +
		"the low-variance trials) and a profile with the *same* ~28.65%\n" +
		"cumulative CPU in `html.ParseWithOptions` — total parsing cost is a\n" +
		"function of total link count, not how concentrated those links are\n" +
		"into one document. The giant document does cost something real (S1a\n" +
		"uses ~70% more peak RSS than S1b at 100k in the low-variance trial —\n" +
		"a measured, consistently-reproduced tax), but that tax cannot explain\n" +
		"a ~16x-for-10x-scale growth curve on its own. See the topology\n" +
		"conclusion for the fuller picture (frontier/scheduler contention under\n" +
		"bursty wide discovery, not parsing, is what correlates with the\n" +
		"scaling-shape difference). No engine change is proposed from either\n" +
		"finding; profiling here is for measurement and explanation, not a\n" +
		"trigger for optimization.",
	"S1b-wide-distributed": "" +
		"Profiled (`go test ./internal/benchlab -bench\n" +
		"BenchmarkZZS1bWideDistributed100k -cpuprofile -memprofile -mutexprofile\n" +
		"-blockprofile`) specifically because it isolates S1's super-linear\n" +
		"scaling question from the giant-document confound: no page here holds\n" +
		"more than ~1,000 links, yet the 10k->100k runtime ratio (16.63x in the\n" +
		"low-variance reproducibility trial — see the topology conclusion) is\n" +
		"nearly identical to S1a's (16.32x). The CPU profile shows the same\n" +
		"syscall-dominated, GC/scheduler-heavy shape as S1a (`runtime.scanObject`,\n" +
		"`stealWork`, `mallocgc*`, `findRunnable`/`selectgo` together account for\n" +
		"a large share of non-syscall CPU). The block profile is the more\n" +
		"specific finding: 85% of all recorded goroutine blocking time is inside\n" +
		"`runtime.selectgo` — consistent with `PerHostConcurrency`'s default of\n" +
		"4 (confirmed as the real saturation point by the concurrency sweep)\n" +
		"leaving most of the worker pool parked waiting on the per-host\n" +
		"semaphore/frontier channel for most of the crawl, with the volume of\n" +
		"goroutine parking/waking itself growing worse than linearly as URL\n" +
		"count grows. This points at the frontier/scheduling layer's behavior\n" +
		"under high concurrent-goroutine churn, not at HTML parsing, as the\n" +
		"more likely driver of the super-linear *shape* specifically. No engine\n" +
		"change is proposed here — see the topology conclusion for why this is\n" +
		"flagged as a candidate for a separate, future optimization\n" +
		"investigation rather than acted on now.",
}

// scaleTopologyConclusion is the hand-authored judgment call the
// topology-controlled comparison exists to produce, written after and
// citing the actual measured S1/S1b/S1c numbers; not derivable mechanically
// from the tables alone. The full reproducibility story (an outlier run
// traced to the host's powersave cpufreq governor) is in the text itself.
const scaleTopologyConclusion = "" +
	"**Outcome: B — crawler frontier/scheduler behavior under bursty wide " +
	"discovery dominates the super-linear scaling shape, not giant-document " +
	"parsing/allocation.**\n\n" +
	"*A note on reproducibility first, since it materially shaped this " +
	"conclusion:* this host is a shared, actively-used development machine " +
	"(browser, IDE, and other processes competing for CPU) running the " +
	"Linux `powersave` cpufreq governor rather than `performance` — " +
	"confirmed via `scaling_cur_freq` showing individual cores idling at " +
	"400MHz alongside others at ~1.8GHz during measurement, and ruled out " +
	"as thermal throttling (package temperatures stayed at 52-57C against " +
	"a 100C threshold throughout). An initial single measurement showed S1a " +
	"and S1b both clearly super-linear; a second independent full-suite run " +
	"showed S1b at a much lower, roughly-linear ratio — a result that would " +
	"have contradicted the first outright. Rather than report whichever run " +
	"happened to run last, both S1a and S1b were re-measured a third time " +
	"(15 runs each, freshly). The two tables below are the actual per-trial " +
	"numbers, not a single cherry-picked run:\n\n" +
	"| workload | trial | 10k-ish median | 100k-ish median | ratio | in-trial stddev at 100k-ish |\n" +
	"|---|---|---:|---:|---:|---:|\n" +
	"| S1a | 1 | 231ms | 3962ms (n=3) | 17.14x | high (n=3, wide spread) |\n" +
	"| S1a | 2 | 228ms | 2278ms (n=3) | ~10x (unreliable, n=3) | high (n=3, wide spread) |\n" +
	"| S1a | 3 (used below) | 216ms | 3528ms (n=15) | 16.32x | 1.3% (low) |\n" +
	"| S1b | 1 | 199ms | 3342ms (n=15) | 16.76x | 2.1% (low) |\n" +
	"| S1b | 2 (outlier) | 386ms | 3161ms (n=15) | 8.18x | 9.1% (elevated, and all 30 of the 10k samples in this trial were uniformly ~2x slower than trials 1/3) |\n" +
	"| S1b | 3 (used below) | 203ms | 3375ms (n=15) | 16.63x | 2.6% (low) |\n" +
	"| S1c | 1 (used below) | 402ms (@11,111) | 3723ms (@111,111) | 9.27x | 1.0% (low) |\n" +
	"| S1c | 2 | 377ms (@11,111) | 3487ms (@111,111) | 9.25x | 1.0% (low) |\n\n" +
	"Two of three S1a/S1b trials, and both S1c trials, converge closely; " +
	"trial 2's S1b measurement is the one outlier, and its own elevated " +
	"in-trial variance (plus every one of its 30 samples being consistently " +
	"slow, not one bad sample) points at a sustained environmental slowdown " +
	"during that window rather than a real behavioral difference. The " +
	"conclusion below uses the low-variance, mutually-consistent trials " +
	"(S1a/S1b trial 3, S1c trial 1 — also reflected in the tables above " +
	"this section).\n\n" +
	"With that: S1-wide-flat (S1a, 216ms->3528ms, 16.32x) and " +
	"S1b-wide-distributed (203ms->3375ms, 16.63x) show essentially the same " +
	"super-linear 10k->100k growth despite S1b having no document larger " +
	"than ~1,000 links — S1a's single 100,000-link root page cannot be the " +
	"cause of a discontinuity S1b reproduces almost exactly without it. " +
	"S1c-balanced-tree, at a comparable URL-count order (11,111->111,111, " +
	"377ms->3487ms), instead scales roughly linearly (9.25x). The variable " +
	"that actually correlates with super-linear vs linear scaling is not " +
	"document size — it is discovery *burst width*: S1a/S1b both discover a " +
	"huge number of new URLs within one or two hops of a handful of source " +
	"pages, while S1c discovers only 10 new URLs per fetched page, spread " +
	"across many more, much smaller fetch events.\n\n" +
	"The giant document does cost something real and consistently " +
	"measurable — just not in wall-clock ratio terms. Peak RSS at 100k is " +
	"~70% higher for S1a than S1b (381.5MB vs 224.0MB) in the low-variance " +
	"trial, and was ~58-63% higher in every other trial too — a real, " +
	"reproducible memory tax from concentrating 100,000 links' worth of DOM " +
	"nodes into one parse, even though it does not explain the *scaling " +
	"shape*.\n\n" +
	"This is corroborated by profiling: S1b's CPU profile shows the same " +
	"~28.65% cumulative time in HTML parsing as S1a's ~28.72% (removing the " +
	"giant document did not reduce total parsing cost — it only redistributed " +
	"it across more, smaller documents), so parsing volume is not what " +
	"differs between the two. What the diagnostic parser-isolation benchmark " +
	"does show is that pure parse+extract of a 100,000-link document takes " +
	"~170ms in isolation (`BenchmarkDiagParseExtract100k`) — under 5% of " +
	"S1a's ~3.5s total measured wall time — confirming parsing alone, " +
	"even at its most concentrated, cannot be the dominant cost. Similarly, " +
	"the isolated dedup diagnostic (`BenchmarkDiagDedup100k`, ~34ms) and " +
	"frontier push/pop diagnostic (`BenchmarkDiagFrontierPushPop100k`, " +
	"~7.5ms) are each individually small — the real cost only appears when " +
	"all of these run *together*, under real concurrency, which is exactly " +
	"what S1b's block profile shows: 85% of all recorded goroutine-blocking " +
	"time sits in `runtime.selectgo`, consistent with the concurrency " +
	"sweep's own finding that `PerHostConcurrency`'s default of 4 saturates " +
	"this single-host workload — most of the 10-worker pool spends most of " +
	"the crawl parked on the per-host semaphore or frontier channel, and " +
	"that parking/waking volume grows worse than linearly as URL count " +
	"grows.\n\n" +
	"No isolated diagnostic benchmark run alone is treated as the primary " +
	"performance number here — each is used only to attribute cost across " +
	"categories, per the brief. **No CHCrawl implementation change is made " +
	"or proposed in this task.** The evidence does suggest a real, " +
	"worth-investigating candidate for a *separate* future optimization " +
	"task: whether worker-pool/channel scheduling behavior under high " +
	"concurrent-goroutine churn (many workers parked on a per-host " +
	"semaphore during bursty wide discovery) can be made to scale more " +
	"linearly — but that investigation, and any resulting change, is " +
	"explicitly out of scope here."

// runScaleRenderFrom reads a previously-written -scale-bench -json report
// and re-renders it as markdown, without re-running the (expensive) measurement.
func runScaleRenderFrom(jsonPath, reportPath string) error {
	f, err := os.Open(jsonPath)
	if err != nil {
		return err
	}
	defer f.Close()
	var report benchlab.ScaleReport
	if err := json.NewDecoder(f).Decode(&report); err != nil {
		return fmt.Errorf("decoding %s: %w", jsonPath, err)
	}
	// Profiling notes/topology conclusion are hand-written prose in this
	// binary, not measured data, so always overwrite whatever the JSON carries.
	report.ProfilingNotes = scaleProfilingNotes
	report.TopologyConclusion = scaleTopologyConclusion

	out := os.Stdout
	if reportPath != "" {
		wf, err := os.Create(reportPath)
		if err != nil {
			return err
		}
		defer wf.Close()
		out = wf
	}
	benchlab.WriteScaleReport(out, &report)
	return nil
}

// runScaleBench probes each scale (1 iteration, 0 warmups) against the
// wall-clock/RSS practicality ceilings before committing to the full
// tier-appropriate measurement; exceeding a ceiling excludes that scale and
// every larger one in the family.
func runScaleBench(familyFilter, reportPath string, includeConcurrencySweep, jsonOut bool) error {
	env := benchlab.CaptureEnvironment()

	families := benchlab.ScaleFamilies()
	if familyFilter != "" {
		ok := false
		for _, f := range families {
			if f == familyFilter {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("unknown scale family %q (available: %s)", familyFilter, strings.Join(families, ", "))
		}
		families = []string{familyFilter}
	}

	report := &benchlab.ScaleReport{
		Meta: benchlab.ScaleReportMeta{
			GeneratedAt:         time.Now(),
			Environment:         env,
			WallClockCeiling:    benchlab.ScaleWallClockCeiling,
			PeakRSSCeilingBytes: benchlab.ScalePeakRSSCeiling,
		},
		ProfilingNotes:     scaleProfilingNotes,
		TopologyConclusion: scaleTopologyConclusion,
	}

	for _, family := range families {
		scales := benchlab.DefaultScales(family)
		fam := benchlab.ScaleFamilyResult{Family: family}
		excludedRest := false
		for i, scale := range scales {
			if excludedRest {
				fam.Points = append(fam.Points, benchlab.ScalePoint{
					Scale: scale, Excluded: true,
					Reason: fmt.Sprintf("skipped: a smaller scale in %s already exceeded the practicality ceiling", family),
				})
				continue
			}

			probe, probeWall, err := spawnScaleWorker(family, scale, 1, 0, 0, scaleProbeTimeout)
			if err != nil {
				fam.Points = append(fam.Points, benchlab.ScalePoint{
					Scale: scale, Excluded: true,
					Reason:        fmt.Sprintf("probe failed/timed out after %s: %v", probeWall.Round(time.Millisecond), err),
					ProbeDuration: probeWall,
				})
				fmt.Fprintf(os.Stderr, "%-24s %-12s EXCLUDED (probe error: %v)\n", family, benchlab.ScaleLabel(scale), err)
				excludedRest = true
				continue
			}
			probeDur := probe.Duration.Median // single sample: Median==the one sample
			probeRSS := probe.MedianPeakRSSBytes
			if probeDur > benchlab.ScaleWallClockCeiling || probeRSS > benchlab.ScalePeakRSSCeiling {
				reason := fmt.Sprintf("probe measured %s wall-clock / %s peak RSS, exceeding the %s / %s ceiling",
					probeDur.Round(time.Millisecond), fmtBytesLocal(probeRSS),
					benchlab.ScaleWallClockCeiling, fmtBytesLocal(benchlab.ScalePeakRSSCeiling))
				fam.Points = append(fam.Points, benchlab.ScalePoint{
					Scale: scale, Excluded: true, Reason: reason,
					ProbeDuration: probeDur, ProbePeakRSSBytes: probeRSS,
				})
				fmt.Fprintf(os.Stderr, "%-24s %-12s EXCLUDED (%s)\n", family, benchlab.ScaleLabel(scale), reason)
				excludedRest = true
				continue
			}

			runs, warmups := benchlab.RunTierFor(i)
			ws, _, err := spawnScaleWorker(family, scale, runs, warmups, 0, scaleTierTimeout)
			if err != nil {
				return fmt.Errorf("measuring %s @ %s: %w", family, benchlab.ScaleLabel(scale), err)
			}
			fam.Points = append(fam.Points, benchlab.ScalePoint{Scale: scale, Stats: ws})
			fmt.Fprintf(os.Stderr, "%-24s %-12s runs=%-3d median=%-9s p95=%-9s %d/%d %s\n",
				family, benchlab.ScaleLabel(scale), ws.Runs, ws.Duration.Median.Round(time.Millisecond),
				ws.Duration.P95.Round(time.Millisecond), ws.PassCount, ws.Runs, ws.Status)
		}
		report.Families = append(report.Families, fam)
	}

	// A failure below warns and continues rather than discarding every
	// already-completed family's data by aborting the whole run.
	if familyFilter == "" || familyFilter == "S3-high-duplication" {
		demo, _, err := spawnScaleWorkerForDemo("s3-query-canonicalization-demo")
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: S3 query-canonicalization demo failed, omitting from report: %v\n", err)
		} else {
			report.QueryCanonicalizationDemo = demo
		}
	}
	if familyFilter == "" || familyFilter == "S4-large-html" {
		demo, _, err := spawnScaleWorkerForDemo("s4-default-bodycap-demo")
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: S4 default-bodycap demo failed, omitting from report: %v\n", err)
		} else {
			report.DefaultBodyCapDemo = demo
		}
	}

	if includeConcurrencySweep {
		sweepFamily := "S1-wide-flat"
		sweepScale := benchlab.S1Scales[1] // 1,000 — big enough to show contention, cheap enough for 6 levels
		sweep := &benchlab.ConcurrencySweepResult{Family: sweepFamily, Scale: sweepScale}
		sweepOK := true
		for _, level := range scaleConcurrencyLevels {
			ws, _, err := spawnScaleWorker(sweepFamily, sweepScale, 10, 2, level, scaleTierTimeout)
			if err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: concurrency sweep at level %d failed, omitting the sweep from report: %v\n", level, err)
				sweepOK = false
				break
			}
			sweep.Levels = append(sweep.Levels, benchlab.ConcurrencyPoint{Concurrency: level, Stats: ws})
			fmt.Fprintf(os.Stderr, "concurrency-sweep       c=%-4d median=%-9s p95=%-9s %d/%d %s\n",
				level, ws.Duration.Median.Round(time.Millisecond), ws.Duration.P95.Round(time.Millisecond), ws.PassCount, ws.Runs, ws.Status)
		}
		if sweepOK {
			report.ConcurrencySweep = sweep
			report.Meta.ConcurrencyLevels = scaleConcurrencyLevels
			report.Meta.ConcurrencyWorkloadFamily = sweepFamily
			report.Meta.ConcurrencyWorkloadScale = sweepScale
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
	if jsonOut {
		return benchlab.WriteScaleJSON(out, report)
	}
	benchlab.WriteScaleReport(out, report)
	return nil
}

// spawnScaleWorkerForDemo runs one of the two fixed-size demonstration
// workloads (S3 query-canonicalization, S4 default body cap) in its own
// subprocess.
func spawnScaleWorkerForDemo(name string) (*benchlab.WorkloadStats, time.Duration, error) {
	ws, wall, err := spawnScaleWorker(name, 0, 10, 2, 0, scaleTierTimeout)
	return ws, wall, err
}

func fmtBytesLocal(b uint64) string {
	return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
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

// runCompetitorBench runs the statistically-hardened comparison: chcrawl (as
// an external subprocess, like the other three) plus katana/hakrawler/gospider,
// warmups+runs times each, against every comparison-eligible workload.
func runCompetitorBench(workload, reportPath string, runs, warmups int, jsonOut bool) error {
	binPath, cleanup, err := buildChcrawlBinary()
	if err != nil {
		return fmt.Errorf("building chcrawl binary: %w", err)
	}
	defer cleanup()
	// Captured now, not after the measurement loop, so git_commit reflects the
	// binary actually measured even if the repo changes mid-run.
	env := benchlab.CaptureEnvironment()

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
		Environment: env,
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
// comparison-eligible workload, excluding the multi-host one whose scope
// semantics aren't comparable across tools.
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
