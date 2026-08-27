package benchlab

import (
	"fmt"
	"io"
	"sort"
	"time"
)

// WriteReport renders a human-readable summary of a benchmark run: a
// per-workload table (timing, throughput, memory, correctness) followed by
// a plain-language note on any workload that didn't match its oracle.
func WriteReport(w io.Writer, results map[string]*Result) {
	names := make([]string, 0, len(results))
	for name := range results {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Fprintf(w, "# chcrawl benchmark report\n\n")
	fmt.Fprintf(w, "Generated %s. All workloads are 100%% local (127.0.0.1-only), no external network.\n\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(w, "| workload | duration | requests/sec | unique in-scope | requests | ok | failed | peak RSS | correctness |\n")
	fmt.Fprintf(w, "|---|---|---|---|---|---|---|---|---|\n")

	var failed []string
	for _, name := range names {
		r := results[name]
		status := "PASS"
		if !r.Passed() {
			status = "FAIL"
			failed = append(failed, name)
		}
		rps := 0.0
		if r.Duration.Seconds() > 0 {
			rps = float64(r.Summary.RequestsMade) / r.Duration.Seconds()
		}
		fmt.Fprintf(w, "| %s | %s | %.1f | %d | %d | %d | %d | %.1f MB | %s |\n",
			name, r.Duration.Round(time.Millisecond), rps,
			r.Summary.URLsInScope, r.Summary.RequestsMade, r.Summary.ResponsesOK, r.Summary.ResponsesFailed,
			float64(r.PeakRSSBytes)/(1024*1024), status)
	}

	fmt.Fprintf(w, "\n")
	if len(failed) == 0 {
		fmt.Fprintf(w, "All workloads matched their oracle exactly: every discovered URL, form, param, redirect, and duplicate was accounted for.\n")
		return
	}
	fmt.Fprintf(w, "## Correctness failures\n\n")
	for _, name := range failed {
		fmt.Fprintf(w, "### %s\n\n", name)
		for _, d := range results[name].Diffs {
			fmt.Fprintf(w, "- %s\n", d)
		}
		fmt.Fprintf(w, "\n")
	}
}
