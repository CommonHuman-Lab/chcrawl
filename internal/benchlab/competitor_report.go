package benchlab

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
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
}

// competitorToolOrder is the fixed, always-in-this-order presentation for
// every table — chcrawl first (the tool this benchmark exists to
// evaluate), then the three external tools in the order they were added.
var competitorToolOrder = []string{"chcrawl", "katana", "hakrawler", "gospider"}

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

	fmt.Fprintf(w, "# chcrawl vs. katana vs. hakrawler vs. gospider\n\n")
	writeCompetitorEnvironment(w, meta)

	fmt.Fprintf(w, "## Methodology\n\n")
	fmt.Fprintf(w, "Every tool crawls the identical local (127.0.0.1-only) synthetic target\n")
	fmt.Fprintf(w, "per workload, run as an external OS process (the real CLI invocation a\n")
	fmt.Fprintf(w, "user would type — not an in-process API call for any tool, chcrawl\n")
	fmt.Fprintf(w, "included), scored against the same DiscoverablePaths ground truth used by\n")
	fmt.Fprintf(w, "chcrawl's own oracle-diff correctness suite. %d warmup iterations are run\n", meta.Warmups)
	fmt.Fprintf(w, "and discarded (including from correctness accounting) before %d measured\n", meta.Runs)
	fmt.Fprintf(w, "iterations per tool per workload; every measured iteration starts fresh\n")
	fmt.Fprintf(w, "local HTTP servers. Percentiles use the nearest-rank method (`ceil(p/100 *\n")
	fmt.Fprintf(w, "n)`, 1-indexed) computed from individual per-run samples, identical to the\n")
	fmt.Fprintf(w, "methodology in chcrawl's own multi-run benchmark; with %d runs, P99's\n", meta.Runs)
	fmt.Fprintf(w, "nearest rank is the sample count itself, i.e. P99 == Max. Standard\n")
	fmt.Fprintf(w, "deviation is sample stddev (n-1 denominator). Peak RSS is the kernel's\n")
	fmt.Fprintf(w, "getrusage(RUSAGE_CHILDREN) Maxrss for that exact subprocess invocation\n")
	fmt.Fprintf(w, "(Linux) — genuinely isolated per invocation, with no cross-run or\n")
	fmt.Fprintf(w, "cross-tool contamination risk, since every invocation is already its own\n")
	fmt.Fprintf(w, "fresh OS process. `w9-multi-host-scope` is excluded: same-origin scope is\n")
	fmt.Fprintf(w, "implemented differently enough across these four tools (registered-domain\n")
	fmt.Fprintf(w, "matching, subdomain flags, no built-in concept at all) that scoring it\n")
	fmt.Fprintf(w, "wouldn't be a fair comparison.\n\n")
	writeCompetitorConfiguration(w, meta)

	fmt.Fprintf(w, "## View 1: production wall-clock\n\n")
	fmt.Fprintf(w, "The real end-to-end experience: process startup, HTTP work, parsing,\n")
	fmt.Fprintf(w, "scheduling, retries, and retry backoff where the tool has any — everything\n")
	fmt.Fprintf(w, "a user actually waits for from the CLI invocation. This is the primary,\n")
	fmt.Fprintf(w, "public number for every tool.\n\n")
	writeProductionTable(w, workloads, results)

	fmt.Fprintf(w, "## View 2: engine/active diagnostic\n\n")
	fmt.Fprintf(w, "Wall-clock time with measured retry backoff excluded — NOT CPU time, NOT\n")
	fmt.Fprintf(w, "a production number. Only chcrawl exposes this (`active_wall`, from its own\n")
	fmt.Fprintf(w, "JSONL summary record); none of katana/hakrawler/gospider expose an\n")
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
	fmt.Fprintf(w, "Tool versions: katana=%s hakrawler=%s gospider=%s\n",
		orNA(e.ToolVersions["katana"]), orNA(e.ToolVersions["hakrawler"]), orNA(e.ToolVersions["gospider"]))
	fmt.Fprintf(w, "```\n\n")
}

func orNA(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

func writeCompetitorConfiguration(w io.Writer, meta CompetitorReportMeta) {
	fmt.Fprintf(w, "### Tool configuration\n\n")
	fmt.Fprintf(w, "Every tool uses plain HTTP crawling — no headless/browser mode is enabled\n")
	fmt.Fprintf(w, "for any tool. All four are given the same crawl depth (%d).\n\n", meta.MaxDepth)
	fmt.Fprintf(w, "| tool | concurrency | depth | JS mode | retries | timeout | redirects | user agent | output |\n")
	fmt.Fprintf(w, "|---|---|---|---|---|---|---|---|---|\n")
	fmt.Fprintf(w, "| chcrawl | default (10 workers) | %d (-max-depth) | JS endpoint mining always on (static regex miner, no headless) | production default: 3 retries, exponential backoff, 429+5xx | 5s (-timeout) | followed (engine default cap) | chcrawl default | JSONL to stdout |\n", meta.MaxDepth)
	fmt.Fprintf(w, "| katana | -c 10 | %d (-d) | -jc (JS endpoint parsing, non-headless) | -retry 1 | -timeout 5 | followed (katana default) | katana default | -jsonl -silent |\n", meta.MaxDepth)
	fmt.Fprintf(w, "| hakrawler | -t 10 | %d (-d) | none (hakrawler has no JS-mining mode) | none exposed | -timeout 5 | followed (hakrawler default) | hakrawler default | -json |\n", meta.MaxDepth)
	fmt.Fprintf(w, "| gospider | -t 1 -c 5 | %d (-d) | linkfinder (regex JS miner, non-headless) | none exposed | -m 5 | followed (gospider default) | gospider default (\"web\") | plain tagged stdout |\n\n", meta.MaxDepth)
	fmt.Fprintf(w, "Scope: same-origin only for every tool (workloads are single-host; see\n")
	fmt.Fprintf(w, "w9-multi-host-scope exclusion above). See `internal/benchlab/tools.go` for\n")
	fmt.Fprintf(w, "the exact command line each tool is invoked with.\n\n")
}

func writeProductionTable(w io.Writer, workloads []string, results map[string]map[string]*CompetitorStats) {
	fmt.Fprintf(w, "| workload | competitor | runs | median | p95 | p99 | requests/sec | peak RSS | correctness | status |\n")
	fmt.Fprintf(w, "|---|---|---:|---:|---:|---:|---:|---:|---|---|\n")
	for _, wl := range workloads {
		for _, tool := range competitorToolOrder {
			cs := results[wl][tool]
			if cs == nil {
				continue
			}
			if !cs.Available {
				fmt.Fprintf(w, "| %s | %s | — | — | — | — | — | — | — | NOT INSTALLED |\n", wl, tool)
				continue
			}
			rps := fmt.Sprintf("%.1f", cs.MedianRPS)
			if cs.RPSIsApproximate {
				rps += "*"
			}
			fmt.Fprintf(w, "| %s | %s | %d | %s | %s | %s | %s | %s | %d/%d | %s |\n",
				wl, tool, cs.Runs, fmtDur(cs.Duration.Median), fmtDur(cs.Duration.P95), fmtDur(cs.Duration.P99),
				rps, competitorRSS(cs), cs.PassCount, cs.Runs, cs.Status)
		}
	}
	fmt.Fprintf(w, "\n`*` requests/sec is approximate (found-URLs/sec, not a raw HTTP request\n")
	fmt.Fprintf(w, "count) for tools that don't expose a request counter — only chcrawl's own\n")
	fmt.Fprintf(w, "JSONL summary reports true requests made.\n\n")
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
		for _, tool := range competitorToolOrder {
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
	fmt.Fprintf(w, "| workload | oracle size | chcrawl | katana | hakrawler | gospider |\n")
	fmt.Fprintf(w, "|---|---:|---|---|---|---|\n")
	for _, wl := range workloads {
		var oracleSize int
		cells := make([]string, len(competitorToolOrder))
		for i, tool := range competitorToolOrder {
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
		fmt.Fprintf(w, "| %s | %d | %s | %s | %s | %s |\n", wl, oracleSize, cells[0], cells[1], cells[2], cells[3])
	}
	fmt.Fprintf(w, "\n")
}

func writeCompetitorLimitations(w io.Writer) {
	fmt.Fprintf(w, "## Behavioral differences and limitations\n\n")
	fmt.Fprintf(w, "- **Retry/backoff instrumentation is chcrawl-only.** katana, hakrawler, and\n")
	fmt.Fprintf(w, "  gospider expose no retry-count flag and no equivalent telemetry in their\n")
	fmt.Fprintf(w, "  output, so View 2 reports `N/A` for them rather than an inferred value.\n")
	fmt.Fprintf(w, "  This does not mean they never retry internally — only that this harness\n")
	fmt.Fprintf(w, "  has no way to observe it, and does not pretend otherwise.\n")
	fmt.Fprintf(w, "- **requests/sec is not uniformly measured.** Only chcrawl's JSONL summary\n")
	fmt.Fprintf(w, "  reports a true HTTP request count; for the other three tools it's\n")
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
	fmt.Fprintf(w, "  of the three external tools report those in a comparably structured way.\n")
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
