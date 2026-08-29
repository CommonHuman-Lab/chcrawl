package benchlab

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// CompetitorReportMeta carries the run parameters and machine/tool-version
// context for a competitor report, so the report is self-describing.
type CompetitorReportMeta struct {
	Runs        int         `json:"runs"`
	Warmups     int         `json:"warmups"`
	MaxDepth    int         `json:"max_depth"`
	GeneratedAt time.Time   `json:"generated_at"`
	Environment Environment `json:"environment"`
	// RunLogs is the exact randomized tool-invocation order per workload,
	// keyed by workload name — see RunCompetitorInterleaved. Nil/empty for
	// a report built without it (e.g. an older JSON file re-rendered).
	RunLogs map[string]*InterleavedRunLog `json:"run_logs,omitempty"`
}

func competitorToolOrder() []string {
	order := []string{"chcrawl"}
	for _, t := range ExternalTools() {
		order = append(order, t.Name)
	}
	return order
}

// externalToolNames is competitorToolOrder without chcrawl — the tools
// this report is actually comparing chcrawl against.
func externalToolNames() []string {
	names := make([]string, 0, len(ExternalTools()))
	for _, t := range ExternalTools() {
		names = append(names, t.Name)
	}
	return names
}

// WriteCompetitorJSON writes the full machine-readable report: every
// sample, environment, tool versions, and run configuration.
func WriteCompetitorJSON(w io.Writer, meta CompetitorReportMeta, results map[string]map[string]*CompetitorStats) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Meta      CompetitorReportMeta                   `json:"meta"`
		Workloads map[string]map[string]*CompetitorStats `json:"workloads"`
	}{meta, results})
}

// WriteCompetitorReport renders the full human-readable comparison: three
// clearly separated views (production wall-clock, engine/active
// diagnostic, correctness) — never combined into one score — plus
// configuration, environment, and methodology/limitations sections.
func WriteCompetitorReport(w io.Writer, meta CompetitorReportMeta, results map[string]map[string]*CompetitorStats) {
	workloads := make([]string, 0, len(results))
	for name := range results {
		workloads = append(workloads, name)
	}
	sort.Strings(workloads)

	order := competitorToolOrder()
	fmt.Fprintf(w, "# %s\n\n", strings.Join(order, " vs. "))
	writeCompetitorEnvironment(w, meta)
	writeAggregateSummary(w, workloads, results)

	fmt.Fprintf(w, "## Methodology\n\n")
	fmt.Fprintf(w, "Every tool crawls the identical local (127.0.0.1-only) synthetic target\n")
	fmt.Fprintf(w, "per workload, run as an external OS process (the real CLI invocation a\n")
	fmt.Fprintf(w, "user would type — not an in-process API call for any tool, chcrawl\n")
	fmt.Fprintf(w, "included), scored against the same DiscoverablePaths ground truth used by\n")
	fmt.Fprintf(w, "chcrawl's own oracle-diff correctness suite. %d warmup iterations are run\n", meta.Warmups)
	fmt.Fprintf(w, "and discarded (including from correctness accounting) before %d measured\n", meta.Runs)
	fmt.Fprintf(w, "iterations per tool per workload; every measured iteration starts fresh\n")
	fmt.Fprintf(w, "local HTTP servers. Tool order is re-randomized independently on every\n")
	fmt.Fprintf(w, "single repetition (warmup or measured) rather than running one tool's full\n")
	fmt.Fprintf(w, "warmups+runs before starting the next — a blocked order would let\n")
	fmt.Fprintf(w, "environmental drift over a long run (thermal throttling, background load\n")
	fmt.Fprintf(w, "building up) bias whichever tool happens to run last, confounding drift\n")
	fmt.Fprintf(w, "with tool identity; see Reproducibility below for the exact seed and\n")
	fmt.Fprintf(w, "per-repetition order used. Percentiles use the nearest-rank method\n")
	fmt.Fprintf(w, "(`ceil(p/100 * n)`, 1-indexed) computed from individual per-run samples,\n")
	fmt.Fprintf(w, "identical to the methodology in chcrawl's own multi-run benchmark; with\n")
	fmt.Fprintf(w, "%d runs, P99's nearest rank is the sample count itself, i.e. P99 == Max —\n", meta.Runs)
	fmt.Fprintf(w, "reported anyway since it's requested, but treat it as barely more\n")
	fmt.Fprintf(w, "informative than Max at this sample size. MAD (median absolute deviation\n")
	fmt.Fprintf(w, "from the median) is reported alongside stddev as a dispersion measure\n")
	fmt.Fprintf(w, "less sensitive to a single slow-run spike; %d samples is not enough to\n", meta.Runs)
	fmt.Fprintf(w, "assert statistical significance from stddev/MAD alone — a difference\n")
	fmt.Fprintf(w, "smaller than or comparable to either is called out as inconclusive in\n")
	fmt.Fprintf(w, "prose, not asserted as a real effect. Peak RSS is the kernel's\n")
	fmt.Fprintf(w, "getrusage(RUSAGE_CHILDREN) Maxrss for that exact subprocess invocation\n")
	fmt.Fprintf(w, "(Linux) — genuinely isolated per invocation, with no cross-run or\n")
	fmt.Fprintf(w, "cross-tool contamination risk, since every invocation is already its own\n")
	fmt.Fprintf(w, "fresh OS process. `w9-multi-host-scope` is excluded: same-origin scope is\n")
	fmt.Fprintf(w, "implemented differently enough across these %d tools (registered-domain\n", len(order))
	fmt.Fprintf(w, "matching, subdomain flags, no built-in concept at all) that scoring it\n")
	fmt.Fprintf(w, "wouldn't be a fair comparison.\n\n")
	writeCompetitorConfiguration(w, meta)
	writeReproducibility(w, meta)

	fmt.Fprintf(w, "## View 1: production wall-clock\n\n")
	fmt.Fprintf(w, "The real end-to-end experience: process startup, HTTP work, parsing,\n")
	fmt.Fprintf(w, "scheduling, retries, and retry backoff where the tool has any — everything\n")
	fmt.Fprintf(w, "a user actually waits for from the CLI invocation. This is the primary,\n")
	fmt.Fprintf(w, "public number for every tool.\n\n")
	writeProductionTable(w, workloads, results)

	fmt.Fprintf(w, "## View 2: engine/active diagnostic\n\n")
	fmt.Fprintf(w, "Wall-clock time with measured retry backoff excluded — NOT CPU time, NOT\n")
	fmt.Fprintf(w, "a production number. Only chcrawl exposes this (`active_wall`, from its own\n")
	fmt.Fprintf(w, "JSONL summary record); none of %s expose an\n", strings.Join(externalToolNames(), "/"))
	fmt.Fprintf(w, "equivalent retry/backoff breakdown, so their rows report `N/A` rather than\n")
	fmt.Fprintf(w, "an inferred or estimated value.\n\n")
	writeActiveWallTable(w, workloads, results)

	fmt.Fprintf(w, "## View 3: correctness\n\n")
	fmt.Fprintf(w, "Pure oracle comparison — kept entirely separate from performance. A tool\n")
	fmt.Fprintf(w, "with excellent speed but incomplete discovery stays visibly incomplete\n")
	fmt.Fprintf(w, "here; nothing above hides it. Cell format is the worst-observed\n")
	fmt.Fprintf(w, "found/total across all %d measured runs (MinFound, not an average, so an\n", meta.Runs)
	fmt.Fprintf(w, "intermittent miss can't be smoothed away) plus the aggregate run status.\n\n")
	writeCorrectnessTable(w, workloads, results)

	writeCompetitorLimitations(w)
}

func writeCompetitorEnvironment(w io.Writer, meta CompetitorReportMeta) {
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
		fmt.Fprintf(w, "Git commit:    %s (chcrawl + benchmark harness — same repository)\n", e.GitCommit)
	}
	names := externalToolNames()
	versions := make([]string, 0, len(names))
	for _, name := range names {
		versions = append(versions, fmt.Sprintf("%s=%s", name, orNA(e.ToolVersions[name])))
	}
	fmt.Fprintf(w, "Tool versions: %s\n", strings.Join(versions, " "))
	fmt.Fprintf(w, "```\n\n")
}

// writeAggregateSummary rolls the per-workload results up into one glance
// row per tool. Deliberately NOT a weighted composite score — each figure
// stays in its own dimension so a reader can see, say, "fast but
// incomplete" instead of that tradeoff being averaged away. Runtime/RSS
// aggregates use median-of-medians for the same robustness-to-outliers
// reasoning as MAD (see Methodology).
func writeAggregateSummary(w io.Writer, workloads []string, results map[string]map[string]*CompetitorStats) {
	fmt.Fprintf(w, "## At a glance\n\n")
	fmt.Fprintf(w, "Roll-up across all %d workloads. Not a score — see View 1/2/3 below for\n", len(workloads))
	fmt.Fprintf(w, "the real, un-collapsed per-workload numbers before drawing a conclusion\n")
	fmt.Fprintf(w, "from any single row here.\n\n")
	fmt.Fprintf(w, "| tool | 100%%-recall workloads | oracle URLs covered | URLs never found | median runtime (of per-workload medians) | peak RSS range |\n")
	fmt.Fprintf(w, "|---|---:|---:|---:|---:|---:|\n")

	for _, tool := range competitorToolOrder() {
		var perfectWorkloads, testedWorkloads, coveredTotal, oracleTotal, missingTotal int
		var medians []time.Duration
		var rssVals []uint64
		for _, wl := range workloads {
			cs := results[wl][tool]
			if cs == nil || !cs.Available {
				continue
			}
			testedWorkloads++
			if cs.Status == "PASS" {
				perfectWorkloads++
			}
			coveredTotal += cs.MinFound
			oracleTotal += cs.GroundTruthTotal
			missingTotal += cs.GroundTruthTotal - cs.MinFound
			medians = append(medians, cs.Duration.Median)
			if cs.RSSAvailable {
				rssVals = append(rssVals, cs.MedianPeakRSSBytes)
			}
		}
		if testedWorkloads == 0 {
			fmt.Fprintf(w, "| %s | — | — | — | — | NOT INSTALLED |\n", tool)
			continue
		}
		rssRange := "N/A"
		if len(rssVals) > 0 {
			sort.Slice(rssVals, func(i, j int) bool { return rssVals[i] < rssVals[j] })
			rssRange = fmt.Sprintf("%s – %s", fmtBytes(rssVals[0]), fmtBytes(rssVals[len(rssVals)-1]))
		}
		sort.Slice(medians, func(i, j int) bool { return medians[i] < medians[j] })
		medianOfMedians := time.Duration(0)
		if len(medians) > 0 {
			medianOfMedians = medians[len(medians)/2]
		}
		fmt.Fprintf(w, "| %s | %d/%d | %d/%d | %d | %s | %s |\n",
			tool, perfectWorkloads, testedWorkloads, coveredTotal, oracleTotal, missingTotal,
			fmtDur(medianOfMedians), rssRange)
	}
	fmt.Fprintf(w, "\n")
}

func orNA(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// joinNatural joins names as an English list: "a", "a and b", or
// "a, b, and c" — used where report prose names the active external tools
// directly instead of a table column.
func joinNatural(names []string) string {
	switch len(names) {
	case 0:
		return "(no external tools active)"
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
	}
}

// competitorConfigRows are the documented per-tool config-table rows, keyed
// by Tool.Name. Only rows for tools currently in competitorToolOrder are
// printed — see writeCompetitorConfiguration.
var competitorConfigRows = map[string]string{
	"chcrawl":   "| chcrawl | default (10 workers) | %[1]d (-max-depth) | JS endpoint mining always on (static regex miner, no headless) | production default: 3 retries, exponential backoff, 429+5xx | 5s (-timeout) | followed (engine default cap) | chcrawl default | JSONL to stdout |\n",
	"katana":    "| katana | -c 10 | %[1]d (-d) | -jc (JS endpoint parsing, non-headless) | -retry 1 | -timeout 5 | followed (katana default) | katana default | -jsonl -silent |\n",
	"hakrawler": "| hakrawler | -t 10 (tool default: 8) | %[1]d (-d) | none (hakrawler has no JS-mining mode) | none exposed | 5s, imposed (tool default: none) | followed (hakrawler default) | hakrawler default | -json |\n",
	"gospider":  "| gospider | -t 1 -c 5 (both tool defaults) | %[1]d (-d) | linkfinder (regex JS miner, non-headless) | none exposed | 5s, imposed (tool default: 10s) | followed (gospider default) | gospider default (\"web\") | plain tagged stdout |\n",
}

func writeCompetitorConfiguration(w io.Writer, meta CompetitorReportMeta) {
	order := competitorToolOrder()
	fmt.Fprintf(w, "### Tool configuration\n\n")
	fmt.Fprintf(w, "Every tool uses plain HTTP crawling — no headless/browser mode is enabled\n")
	fmt.Fprintf(w, "for any tool. All %d are given the same crawl depth (%d).\n\n", len(order), meta.MaxDepth)
	fmt.Fprintf(w, "| tool | concurrency | depth | JS mode | retries | timeout | redirects | user agent | output |\n")
	fmt.Fprintf(w, "|---|---|---|---|---|---|---|---|---|\n")
	for _, name := range order {
		if row, ok := competitorConfigRows[name]; ok {
			fmt.Fprintf(w, row, meta.MaxDepth)
		}
	}
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "Scope: same-origin only for every tool (workloads are single-host; see\n")
	fmt.Fprintf(w, "w9-multi-host-scope exclusion above). See `internal/benchlab/tools.go` for\n")
	fmt.Fprintf(w, "the exact command line each tool is invoked with.\n\n")
	fmt.Fprintf(w, "**Fairness audit** (verified against each tool's own `-h`/`--help` output,\n")
	fmt.Fprintf(w, "not assumed): gospider's `-t 1 -c 5` are its own documented defaults, not a\n")
	fmt.Fprintf(w, "deliberately weak setting — `-t` only parallelizes across multiple seed\n")
	fmt.Fprintf(w, "sites, irrelevant here since every workload gives it exactly one. hakrawler's\n")
	fmt.Fprintf(w, "`-t 10` is slightly *above* its own default of 8. The one real, deliberate\n")
	fmt.Fprintf(w, "asymmetry: the 5s timeout is imposed uniformly by this harness and matches\n")
	fmt.Fprintf(w, "chcrawl's own default, but matches neither external tool's own default\n")
	fmt.Fprintf(w, "(hakrawler defaults to no timeout at all; gospider defaults to 10s) — a\n")
	fmt.Fprintf(w, "deliberate like-for-like choice over each tool's natural behavior, disclosed\n")
	fmt.Fprintf(w, "here rather than left implicit.\n\n")
}

// writeReproducibility lists each workload's interleaving seed; the full
// per-repetition tool order lives in the JSON report's run_logs field
// instead of here, since printing it in markdown would be one line per
// repetition per workload.
func writeReproducibility(w io.Writer, meta CompetitorReportMeta) {
	if len(meta.RunLogs) == 0 {
		return
	}
	names := make([]string, 0, len(meta.RunLogs))
	for name := range meta.RunLogs {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Fprintf(w, "### Reproducibility\n\n")
	fmt.Fprintf(w, "Tool order was independently randomized per repetition per workload (see\n")
	fmt.Fprintf(w, "Methodology above); each workload's seed reproduces its exact sequence.\n")
	fmt.Fprintf(w, "The full per-repetition order (not just the seed) is in the JSON report's\n")
	fmt.Fprintf(w, "`run_logs` field.\n\n")
	fmt.Fprintf(w, "| workload | seed |\n")
	fmt.Fprintf(w, "|---|---:|\n")
	for _, name := range names {
		fmt.Fprintf(w, "| %s | %d |\n", name, meta.RunLogs[name].Seed)
	}
	fmt.Fprintf(w, "\n")
}

func writeProductionTable(w io.Writer, workloads []string, results map[string]map[string]*CompetitorStats) {
	fmt.Fprintf(w, "| workload | competitor | runs | median | MAD | p95 | p99 | min | max | requests/sec | peak RSS | correctness | status |\n")
	fmt.Fprintf(w, "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|---|\n")
	for _, wl := range workloads {
		for _, tool := range competitorToolOrder() {
			cs := results[wl][tool]
			if cs == nil {
				continue
			}
			if !cs.Available {
				fmt.Fprintf(w, "| %s | %s | — | — | — | — | — | — | — | — | — | — | NOT INSTALLED |\n", wl, tool)
				continue
			}
			rps := fmt.Sprintf("%.1f", cs.MedianRPS)
			if cs.RPSIsApproximate {
				rps += "*"
			}
			fmt.Fprintf(w, "| %s | %s | %d | %s | %s | %s | %s | %s | %s | %s | %s | %d/%d | %s |\n",
				wl, tool, cs.Runs, fmtDur(cs.Duration.Median), fmtDur(cs.Duration.MAD),
				fmtDur(cs.Duration.P95), fmtDur(cs.Duration.P99), fmtDur(cs.Duration.Min), fmtDur(cs.Duration.Max),
				rps, competitorRSS(cs), cs.PassCount, cs.Runs, cs.Status)
		}
	}
	fmt.Fprintf(w, "\n`*` requests/sec is approximate (found-URLs/sec, not a raw HTTP request\n")
	fmt.Fprintf(w, "count) for tools that don't expose a request counter — only chcrawl's own\n")
	fmt.Fprintf(w, "JSONL summary reports true requests made. MAD is median absolute deviation\n")
	fmt.Fprintf(w, "from the median (see Methodology) — a small median gap between two tools\n")
	fmt.Fprintf(w, "that's within either tool's own MAD is noise, not a real difference.\n\n")
}

func competitorRSS(cs *CompetitorStats) string {
	if !cs.RSSAvailable {
		return "N/A"
	}
	return fmt.Sprintf("%s (max %s)", fmtBytes(cs.MedianPeakRSSBytes), fmtBytes(cs.MaxPeakRSSBytes))
}

func writeActiveWallTable(w io.Writer, workloads []string, results map[string]map[string]*CompetitorStats) {
	fmt.Fprintf(w, "| workload | competitor | active_wall (median) | retry backoff (median) | retry attempts (median run) |\n")
	fmt.Fprintf(w, "|---|---|---:|---:|---:|\n")
	for _, wl := range workloads {
		for _, tool := range competitorToolOrder() {
			cs := results[wl][tool]
			if cs == nil || !cs.Available {
				continue
			}
			if !cs.HasActiveWallInstrumentation {
				fmt.Fprintf(w, "| %s | %s | N/A | N/A | N/A |\n", wl, tool)
				continue
			}
			aw, bo, ra := medianActiveWallTriple(cs)
			fmt.Fprintf(w, "| %s | %s | %s | %s | %d |\n", wl, tool, fmtDur(aw), fmtDur(bo), ra)
		}
	}
	fmt.Fprintf(w, "\n")
}

// medianActiveWallTriple picks the sample at the median-Duration position
// (not an independent per-field median) so the reported active_wall/
// backoff/attempts values always come from one real, coherent run.
func medianActiveWallTriple(cs *CompetitorStats) (time.Duration, time.Duration, int64) {
	samples := append([]CompetitorSample(nil), cs.Samples...)
	sort.Slice(samples, func(i, j int) bool { return samples[i].Duration < samples[j].Duration })
	mid := samples[len(samples)/2]
	if mid.ActiveWall == nil {
		return 0, 0, 0
	}
	var bo time.Duration
	var ra int64
	if mid.RetryBackoff != nil {
		bo = *mid.RetryBackoff
	}
	if mid.RetryAttempts != nil {
		ra = *mid.RetryAttempts
	}
	return *mid.ActiveWall, bo, ra
}

func writeCorrectnessTable(w io.Writer, workloads []string, results map[string]map[string]*CompetitorStats) {
	order := competitorToolOrder()
	fmt.Fprintf(w, "| workload | oracle size | %s |\n", strings.Join(order, " | "))
	fmt.Fprintf(w, "|---|---:|%s\n", strings.Repeat("---|", len(order)))
	for _, wl := range workloads {
		var oracleSize int
		cells := make([]string, len(order))
		for i, tool := range order {
			cs := results[wl][tool]
			if cs == nil {
				cells[i] = "—"
				continue
			}
			if oracleSize == 0 {
				oracleSize = cs.GroundTruthTotal
			}
			if !cs.Available {
				cells[i] = "NOT INSTALLED"
				continue
			}
			cells[i] = fmt.Sprintf("%d/%d %s", cs.MinFound, cs.GroundTruthTotal, cs.Status)
		}
		fmt.Fprintf(w, "| %s | %d | %s |\n", wl, oracleSize, strings.Join(cells, " | "))
	}
	fmt.Fprintf(w, "\n")
	writeCorrectnessDetails(w, workloads, results)
}

// writeCorrectnessDetails lists, for every FAIL/FLAKY result, which
// ground-truth pages were missed and whether the same pages were missed
// every failing run or different pages on different runs. Silent for a
// workload/tool with no failures.
func writeCorrectnessDetails(w io.Writer, workloads []string, results map[string]map[string]*CompetitorStats) {
	var lines []string
	for _, wl := range workloads {
		for _, tool := range competitorToolOrder() {
			cs := results[wl][tool]
			if cs == nil || !cs.Available || cs.Status == "PASS" {
				continue
			}
			reproduced := "varies run to run, not the same pages every time"
			if cs.MissingPathsConsistent {
				reproduced = "reproduced identically on every failing run"
			}
			lines = append(lines, fmt.Sprintf("- **%s / %s** (%s, %d/%d passed, %s) — missing: %s",
				wl, tool, cs.Status, cs.PassCount, cs.Runs, reproduced, strings.Join(cs.EverMissingPaths, ", ")))
		}
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(w, "### Correctness detail\n\n")
	fmt.Fprintf(w, "%s\n\n", strings.Join(lines, "\n"))
}

func writeCompetitorLimitations(w io.Writer) {
	names := externalToolNames()
	fmt.Fprintf(w, "## Behavioral differences and limitations\n\n")
	fmt.Fprintf(w, "- **Retry/backoff instrumentation is chcrawl-only.** %s\n", joinNatural(names))
	fmt.Fprintf(w, "  expose no retry-count flag and no equivalent telemetry in their\n")
	fmt.Fprintf(w, "  output, so View 2 reports `N/A` for them rather than an inferred value.\n")
	fmt.Fprintf(w, "  This does not mean they never retry internally — only that this harness\n")
	fmt.Fprintf(w, "  has no way to observe it, and does not pretend otherwise.\n")
	fmt.Fprintf(w, "- **requests/sec is not uniformly measured.** Only chcrawl's JSONL summary\n")
	fmt.Fprintf(w, "  reports a true HTTP request count; for the other %d tools it's\n", len(names))
	fmt.Fprintf(w, "  approximated as found-URLs/sec (marked with `*`), a related but distinct\n")
	fmt.Fprintf(w, "  quantity — it excludes failed/redundant requests a tool may have made.\n")
	fmt.Fprintf(w, "- **Every invocation is a fresh OS process for every tool, including\n")
	fmt.Fprintf(w, "  chcrawl.** This is the real CLI experience (the metric this report treats\n")
	fmt.Fprintf(w, "  as primary), but it is a *different* metric from chcrawl-bench's own\n")
	fmt.Fprintf(w, "  default in-process engine benchmark, which reuses one warm process and\n")
	fmt.Fprintf(w, "  has no per-run process-spawn overhead. The two are not directly\n")
	fmt.Fprintf(w, "  comparable; where process startup dominates a small workload's wall time\n")
	fmt.Fprintf(w, "  for any tool, that's a real cost of the CLI invocation model, not\n")
	fmt.Fprintf(w, "  subtracted out.\n")
	fmt.Fprintf(w, "- **Correctness is scored against URL-reachability, not chcrawl's full\n")
	fmt.Fprintf(w, "  oracle.** The ground truth is `DiscoverablePaths` (the set of pages\n")
	fmt.Fprintf(w, "  genuinely reachable by following links/scripts/form actions) — the same\n")
	fmt.Fprintf(w, "  ground truth `-compare` already used. It does not attempt to score form\n")
	fmt.Fprintf(w, "  field extraction, parameter lists, or redirect-chain detail, since none\n")
	fmt.Fprintf(w, "  of the %d external tools report those in a comparably structured way.\n", len(names))
	fmt.Fprintf(w, "- **No headless/browser mode is enabled for any tool** — this is a plain\n")
	fmt.Fprintf(w, "  HTTP-crawling comparison. A separate JS-rendering benchmark, if wanted,\n")
	fmt.Fprintf(w, "  would need its own workloads and is out of scope here.\n")
	fmt.Fprintf(w, "- **hakrawler has no `-version` flag**; its reported version (if any) comes\n")
	fmt.Fprintf(w, "  from the Debian/Kali package manager on this specific machine and may be\n")
	fmt.Fprintf(w, "  unavailable elsewhere.\n")
	fmt.Fprintf(w, "- **No single \"winner\" score is computed.** Speed, correctness, and\n")
	fmt.Fprintf(w, "  resource usage are reported as separate views deliberately — collapsing\n")
	fmt.Fprintf(w, "  them into one number would hide exactly the tradeoffs (e.g. a fast tool\n")
	fmt.Fprintf(w, "  with incomplete discovery) this report exists to surface.\n")
}
