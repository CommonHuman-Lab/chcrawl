package benchlab

import "context"

// ConcurrencyPoint is one worker-count measurement; only RunOptions.Concurrency
// varies so differences between points isolate concurrency's effect.
type ConcurrencyPoint struct {
	Concurrency int            `json:"concurrency"`
	Stats       *WorkloadStats `json:"stats"`
}

// ConcurrencySweepResult is one workload measured at several concurrency
// settings, to find the saturation point.
type ConcurrencySweepResult struct {
	Family string             `json:"family"`
	Scale  int                `json:"scale"`
	Levels []ConcurrencyPoint `json:"levels"`
}

// RunConcurrencySweep measures w at each level with the same runs/warmups
// throughout; only the global worker-pool size varies, PerHostConcurrency
// stays at w.Opts' value.
func RunConcurrencySweep(ctx context.Context, w ScaleWorkload, levels []int, runs, warmups int) (*ConcurrencySweepResult, error) {
	res := &ConcurrencySweepResult{Family: w.Family, Scale: w.Scale}
	for _, level := range levels {
		opts := w.Opts
		opts.Concurrency = level
		// Scale-family workloads are single-host by construction; sameOrigin=true is always valid.
		ws, err := RunMany(ctx, w.Site, true, opts, warmups, runs)
		if err != nil {
			return nil, err
		}
		res.Levels = append(res.Levels, ConcurrencyPoint{Concurrency: level, Stats: ws})
	}
	return res, nil
}
