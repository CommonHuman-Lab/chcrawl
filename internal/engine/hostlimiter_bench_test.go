package engine

import (
	"context"
	"sync"
	"testing"
)

// BenchmarkHostLimiterContention isolates the per-host semaphore's real
// contention pattern: Concurrency-many (config.defaults().Concurrency,
// see internal/config/config.go) goroutines doing repeated Acquire→(trivial
// work)→Release cycles against one hostLimiter for one host at
// PerHostConcurrency (also config.defaults()) capacity — the "6 of 10
// workers parked in Acquire's select for the whole crawl" structural
// contention identified for single-host workloads, distinct from and
// present independent of the frontier's burst-vs-staggered discovery
// pattern (see internal/frontier/frontier_bench_test.go). Run with:
//
//	go test ./internal/engine -bench BenchmarkHostLimiterContention -benchtime=20x -run '^$'
//	go test ./internal/engine -bench BenchmarkHostLimiterContention -benchtime=20x -run '^$' -blockprofile=hostlimiter_block.out
func BenchmarkHostLimiterContention(b *testing.B) {
	// concurrency/perHost match config.defaults() exactly (10, 4) — not
	// tuned for this benchmark, the actual production defaults.
	const concurrency = 10
	const perHost = 4
	const cyclesPerWorker = 500

	for iter := 0; iter < b.N; iter++ {
		h := newHostLimiter(perHost)
		ctx := context.Background()
		var wg sync.WaitGroup
		for w := 0; w < concurrency; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for c := 0; c < cyclesPerWorker; c++ {
					if err := h.Acquire(ctx, "example.com"); err != nil {
						b.Errorf("Acquire: %v", err)
						return
					}
					h.Release("example.com")
				}
			}()
		}
		wg.Wait()
	}
	b.ReportMetric(float64(concurrency*cyclesPerWorker*b.N)/b.Elapsed().Seconds(), "acquire-release-cycles/sec")
}
