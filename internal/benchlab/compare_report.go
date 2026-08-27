package benchlab

import (
	"fmt"
	"io"
	"sort"
	"time"
)

// WriteCompareReport renders a per-workload table scoring chcrawl against
// every external tool's coverage of the same local synthetic target.
func WriteCompareReport(w io.Writer, byWorkload map[string]map[string]*CompareResult, toolOrder []string) {
	workloads := make([]string, 0, len(byWorkload))
	for name := range byWorkload {
		workloads = append(workloads, name)
	}
	sort.Strings(workloads)

	fmt.Fprintf(w, "# chcrawl vs. external crawlers\n\n")
	fmt.Fprintf(w, "Generated %s. All tools crawl the exact same local (127.0.0.1-only) synthetic target per workload; coverage is scored against a ground-truth discoverable-path set derived directly from that workload's site graph (see benchlab.DiscoverablePaths).\n\n", time.Now().Format(time.RFC3339))

	for _, name := range workloads {
		results := byWorkload[name]
		fmt.Fprintf(w, "## %s\n\n", name)
		fmt.Fprintf(w, "| tool | found / total | recall | extra | duration |\n")
		fmt.Fprintf(w, "|---|---|---|---|---|\n")
		for _, toolName := range toolOrder {
			r, ok := results[toolName]
			if !ok {
				continue
			}
			if !r.Available {
				fmt.Fprintf(w, "| %s | — | — | — | not installed |\n", toolName)
				continue
			}
			errNote := ""
			if r.Err != nil {
				errNote = " (nonzero exit)"
			}
			fmt.Fprintf(w, "| %s | %d / %d | %.0f%% | %d | %s%s |\n",
				toolName, r.Found, r.Total, r.Recall(), r.Extra, r.Duration.Round(time.Millisecond), errNote)
		}
		fmt.Fprintf(w, "\n")
	}
}
