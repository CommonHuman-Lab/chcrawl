package frontier

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// Isolated many-goroutine contention benchmarks for the frontier's shared
// channel, reproducing two real crawl shapes (see internal/benchlab/scale.go):
// "burst" pushes N items back-to-back with no gaps (the S1-wide-flat shape,
// one page holding N links); "staggered" spreads the same N items across
// many short-lived producers of itemsPerStaggeredProducer each (the
// S1c-balanced-tree shape). Unlike the sequential push-then-pop benchmark in
// diagnostic_bench_test.go, these reproduce actual many-goroutine contention.
//
//	go test ./internal/frontier -bench BenchmarkFrontier -benchtime=5x -run '^$'

// popperCounts: 10 matches config.defaults().Concurrency (the production
// default); 32 checks a higher worker count.
var popperCounts = []int{10, 32}

// itemsPerStaggeredProducer matches S1c-balanced-tree's branch factor (see
// internal/benchlab/scale.go's s1cBalancedTree).
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

// runBurstContention starts `poppers` goroutines parked in Pop, then does n
// sequential Push calls with no gaps — the shape one worker holding a
// host-limiter slot through a page's whole discovery loop produces in the
// real engine (see pipeline.go's maybeEnqueueChild/enqueue).
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
// goroutines, each pushing only itemsPerStaggeredProducer items — matching
// S1c-balanced-tree, where each page only ever discovers 10 links and a
// fresh worker is required to fetch+parse each one. This is the in-repo
// control: it should scale closer to linearly with n and show a different
// contention profile than the burst variant.
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
