package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/commonhuman-lab/chcrawl/internal/frontier"
)

// BenchmarkPipelineContention choreographs goroutines against real
// hostLimiter + frontier.Frontier objects, reproducing pipeline.go's
// actual call order without real HTTP/parse work (which would reintroduce
// the wall-clock noise the whole topology-controlled investigation found
// on this machine — see the delivered scale-bench report's reproducibility
// section): `concurrency` goroutines each Acquire the single shared host,
// then either (one goroutine, the "burst" role, matching a page with N
// discovered links) does N sequential frontier.Push calls before
// Release, or (the rest, "steady" roles, matching every other worker
// independently draining the frontier) loop frontier.Pop.
//
// The `release=before|after` sub-benchmark is the direct A/B test for
// Phase 1's fix: "before" releases the host slot right after the burst
// role "would have" finished its fetch (i.e. immediately, before the push
// loop) — matching the proposed pipeline.go change; "after" holds the
// slot through the whole push loop — matching current behavior. Compare
// the two to validate the fix in isolation before pipeline.go is touched,
// and re-run after the real edit lands as a sanity check that the
// synthetic model and the real change agree in direction.
//
// Run with:
//
//	go test ./internal/engine -bench BenchmarkPipelineContention -benchtime=10x -run '^$'
//	go test ./internal/engine -bench 'BenchmarkPipelineContention100k/release=after' -benchtime=5x -run '^$' -blockprofile=pipeline_after_block.out
func BenchmarkPipelineContention10k(b *testing.B) {
	for _, releaseBefore := range []bool{false, true} {
		name := "release=after"
		if releaseBefore {
			name = "release=before"
		}
		b.Run(name, func(b *testing.B) {
			runPipelineContention(b, 10_000, releaseBefore)
		})
	}
}

func BenchmarkPipelineContention100k(b *testing.B) {
	for _, releaseBefore := range []bool{false, true} {
		name := "release=after"
		if releaseBefore {
			name = "release=before"
		}
		b.Run(name, func(b *testing.B) {
			runPipelineContention(b, 100_000, releaseBefore)
		})
	}
}

// runPipelineContention runs one iteration of the choreography described
// above. concurrency/perHost match config.defaults() (10, 4) — the actual
// production defaults, not tuned for this benchmark.
func runPipelineContention(b *testing.B, n int, releaseBefore bool) {
	const concurrency = 10
	const perHost = 4
	const host = "example.com"
	ctx := context.Background()
	b.ReportAllocs()

	for iter := 0; iter < b.N; iter++ {
		b.StopTimer()
		h := newHostLimiter(perHost)
		f := frontier.New(n + concurrency + 1)
		var popped int64
		drained := make(chan struct{})
		var wg sync.WaitGroup

		// Steady roles mirror the real worker()/process() order exactly:
		// Pop first, then Acquire/Release per item — so every steady
		// goroutine keeps re-contending the host slot on every single item
		// it processes (matching "workers parked in Acquire's select for
		// the whole crawl"), rather than acquiring once and holding it,
		// which would just permanently block most of them behind
		// perHost's capacity instead of creating ongoing contention.
		steady := concurrency - 1
		for s := 0; s < steady; s++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					_, ok, err := f.Pop(ctx)
					if err != nil || !ok {
						return
					}
					if err := h.Acquire(ctx, host); err != nil {
						b.Errorf("steady Acquire: %v", err)
						return
					}
					h.Release(host)
					if atomic.AddInt64(&popped, 1) == int64(n) {
						close(drained)
					}
				}
			}()
		}
		b.StartTimer()

		if err := h.Acquire(ctx, host); err != nil {
			b.Fatalf("burst Acquire: %v", err)
		}
		if releaseBefore {
			h.Release(host)
		}
		for i := 0; i < n; i++ {
			if err := f.Push(ctx, frontier.Item{URL: "burst"}); err != nil {
				b.Fatalf("Push: %v", err)
			}
		}
		if !releaseBefore {
			h.Release(host)
		}
		<-drained

		b.StopTimer()
		f.Close()
		wg.Wait()
		b.StartTimer()
	}
	b.ReportMetric(float64(n)*float64(b.N)/b.Elapsed().Seconds(), "pushes/sec")
}
