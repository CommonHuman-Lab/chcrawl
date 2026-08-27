package benchlab

import (
	"testing"
	"time"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func TestPercentile_NearestRank(t *testing.T) {
	sorted := []time.Duration{ms(1), ms(2), ms(3), ms(4), ms(5), ms(6), ms(7), ms(8), ms(9), ms(10)}
	cases := []struct {
		p    float64
		want time.Duration
	}{
		{50, ms(5)},  // ceil(0.5*10)=5 -> sorted[4]=5
		{90, ms(9)},  // ceil(0.9*10)=9 -> sorted[8]=9
		{95, ms(10)}, // ceil(0.95*10)=10 -> sorted[9]=10
		{99, ms(10)}, // ceil(0.99*10)=10 -> sorted[9]=10 (P99==Max at N=10, documented)
		{100, ms(10)},
	}
	for _, c := range cases {
		if got := percentile(sorted, c.p); got != c.want {
			t.Errorf("percentile(p=%v) = %v, want %v", c.p, got, c.want)
		}
	}
}

func TestPercentile_Empty(t *testing.T) {
	if got := percentile(nil, 50); got != 0 {
		t.Errorf("percentile(nil) = %v, want 0", got)
	}
}

func TestComputeMetricStats_DoesNotMutateInput(t *testing.T) {
	in := []time.Duration{ms(5), ms(1), ms(3)}
	orig := append([]time.Duration(nil), in...)
	computeMetricStats(in)
	for i := range in {
		if in[i] != orig[i] {
			t.Fatalf("computeMetricStats mutated its input slice: got %v, want %v", in, orig)
		}
	}
}

func TestComputeMetricStats_MinMaxMedian(t *testing.T) {
	s := computeMetricStats([]time.Duration{ms(10), ms(1), ms(5)})
	if s.Min != ms(1) || s.Max != ms(10) || s.Median != ms(5) {
		t.Errorf("got min=%v max=%v median=%v", s.Min, s.Max, s.Median)
	}
}

func TestSampleStdDev_KnownValue(t *testing.T) {
	// [2,4,6] ms, mean=4: squared deviations 4,0,4 -> sum 8, /(n-1=2) = 4,
	// sqrt(4) = 2ms exactly.
	sorted := []time.Duration{ms(2), ms(4), ms(6)}
	mean := ms(4)
	got := sampleStdDev(sorted, mean)
	if got != ms(2) {
		t.Errorf("sampleStdDev = %v, want 2ms", got)
	}
}

func TestSampleStdDev_SingleSample(t *testing.T) {
	if got := sampleStdDev([]time.Duration{ms(5)}, ms(5)); got != 0 {
		t.Errorf("sampleStdDev of 1 sample = %v, want 0 (undefined variance)", got)
	}
}

func TestWorkloadStats_Finalize_AllPass(t *testing.T) {
	ws := &WorkloadStats{Samples: []Sample{{Passed: true}, {Passed: true}, {Passed: true}}}
	ws.finalize()
	if ws.Status != "PASS" || ws.PassCount != 3 {
		t.Errorf("got status=%s passCount=%d, want PASS/3", ws.Status, ws.PassCount)
	}
}

func TestWorkloadStats_Finalize_AllFail(t *testing.T) {
	ws := &WorkloadStats{Samples: []Sample{{Passed: false, Diffs: []string{"x"}}, {Passed: false}}}
	ws.finalize()
	if ws.Status != "FAIL" || ws.PassCount != 0 {
		t.Errorf("got status=%s passCount=%d, want FAIL/0", ws.Status, ws.PassCount)
	}
}

func TestWorkloadStats_Finalize_Flaky(t *testing.T) {
	ws := &WorkloadStats{Samples: []Sample{
		{Passed: true}, {Passed: false, Diffs: []string{"mismatch"}}, {Passed: true},
	}}
	ws.finalize()
	if ws.Status != "FLAKY" || ws.PassCount != 2 {
		t.Errorf("got status=%s passCount=%d, want FLAKY/2", ws.Status, ws.PassCount)
	}
	if len(ws.FailureExamples) != 1 {
		t.Errorf("expected 1 failure example captured, got %d", len(ws.FailureExamples))
	}
}

func TestWorkloadStats_Finalize_CapsFailureExamples(t *testing.T) {
	var samples []Sample
	for i := 0; i < maxFailureExamples+5; i++ {
		samples = append(samples, Sample{Passed: false, Diffs: []string{"x"}})
	}
	ws := &WorkloadStats{Samples: samples}
	ws.finalize()
	if len(ws.FailureExamples) != maxFailureExamples {
		t.Errorf("FailureExamples len = %d, want capped at %d", len(ws.FailureExamples), maxFailureExamples)
	}
}

func TestBasisStats_SelectsCorrectSeries(t *testing.T) {
	ws := &WorkloadStats{
		Duration:   MetricStats{Median: ms(100)},
		ActiveWall: MetricStats{Median: ms(5)},
	}
	if got := ws.BasisStats(BasisDuration).Median; got != ms(100) {
		t.Errorf("BasisDuration median = %v, want 100ms", got)
	}
	if got := ws.BasisStats(BasisActiveWall).Median; got != ms(5) {
		t.Errorf("BasisActiveWall median = %v, want 5ms", got)
	}
}
