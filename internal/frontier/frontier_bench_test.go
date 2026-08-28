package frontier

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// Isolated many-goroutine contention benchmarks for the frontier's shared
// channel, reproducing two real crawl shapes from the topology-controlled
// scaling investigation (see internal/benchlab/scale.go's S1-wide-flat vs
// S1c-balanced-tree, and cmd/chcrawl-bench's delivered scale-bench report):
//
//   - "burst": one goroutine pushes N items back-to-back with no work
//     between pushes, while several goroutines are already parked in Pop,
//     waiting — the S1-wide-flat shape (one page holding N links).
//   - "staggered": the same N items pushed by N/itemsPerProducer producer
//     goroutines (itemsPerProducer=10, matching S1c-balanced-tree's real
//     branch factor), each pushing only its own 10 items before exiting —
//     the S1c shape (many pages, 10 links each, no single goroutine ever
//     pushing more than 10 in a row).
//
// These are NOT full-crawl wall-clock benchmarks (already known to be
// noisy on a shared/desktop machine — see the delivered scale-bench
// report's reproducibility section) and are NOT the existing
// single-goroutine diagnostic benchmarks in
// internal/benchlab/diagnostic_bench_test.go, whose frontier benchmark is
// deliberately sequential (one goroutine push-then-pop) and explicitly
// cannot reproduce many-goroutine contention. Run with:
//
//	go test ./internal/frontier -bench BenchmarkFrontier -benchtime=5x -run '^$'
//
// To reconfirm the block-profile shape in isolation (the burst variant
// should be selectgo-dominated; the staggered variant should not):
//
//	go test ./internal/frontier -bench BenchmarkFrontierBurstContention100k -benchtime=3x -run '^$' -blockprofile=burst_block.out
//	go tool pprof -top burst_block.out
//	go test ./internal/frontier -bench BenchmarkFrontierStaggeredContention100k -benchtime=3x -run '^$' -blockprofile=staggered_block.out
//	go tool pprof -top staggered_block.out

// popperCounts are the worker-pool sizes exercised as a sub-benchmark
// dimension: 10 matches config.defaults().Concurrency (the production
// default); 32 is a higher count to see how the pattern scales with more
// workers than the bug report itself isolated.
var popperCounts = []int{10, 32}

// itemsPerStaggeredProducer matches S1c-balanced-tree's actual branch
// factor (see internal/benchlab/scale.go's s1cBalancedTree) — not a tuned
// constant, the same structural value the real topology comparison used.
const itemsPerStaggeredProducer = 10

func BenchmarkFrontierBurstContention10k(b *testing.B) {
	for _, poppers := range popperCounts {
		b.Run(fmt.Sprintf("poppers=%d", poppers), func(b *testing.B) {
			runBurstContention(b, 10_000, poppers)
		})
	}
}

func BenchmarkFrontierBurstContention100k(b *testing.B) {
	for _, poppers := range popperCounts {
		b.Run(fmt.Sprintf("poppers=%d", poppers), func(b *testing.B) {
			runBurstContention(b, 100_000, poppers)
		})
	}
}

func BenchmarkFrontierStaggeredContention10k(b *testing.B) {
	for _, poppers := range popperCounts {
		b.Run(fmt.Sprintf("poppers=%d", poppers), func(b *testing.B) {
			runStaggeredContention(b, 10_000, poppers)
		})
	}
}

func BenchmarkFrontierStaggeredContention100k(b *testing.B) {
	for _, poppers := range popperCounts {
		b.Run(fmt.Sprintf("poppers=%d", poppers), func(b *testing.B) {
			runStaggeredContention(b, 100_000, poppers)
		})
	}
}

// runBurstContention starts `poppers` goroutines looping Pop, waits until
// every one of them has actually entered the loop (so they're genuinely
// parked in Pop's select when the burst starts, not merely scheduled),
// then does n sequential Push calls on the benchmark goroutine itself with
// no work between them — the exact shape one worker holding a host-limiter
// slot through a page's whole discovery loop produces in the real engine
// (see internal/engine/pipeline.go's maybeEnqueueChild/enqueue).
func runBurstContention(b *testing.B, n, poppers int) {
	ctx := context.Background()
	b.ReportAllocs()

	for iter := 0; iter < b.N; iter++ {
		b.StopTimer()
		f := New(n + 1)
		var popped int64
		drained := make(chan struct{})
		ready := make(chan struct{}, poppers)
		var wg sync.WaitGroup
		for p := 0; p < poppers; p++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ready <- struct{}{}
				for {
					_, ok, err := f.Pop(ctx)
					if err != nil || !ok {
						return
					}
					if atomic.AddInt64(&popped, 1) == int64(n) {
						close(drained)
					}
				}
			}()
		}
		for p := 0; p < poppers; p++ {
			<-ready
		}
		b.StartTimer()

		for i := 0; i < n; i++ {
			if err := f.Push(ctx, Item{URL: "burst"}); err != nil {
				b.Fatalf("Push: %v", err)
			}
		}
		<-drained

		b.StopTimer()
		f.Close()
		wg.Wait()
		b.StartTimer()
	}
	b.ReportMetric(float64(n)*float64(b.N)/b.Elapsed().Seconds(), "pushes/sec")
}

// runStaggeredContention pushes the same total n items as runBurstContention,
// but spread across n/itemsPerStaggeredProducer independent producer
// goroutines, each pushing only itemsPerStaggeredProducer items before
// exiting — matching the real structural difference between S1-wide-flat
// and S1c-balanced-tree: in the real engine, one worker goroutine holding
// its host-limiter slot pushes *all* of one page's discoveries in a tight
// loop before releasing it (see maybeEnqueueChild/enqueue in
// internal/engine/pipeline.go); S1c's pages only ever discover 10 links
// each, so no single goroutine ever pushes more than 10 in a row, and each
// page requires a fresh worker to actually fetch+parse it first — modeled
// here by using many short-lived producer goroutines rather than one
// goroutine looping n times. (An earlier version of this benchmark added
// runtime.Gosched() between every individual push to simulate a "gap" —
// that was wrong: it doesn't model inter-*page* fetch latency, it just
// adds scheduling churn between individual links on what the real engine
// still processes as one uninterrupted burst, and measurably made the
// block profile worse, not better. The goroutine-count structure alone is
// the real variable.) This is the in-repo control: it should scale closer
// to linearly with n and show a different contention profile than the
// burst variant, proving this harness isolates the topology effect the
// real S1-vs-S1c comparison found, not just raw item count.
func runStaggeredContention(b *testing.B, n, poppers int) {
	ctx := context.Background()
	producers := n / itemsPerStaggeredProducer
	if producers < 1 {
		producers = 1
	}
	b.ReportAllocs()

	for iter := 0; iter < b.N; iter++ {
		b.StopTimer()
		f := New(n + 1)
		var popped int64
		drained := make(chan struct{})
		ready := make(chan struct{}, poppers)
		var wg sync.WaitGroup
		for p := 0; p < poppers; p++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ready <- struct{}{}
				for {
					_, ok, err := f.Pop(ctx)
					if err != nil || !ok {
						return
					}
					if atomic.AddInt64(&popped, 1) == int64(n) {
						close(drained)
					}
				}
			}()
		}
		for p := 0; p < poppers; p++ {
			<-ready
		}

		var pushErr atomic.Bool
		var pwg sync.WaitGroup
		itemsPerProducer := n / producers
		remainder := n % producers
		b.StartTimer()

		for p := 0; p < producers; p++ {
			count := itemsPerProducer
			if p < remainder {
				count++
			}
			pwg.Add(1)
			go func(count int) {
				defer pwg.Done()
				for i := 0; i < count; i++ {
					if err := f.Push(ctx, Item{URL: "staggered"}); err != nil {
						pushErr.Store(true)
						return
					}
				}
			}(count)
		}
		pwg.Wait()
		if pushErr.Load() {
			b.Fatal("Push failed in a producer goroutine")
		}
		<-drained

		b.StopTimer()
		f.Close()
		wg.Wait()
		b.StartTimer()
	}
	b.ReportMetric(float64(n)*float64(b.N)/b.Elapsed().Seconds(), "pushes/sec")
}
