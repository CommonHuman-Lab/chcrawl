package benchlab

import (
	"testing"
	"time"
)

func samplesOf(durations ...time.Duration) []CompetitorSample {
	s := make([]CompetitorSample, len(durations))
	for i, d := range durations {
		s[i] = CompetitorSample{Duration: d, Passed: true}
	}
	return s
}

func TestComputePairwise_ClearlySlowerIsSignificant(t *testing.T) {
	base := &CompetitorStats{Samples: samplesOf(100*time.Millisecond, 102*time.Millisecond, 98*time.Millisecond, 101*time.Millisecond, 99*time.Millisecond)}
	base.finalize()
	other := &CompetitorStats{Samples: samplesOf(10*time.Millisecond, 11*time.Millisecond, 9*time.Millisecond, 10*time.Millisecond, 10*time.Millisecond)}
	other.finalize()

	pc := computePairwise("other", base, other)
	if pc == nil {
		t.Fatal("computePairwise returned nil")
	}
	if pc.MedianRatio < 8 || pc.MedianRatio > 12 {
		t.Errorf("MedianRatio = %v, want ~10", pc.MedianRatio)
	}
	if pc.CliffsDelta != 1 {
		t.Errorf("CliffsDelta = %v, want 1 (base always slower)", pc.CliffsDelta)
	}
	if pc.EffectLabel != "large" {
		t.Errorf("EffectLabel = %q, want large", pc.EffectLabel)
	}
	if !pc.SignificantAtNoise {
		t.Error("SignificantAtNoise = false, want true for a 10x, non-overlapping difference")
	}
	if pc.CI95LowNS <= 0 {
		t.Errorf("CI95LowNS = %d, want > 0 (base is unambiguously slower)", pc.CI95LowNS)
	}
}

func TestComputePairwise_OverlappingIsNotSignificant(t *testing.T) {
	base := &CompetitorStats{Samples: samplesOf(10*time.Millisecond, 12*time.Millisecond, 9*time.Millisecond, 11*time.Millisecond, 10*time.Millisecond)}
	base.finalize()
	other := &CompetitorStats{Samples: samplesOf(10*time.Millisecond, 11*time.Millisecond, 10*time.Millisecond, 9*time.Millisecond, 12*time.Millisecond)}
	other.finalize()

	pc := computePairwise("other", base, other)
	if pc == nil {
		t.Fatal("computePairwise returned nil")
	}
	if pc.EffectLabel != "negligible" {
		t.Errorf("EffectLabel = %q, want negligible for near-identical samples", pc.EffectLabel)
	}
	if pc.SignificantAtNoise {
		t.Error("SignificantAtNoise = true, want false for overlapping/near-identical samples")
	}
}

func TestComputePairwise_NilWhenNoSamples(t *testing.T) {
	base := &CompetitorStats{}
	base.finalize()
	other := &CompetitorStats{Samples: samplesOf(10 * time.Millisecond)}
	other.finalize()
	if pc := computePairwise("other", base, other); pc != nil {
		t.Errorf("computePairwise = %+v, want nil when base has no samples", pc)
	}
}

func TestCliffsDelta_KnownCases(t *testing.T) {
	allGreater := []time.Duration{10, 11, 12}
	allLess := []time.Duration{1, 2, 3}
	if d := cliffsDelta(allGreater, allLess); d != 1 {
		t.Errorf("cliffsDelta(allGreater, allLess) = %v, want 1", d)
	}
	if d := cliffsDelta(allLess, allGreater); d != -1 {
		t.Errorf("cliffsDelta(allLess, allGreater) = %v, want -1", d)
	}
	identical := []time.Duration{5, 5, 5}
	if d := cliffsDelta(identical, identical); d != 0 {
		t.Errorf("cliffsDelta(identical, identical) = %v, want 0", d)
	}
}
