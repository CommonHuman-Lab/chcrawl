package benchlab

import (
	"math/rand"
	"sort"
	"time"
)

// PairwiseComparison is a statistically-hardened comparison of chcrawl's
// Duration samples against one competitor's, for one workload. Built from
// raw per-run samples (paired by workload/environment, not by run order —
// see RunCompetitorInterleaved's doc comment for why run order itself
// carries no meaning to pair on), using robust statistics appropriate for
// the skewed, often multimodal runtime distributions a real crawl produces
// (a handful of retries can make one sample an order of magnitude slower
// than the rest) rather than assuming normality.
type PairwiseComparison struct {
	Tool   string `json:"tool"`   // the competitor being compared against chcrawl
	NBase  int    `json:"n_base"` // chcrawl sample count
	NOther int    `json:"n_other"`

	MedianRatio float64 `json:"median_ratio"` // chcrawl median / other median; >1 means chcrawl is slower
	// MedianDiffNS is chcrawl's median minus the other tool's median, in
	// nanoseconds (positive = chcrawl slower).
	MedianDiffNS int64 `json:"median_diff_ns"`

	// CI95LowNS/CI95HighNS bound a 95% bootstrap confidence interval on
	// MedianDiffNS (see bootstrapMedianDiffCI's doc comment for the method).
	CI95LowNS  int64 `json:"ci95_low_ns"`
	CI95HighNS int64 `json:"ci95_high_ns"`

	// CliffsDelta is a nonparametric effect size in [-1, 1]: the probability
	// a random chcrawl sample is slower than a random other-tool sample,
	// minus the reverse probability. 0 = no tendency either way; magnitude
	// thresholds below follow Romano et al.'s conventional bands.
	CliffsDelta float64 `json:"cliffs_delta"`
	EffectLabel string  `json:"effect_label"` // negligible, small, medium, large

	// SignificantAtNoise is true only when the bootstrap CI on the median
	// difference excludes zero AND the difference exceeds both tools' own
	// MAD — the same "a gap smaller than either tool's MAD is noise" bar
	// this report already applies in prose elsewhere in the report,
	// combined with the CI so a single lucky/unlucky run can't flip it.
	SignificantAtNoise bool `json:"significant_at_noise"`
}

const bootstrapResamples = 2000

// bootstrapSeed is fixed (not wall-clock-derived) so the confidence interval
// itself is exactly reproducible from the same raw samples — unlike the
// interleaved tool-order seed (which is deliberately randomized per run and
// logged for reproducibility), there's no value in this one varying run to
// run since it isn't sampling anything about the environment, only
// resampling already-collected data.
const bootstrapSeed = 20260101

// bootstrapMedianDiffCI returns a 95% confidence interval on median(a) -
// median(b) via the percentile bootstrap: resample each of a and b with
// replacement (same size as the original) bootstrapResamples times,
// recompute the median difference each time, and take the 2.5th/97.5th
// percentiles of the resulting distribution. This makes no distributional
// assumption (no normality, no symmetry) — appropriate here since a
// crawler's runtime distribution is often right-skewed by retries/backoff
// and can be bimodal (a workload that sometimes needs a retry and sometimes
// doesn't).
func bootstrapMedianDiffCI(a, b []time.Duration) (low, high time.Duration) {
	if len(a) == 0 || len(b) == 0 {
		return 0, 0
	}
	rng := rand.New(rand.NewSource(bootstrapSeed))
	diffs := make([]float64, bootstrapResamples)
	sampleA := make([]time.Duration, len(a))
	sampleB := make([]time.Duration, len(b))
	for i := 0; i < bootstrapResamples; i++ {
		for j := range sampleA {
			sampleA[j] = a[rng.Intn(len(a))]
		}
		for j := range sampleB {
			sampleB[j] = b[rng.Intn(len(b))]
		}
		diffs[i] = float64(medianDuration(sampleA) - medianDuration(sampleB))
	}
	sort.Float64s(diffs)
	lowIdx := int(0.025 * float64(bootstrapResamples))
	highIdx := int(0.975 * float64(bootstrapResamples))
	if highIdx >= bootstrapResamples {
		highIdx = bootstrapResamples - 1
	}
	return time.Duration(diffs[lowIdx]), time.Duration(diffs[highIdx])
}

func medianDuration(d []time.Duration) time.Duration {
	sorted := append([]time.Duration(nil), d...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// cliffsDelta computes the nonparametric effect size described on
// PairwiseComparison.CliffsDelta: (#{a_i > b_j} - #{a_i < b_j}) / (n*m).
// O(n*m) — fine at this benchmark's sample sizes (tens of runs, not
// thousands).
func cliffsDelta(a, b []time.Duration) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var gt, lt int
	for _, x := range a {
		for _, y := range b {
			switch {
			case x > y:
				gt++
			case x < y:
				lt++
			}
		}
	}
	return float64(gt-lt) / float64(len(a)*len(b))
}

// effectLabel follows the conventional Cliff's-delta magnitude bands
// (Romano, Kromrey, Coraggio & Skowronek 2006): |δ| < 0.147 negligible,
// < 0.33 small, < 0.474 medium, otherwise large.
func effectLabel(delta float64) string {
	d := delta
	if d < 0 {
		d = -d
	}
	switch {
	case d < 0.147:
		return "negligible"
	case d < 0.33:
		return "small"
	case d < 0.474:
		return "medium"
	default:
		return "large"
	}
}

// computePairwise builds a PairwiseComparison of base (chcrawl) against
// other (one competitor) for one workload. Returns nil if either tool has
// no samples (not run, or unavailable) — a pairwise comparison against
// nothing isn't meaningful and shouldn't be silently reported as "no
// difference."
func computePairwise(tool string, base, other *CompetitorStats) *PairwiseComparison {
	if base == nil || other == nil || len(base.Samples) == 0 || len(other.Samples) == 0 {
		return nil
	}
	a := make([]time.Duration, len(base.Samples))
	for i, s := range base.Samples {
		a[i] = s.Duration
	}
	b := make([]time.Duration, len(other.Samples))
	for i, s := range other.Samples {
		b[i] = s.Duration
	}

	medA, medB := medianDuration(a), medianDuration(b)
	diff := medA - medB
	low, high := bootstrapMedianDiffCI(a, b)
	delta := cliffsDelta(a, b)

	ratio := 0.0
	if medB > 0 {
		ratio = float64(medA) / float64(medB)
	}

	ciExcludesZero := low > 0 || high < 0
	exceedsMAD := diff > base.Duration.MAD+other.Duration.MAD || -diff > base.Duration.MAD+other.Duration.MAD

	return &PairwiseComparison{
		Tool: tool, NBase: len(a), NOther: len(b),
		MedianRatio: ratio, MedianDiffNS: int64(diff),
		CI95LowNS: int64(low), CI95HighNS: int64(high),
		CliffsDelta: delta, EffectLabel: effectLabel(delta),
		SignificantAtNoise: ciExcludesZero && exceedsMAD,
	}
}
