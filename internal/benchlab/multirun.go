package benchlab

import (
	"context"
	"fmt"
)

// RunMany runs site through the real engine warmups+runs times (each a
// fully independent Run() call: fresh httptest servers, fresh engine, fresh
// dedup/frontier state — see Run), discards the warmups, and returns
// statistics computed from the runs measured samples.
//
// Correctness is checked on every measured sample, not just the first —
// see WorkloadStats.Status. A warmup iteration that itself fails the oracle
// diff is not treated as a correctness failure (only measured samples
// count), but a hard error from Run (e.g. a config/transport problem, not a
// discovery mismatch) aborts immediately during warmup or measurement
// alike, since that's not a benchmark result at all.
func RunMany(ctx context.Context, site *Site, sameOrigin bool, opts RunOptions, warmups, runs int) (*WorkloadStats, error) {
	if runs < 1 {
		runs = 1
	}
	if warmups < 0 {
		warmups = 0
	}

	for i := 0; i < warmups; i++ {
		if _, err := Run(ctx, site, sameOrigin, opts); err != nil {
			return nil, fmt.Errorf("benchlab: warmup %d/%d for %s: %w", i+1, warmups, site.Name, err)
		}
	}

	ws := &WorkloadStats{Workload: site.Name, Warmups: warmups}
	for i := 0; i < runs; i++ {
		r, err := Run(ctx, site, sameOrigin, opts)
		if err != nil {
			return nil, fmt.Errorf("benchlab: measured run %d/%d for %s: %w", i+1, runs, site.Name, err)
		}
		rps := 0.0
		if r.Duration.Seconds() > 0 {
			rps = float64(r.Summary.RequestsMade) / r.Duration.Seconds()
		}
		ws.Samples = append(ws.Samples, Sample{
			Duration:      r.Duration,
			ActiveWall:    r.Summary.ActiveWall,
			Backoff:       r.Summary.RetryBackoff,
			RetryAttempts: r.Summary.RetryAttempts,
			RequestsMade:  r.Summary.RequestsMade,
			RPS:           rps,
			PeakRSSBytes:  r.PeakRSSBytes,
			Passed:        r.Passed(),
			Diffs:         r.Diffs,
		})
	}

	ws.finalize()
	return ws, nil
}
