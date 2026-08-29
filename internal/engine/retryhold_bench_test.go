package engine

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func BenchmarkRetryHoldVsRelease(b *testing.B) {
	const perHost = 4
	const concurrency = 10
	const host = "example.com"
	backoff := 100 * time.Millisecond

	cases := []struct {
		n          int // total items on the host
		flakyCount int // how many of them are flaky (sleep `backoff` before completing)
	}{
		{n: 20, flakyCount: 2},  // matches w16's real shape: few flaky among a few healthy
		{n: 20, flakyCount: 8},  // flaky count exceeds perHost: the shape that should matter
		{n: 40, flakyCount: 16}, // same ratio, larger n
	}

	for _, tc := range cases {
		name := fmt.Sprintf("n=%d/flaky=%d", tc.n, tc.flakyCount)
		b.Run(name+"/hold", func(b *testing.B) {
			runRetryHold(b, tc.n, tc.flakyCount, perHost, concurrency, host, backoff, true)
		})
		b.Run(name+"/release", func(b *testing.B) {
			runRetryHold(b, tc.n, tc.flakyCount, perHost, concurrency, host, backoff, false)
		})
	}
}

func runRetryHold(b *testing.B, n, flakyCount, perHost, concurrency int, host string, backoff time.Duration, hold bool) {
	ctx := context.Background()
	b.ReportAllocs()

	for iter := 0; iter < b.N; iter++ {
		h := newHostLimiter(perHost)
		items := make(chan int, n)
		for i := 0; i < n; i++ {
			items <- i
		}
		close(items)

		var wg sync.WaitGroup
		for w := 0; w < concurrency; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range items {
					flaky := i < flakyCount
					if err := h.Acquire(ctx, host); err != nil {
						b.Errorf("Acquire: %v", err)
						return
					}
					if flaky {
						if hold {
							time.Sleep(backoff)
						} else {
							h.Release(host)
							time.Sleep(backoff)
							if err := h.Acquire(ctx, host); err != nil {
								b.Errorf("re-Acquire: %v", err)
								return
							}
						}
					}
					h.Release(host)
				}
			}()
		}
		wg.Wait()
	}
}
