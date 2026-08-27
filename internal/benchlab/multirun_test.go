package benchlab

import (
	"context"
	"testing"
	"time"
)

func TestRunMany_CollectsMeasuredSamplesOnly(t *testing.T) {
	site := w1SmallStatic()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ws, err := RunMany(ctx, site, true, RunOptions{}, 2 /*warmups*/, 5 /*runs*/)
	if err != nil {
		t.Fatalf("RunMany: %v", err)
	}
	if ws.Warmups != 2 {
		t.Errorf("Warmups = %d, want 2", ws.Warmups)
	}
	if len(ws.Samples) != 5 {
		t.Fatalf("len(Samples) = %d, want 5 (warmups must not be included)", len(ws.Samples))
	}
	if ws.Runs != 5 {
		t.Errorf("Runs = %d, want 5", ws.Runs)
	}
}

func TestRunMany_EveryMeasuredRunValidatedAgainstOracle(t *testing.T) {
	site := w1SmallStatic()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ws, err := RunMany(ctx, site, true, RunOptions{}, 0, 10)
	if err != nil {
		t.Fatalf("RunMany: %v", err)
	}
	if ws.Status != "PASS" {
		t.Fatalf("expected PASS, got %s (passCount=%d/%d)", ws.Status, ws.PassCount, ws.Runs)
	}
	if ws.PassCount != 10 {
		t.Errorf("PassCount = %d, want 10", ws.PassCount)
	}
	for i, s := range ws.Samples {
		if !s.Passed {
			t.Errorf("sample %d unexpectedly failed: %v", i, s.Diffs)
		}
	}
}

func TestRunMany_ClampsInvalidRunsAndWarmups(t *testing.T) {
	site := w1SmallStatic()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ws, err := RunMany(ctx, site, true, RunOptions{}, -3, 0)
	if err != nil {
		t.Fatalf("RunMany: %v", err)
	}
	if ws.Warmups != 0 {
		t.Errorf("Warmups = %d, want 0 (negative input clamped)", ws.Warmups)
	}
	if len(ws.Samples) != 1 {
		t.Fatalf("runs<1 should clamp to 1 measured run, got %d", len(ws.Samples))
	}
}

func TestRunMany_StatsPopulatedFromRealSamples(t *testing.T) {
	site := w1SmallStatic()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	ws, err := RunMany(ctx, site, true, RunOptions{}, 0, 8)
	if err != nil {
		t.Fatalf("RunMany: %v", err)
	}
	if ws.Duration.Max < ws.Duration.Min {
		t.Errorf("Duration.Max (%v) < Duration.Min (%v)", ws.Duration.Max, ws.Duration.Min)
	}
	if ws.Duration.Median == 0 && ws.Duration.Max > 0 {
		t.Errorf("Duration.Median is 0 but Max is %v — median looks wrong", ws.Duration.Max)
	}
	if ws.MedianRPS <= 0 {
		t.Errorf("MedianRPS = %v, want > 0 for a workload with real requests", ws.MedianRPS)
	}
}
