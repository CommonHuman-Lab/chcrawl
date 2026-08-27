package benchlab

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWorkloads_MatchOracle(t *testing.T) {
	for name, site := range Workloads() {
		site := site
		t.Run(name, func(t *testing.T) {
			sameOrigin := !strings.Contains(name, "multi-host")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			result, err := Run(ctx, site, sameOrigin, RunOptions{})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !result.Passed() {
				t.Errorf("workload %s: crawl result did not match oracle:\n%s", name, strings.Join(result.Diffs, "\n"))
			}
		})
	}
}

func TestWorkload_MultiHostScope_SameOriginExcludesOtherHost(t *testing.T) {
	site := w9MultiHostScope()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := Run(ctx, site, true, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Passed() {
		t.Errorf("same-origin run did not match oracle:\n%s", strings.Join(result.Diffs, "\n"))
	}
	// host "a" has 3 pages (/, /local1, /local2); host "b" must be excluded entirely.
	if result.Summary.URLsInScope != 3 {
		t.Errorf("expected 3 in-scope URLs under same-origin, got %d", result.Summary.URLsInScope)
	}
}

func TestWorkload_MultiHostScope_CrossOriginIncludesBothHosts(t *testing.T) {
	site := w9MultiHostScope()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := Run(ctx, site, false, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Passed() {
		t.Errorf("cross-origin run did not match oracle:\n%s", strings.Join(result.Diffs, "\n"))
	}
	// all 5 pages across both hosts should be reachable.
	if result.Summary.URLsInScope != 5 {
		t.Errorf("expected 5 in-scope URLs under same_origin=false, got %d", result.Summary.URLsInScope)
	}
}
