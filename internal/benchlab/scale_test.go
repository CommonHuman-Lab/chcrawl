package benchlab

import (
	"context"
	"testing"
	"time"
)

// runScaleOnce runs w through the real engine once and fails the test with
// full diff detail on any oracle mismatch — the same correctness bar every
// other benchlab workload is held to.
func runScaleOnce(t *testing.T, w ScaleWorkload) *Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, err := Run(ctx, w.Site, true, w.Opts)
	if err != nil {
		t.Fatalf("%s: Run: %v", w.Site.Name, err)
	}
	if !r.Passed() {
		t.Fatalf("%s: oracle mismatch: %v", w.Site.Name, r.Diffs)
	}
	return r
}

func TestS1WideFlat_SmallScaleCorrect(t *testing.T) {
	for _, n := range []int{1, 5, 50} {
		w := s1WideFlat(n)
		r := runScaleOnce(t, w)
		wantPages := int64(n + 1)
		if r.Summary.ResponsesOK != wantPages {
			t.Errorf("n=%d: ResponsesOK = %d, want %d", n, r.Summary.ResponsesOK, wantPages)
		}
	}
}

func TestS1bWideDistributed_SmallScaleCorrect(t *testing.T) {
	for _, n := range []int{1, 5, 50, 300} {
		w := s1bWideDistributed(n)
		r := runScaleOnce(t, w)
		hubCount := s1bHubCount(n)
		leavesPerHub := n / hubCount
		if leavesPerHub < 1 {
			leavesPerHub = 1
		}
		wantHubsPlusLeaves := hubCount*leavesPerHub + hubCount // Scale: leaves+hubs, root excluded (matches ScaleWorkload.Scale's own accounting)
		wantPages := int64(wantHubsPlusLeaves + 1)             // ResponsesOK: leaves+hubs+root
		if r.Summary.ResponsesOK != wantPages {
			t.Errorf("n=%d: ResponsesOK = %d, want %d (hubs=%d, leaves/hub=%d)", n, r.Summary.ResponsesOK, wantPages, hubCount, leavesPerHub)
		}
		if w.Scale != wantHubsPlusLeaves {
			t.Errorf("n=%d: w.Scale = %d, want %d", n, w.Scale, wantHubsPlusLeaves)
		}
	}
}

// No single document in S1b should approach S1's giant-root-page size —
// verify the root page here links to hubs only (hubCount links), never to
// every leaf directly, at every scale S1bScales actually uses.
func TestS1bWideDistributed_NoDocumentHoldsAllLinks(t *testing.T) {
	for _, n := range S1bScales {
		w := s1bWideDistributed(n)
		for _, p := range w.Site.Pages {
			if len(p.Links) > n/10 {
				t.Errorf("s1b n=%d: page %q holds %d links (>10%% of total %d) — defeats the point of a distributed topology",
					n, p.Path, len(p.Links), n)
			}
		}
	}
}

func TestS1cBalancedTree_SmallScaleCorrect(t *testing.T) {
	for _, target := range []int{1, 11, 111, 1111} {
		w := s1cBalancedTree(target)
		r := runScaleOnce(t, w)
		_, wantTotal := treeDepthForTarget(target)
		if r.Summary.ResponsesOK != int64(wantTotal) {
			t.Errorf("target=%d: ResponsesOK = %d, want %d", target, r.Summary.ResponsesOK, wantTotal)
		}
		if w.Scale != wantTotal {
			t.Errorf("target=%d: w.Scale = %d, want %d", target, w.Scale, wantTotal)
		}
	}
}

// TestS1cBalancedTree_DepthHeadroomDoesNotTruncate checks that
// s1cBalancedTree's default MaxDepth comfortably covers the tree's actual
// depth. A truncated MaxDepth can't surface as an oracle mismatch (Run's
// oracle uses the same RunOptions as the real crawl), so this checks the
// headroom directly instead.
func TestS1cBalancedTree_DepthHeadroomDoesNotTruncate(t *testing.T) {
	for _, target := range S1cScales {
		w := s1cBalancedTree(target)
		depth, _ := treeDepthForTarget(target)
		if w.Opts.MaxDepth <= depth {
			t.Errorf("target=%d: MaxDepth=%d does not exceed the tree's actual depth=%d — discovery would be truncated",
				target, w.Opts.MaxDepth, depth)
		}
	}
}

func TestS2DeepChain_SmallScaleCorrect(t *testing.T) {
	for _, n := range []int{1, 2, 40} {
		w := s2DeepChain(n)
		r := runScaleOnce(t, w)
		if r.Summary.ResponsesOK != int64(n) {
			t.Errorf("n=%d: ResponsesOK = %d, want %d", n, r.Summary.ResponsesOK, n)
		}
	}
}

func TestS3HighDuplication_VariantsCollapse(t *testing.T) {
	w := s3HighDuplication(300) // unique = 100 (300/10, clamped >=100)
	r := runScaleOnce(t, w)
	unique := s3UniqueCount(300)
	wantPages := int64(unique + 1)
	if r.Summary.ResponsesOK != wantPages {
		t.Fatalf("ResponsesOK = %d, want %d (unique=%d)", r.Summary.ResponsesOK, wantPages, unique)
	}
	wantDup := int64(300 - unique)
	if r.Summary.DuplicatesRejected != wantDup {
		t.Errorf("DuplicatesRejected = %d, want %d — trailing-slash/fragment variants did not collapse as expected",
			r.Summary.DuplicatesRejected, wantDup)
	}
}

func TestS3QueryCanonicalizationDemo_RequiresSortQueryParams(t *testing.T) {
	w := S3QueryCanonicalizationDemo()
	if !w.Opts.SortQueryParams {
		t.Fatal("S3QueryCanonicalizationDemo must set SortQueryParams=true — that's the entire point of the demo")
	}
	r := runScaleOnce(t, w)
	// unique targets = 200, hardcoded in S3QueryCanonicalizationDemo.
	const unique = 200
	if r.Summary.ResponsesOK != unique+1 {
		t.Errorf("ResponsesOK = %d, want %d — query-order variants did not collapse under SortQueryParams=true",
			r.Summary.ResponsesOK, unique+1)
	}
}

func TestS3QueryVariants_DoNotCollapseUnderDefaultCanonicalization(t *testing.T) {
	// Sanity check on the documented claim in scale.go: without
	// SortQueryParams, reordered-query variants are genuinely distinct
	// URLs to chcrawl. Build a tiny two-variant case directly (not via the
	// demo helper, which forces SortQueryParams=true) to prove it.
	site := &Site{
		Name: "scale-test-query-no-sort",
		Seed: "/",
		Pages: []PageSpec{
			{Path: "/", Links: []string{"/q?a=1&b=2", "/q?b=2&a=1"}},
			{Path: "/q"},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := Run(ctx, site, true, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !r.Passed() {
		t.Fatalf("oracle mismatch (default canonicalization should treat these as 2 distinct URLs): %v", r.Diffs)
	}
	if r.Summary.ResponsesOK != 3 { // "/" + both query variants, fetched separately
		t.Errorf("ResponsesOK = %d, want 3 (query-order variants must stay distinct without SortQueryParams)", r.Summary.ResponsesOK)
	}
}

func TestS4LargeHTML_LinksSurviveAtEveryConfiguredSize(t *testing.T) {
	for _, kb := range []int{1, 64} { // keep the test fast; full sizes are exercised at bench time
		w := s4LargeHTML(kb)
		r := runScaleOnce(t, w)
		if r.Summary.ResponsesOK != int64(s4LinkCount+1) {
			t.Errorf("kb=%d: ResponsesOK = %d, want %d", kb, r.Summary.ResponsesOK, s4LinkCount+1)
		}
	}
}

func TestS4DefaultBodyCapDemo_LinksSurviveTruncation(t *testing.T) {
	w := S4DefaultBodyCapDemo()
	if w.Opts.MaxBodyBytes != 0 {
		t.Fatalf("S4DefaultBodyCapDemo must leave MaxBodyBytes at 0 (production default), got %d", w.Opts.MaxBodyBytes)
	}
	r := runScaleOnce(t, w)
	if r.Summary.ResponsesOK != int64(s4LinkCount+1) {
		t.Errorf("ResponsesOK = %d, want %d — truncation at the default MaxBodyBytes cap should not cost any links, since real links are always emitted before filler bytes",
			r.Summary.ResponsesOK, s4LinkCount+1)
	}
}

func TestS5ParameterHeavy_SmallScaleCorrect(t *testing.T) {
	for _, n := range []int{1, 5, 60} {
		w := s5ParameterHeavy(n)
		r := runScaleOnce(t, w)
		if r.Summary.ResponsesOK != int64(n+1) {
			t.Errorf("n=%d: ResponsesOK = %d, want %d", n, r.Summary.ResponsesOK, n+1)
		}
		wantParams := int64(n * 4)
		if r.Summary.Params != wantParams {
			t.Errorf("n=%d: Params = %d, want %d", n, r.Summary.Params, wantParams)
		}
	}
}

func TestS6MixedRealistic_SmallScaleCorrect(t *testing.T) {
	for _, n := range []int{20, 200} {
		w := s6MixedRealistic(n)
		runScaleOnce(t, w) // correctness is the assertion; every branch is oracle-checked generically
	}
}

func TestBuildScaleWorkload_DispatchesEveryFamily(t *testing.T) {
	for _, family := range ScaleFamilies() {
		scales := DefaultScales(family)
		if len(scales) == 0 {
			t.Errorf("%s: no default scales registered", family)
			continue
		}
		w, err := BuildScaleWorkload(family, scales[0])
		if err != nil {
			t.Errorf("%s: BuildScaleWorkload: %v", family, err)
			continue
		}
		if w.Site == nil {
			t.Errorf("%s: nil Site", family)
		}
	}
	if _, err := BuildScaleWorkload("nonexistent-family", 1); err == nil {
		t.Error("expected an error for an unknown family")
	}
}

func TestBuildScaleWorkload_DeterministicAcrossCalls(t *testing.T) {
	// This is the property spawnWorker/runInternalWorker-style subprocess
	// isolation depends on: the parent's practicality probe and the
	// re-exec'd measurement subprocess must build byte-for-byte the same
	// Site from the same (family, scale) — see BuildScaleWorkload's doc.
	a, err := BuildScaleWorkload("S1-wide-flat", 25)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildScaleWorkload("S1-wide-flat", 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Site.Pages) != len(b.Site.Pages) {
		t.Fatalf("non-deterministic page count: %d vs %d", len(a.Site.Pages), len(b.Site.Pages))
	}
	for i := range a.Site.Pages {
		if a.Site.Pages[i].Path != b.Site.Pages[i].Path {
			t.Fatalf("non-deterministic page %d path: %q vs %q", i, a.Site.Pages[i].Path, b.Site.Pages[i].Path)
		}
	}
}
