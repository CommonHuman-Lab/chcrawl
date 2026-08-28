package benchlab

import "time"

// runTiers pairs run/warmup counts with a tier index — the position of a
// scale within its family's ascending default-scale list. The smallest
// scale gets the full 30-run/5-warmup treatment; the count shrinks as scale
// grows since exhaustively repeating a 100,000-URL crawl 30 times isn't a
// good use of a CI-sized budget. Every WorkloadStats still reports its own
// actual Runs/Warmups, so the reduction is always visible in the result.
var runTiers = []struct{ Runs, Warmups int }{
	{30, 5},
	{15, 3},
	{5, 2},
	{3, 1},
}

// RunTierFor returns the (runs, warmups) pair for the tier at index
// (0-based position within a family's ascending scale list). index is
// clamped into range, so a family with more scales than defined tiers
// simply reuses the smallest (last) tier for every scale beyond it.
func RunTierFor(index int) (runs, warmups int) {
	if index < 0 {
		index = 0
	}
	if index >= len(runTiers) {
		index = len(runTiers) - 1
	}
	t := runTiers[index]
	return t.Runs, t.Warmups
}

// Practicality-probe ceilings: a throwaway probe run checks these before a
// full tiered measurement. Exceeding either excludes that scale and every
// larger scale in the family, rather than running unboundedly long.
const (
	ScaleWallClockCeiling = 90 * time.Second
	ScalePeakRSSCeiling   = 2 << 30 // 2 GiB
)

// ScalePoint is one (family, scale) measurement outcome: either a full
// WorkloadStats (Excluded=false) or a documented practicality exclusion
// (Excluded=true), never a silent gap.
type ScalePoint struct {
	Scale             int            `json:"scale"`
	Stats             *WorkloadStats `json:"stats,omitempty"`
	Excluded          bool           `json:"excluded"`
	Reason            string         `json:"reason,omitempty"`
	ProbeDuration     time.Duration  `json:"probe_duration_ns,omitempty"`
	ProbePeakRSSBytes uint64         `json:"probe_peak_rss_bytes,omitempty"`
}

// ScaleFamilyResult is one family's full ascending-scale sweep.
type ScaleFamilyResult struct {
	Family string       `json:"family"`
	Points []ScalePoint `json:"points"`
}

// ScalingRatio compares two consecutive *measured* (non-excluded) points
// in the same family: how much scale grew vs. how much runtime/memory
// grew. With only two points this is a ratio, not a fitted curve — see
// Classification's doc comment.
type ScalingRatio struct {
	FromScale           int     `json:"from_scale"`
	ToScale             int     `json:"to_scale"`
	ScaleRatio          float64 `json:"scale_ratio"`
	MedianDurationRatio float64 `json:"median_duration_ratio"`
	MedianRSSRatio      float64 `json:"median_rss_ratio"`
	// Classification is a coarse descriptive label (±25% band around parity
	// as "roughly linear"), not a statistical fit — two points can't support one.
	Classification string `json:"classification"`
}

func classifyRatio(observedRatio, scaleRatio float64) string {
	if scaleRatio <= 0 {
		return "n/a"
	}
	rel := observedRatio / scaleRatio
	switch {
	case rel < 0.75:
		return "sub-linear"
	case rel > 1.25:
		return "super-linear"
	default:
		return "roughly linear"
	}
}

func ratioDuration(b, a time.Duration) float64 {
	if a <= 0 {
		return 0
	}
	return float64(b) / float64(a)
}

func ratioUint64(b, a uint64) float64 {
	if a == 0 {
		return 0
	}
	return float64(b) / float64(a)
}

// ComputeScalingRatios walks a family's ascending, measured (non-excluded)
// points and returns the ratio between each consecutive pair.
func ComputeScalingRatios(points []ScalePoint) []ScalingRatio {
	var out []ScalingRatio
	var prev *ScalePoint
	for i := range points {
		p := &points[i]
		if p.Excluded || p.Stats == nil {
			continue
		}
		if prev != nil {
			scaleRatio := float64(p.Scale) / float64(prev.Scale)
			durRatio := ratioDuration(p.Stats.Duration.Median, prev.Stats.Duration.Median)
			rssRatio := ratioUint64(p.Stats.MedianPeakRSSBytes, prev.Stats.MedianPeakRSSBytes)
			out = append(out, ScalingRatio{
				FromScale:           prev.Scale,
				ToScale:             p.Scale,
				ScaleRatio:          scaleRatio,
				MedianDurationRatio: durRatio,
				MedianRSSRatio:      rssRatio,
				Classification:      classifyRatio(durRatio, scaleRatio),
			})
		}
		prev = p
	}
	return out
}

// DeltaRSSPer1000 is the median-peak-RSS change between two consecutive
// measured points, normalized per 1,000 units of scale. Computed pairwise,
// never as one global fit, so a two-point family isn't implied linear.
func DeltaRSSPer1000(a, b ScalePoint) (bytesPer1000 float64, ok bool) {
	if a.Excluded || b.Excluded || a.Stats == nil || b.Stats == nil {
		return 0, false
	}
	dScale := float64(b.Scale - a.Scale)
	if dScale == 0 {
		return 0, false
	}
	dRSS := float64(int64(b.Stats.MedianPeakRSSBytes) - int64(a.Stats.MedianPeakRSSBytes))
	return dRSS / (dScale / 1000.0), true
}
