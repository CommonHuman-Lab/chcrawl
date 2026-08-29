package benchlab

import (
	"context"
	"testing"
)

// Real-engine, real-HTTP-server benchmarks at the actual scale of specific
// competitor workloads (as opposed to the 10k/100k synthetic scale in
// diagnostic_bench_test.go) — used to CPU/alloc-profile exactly the paths
// the competitor benchmark measures, e.g.:
//
//	go test ./internal/benchlab -run '^$' -bench BenchmarkWorkloadEngine/w17 \
//	  -cpuprofile=/tmp/w17.cpu.prof -memprofile=/tmp/w17.mem.prof -benchtime=200x
func benchWorkloadEngine(b *testing.B, site *Site, sameOrigin bool) {
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := Run(ctx, site, sameOrigin, RunOptions{})
		if err != nil {
			b.Fatal(err)
		}
		if !res.Passed() {
			b.Fatalf("oracle mismatch: %v", res.Diffs)
		}
	}
}

func BenchmarkWorkloadEngine(b *testing.B) {
	b.Run("w17-medium-scale", func(b *testing.B) { benchWorkloadEngine(b, w17MediumScale(), true) })
	b.Run("w19-duplication-stress", func(b *testing.B) { benchWorkloadEngine(b, w19DuplicationStress(), true) })
	b.Run("w2-deep-tree", func(b *testing.B) { benchWorkloadEngine(b, w2DeepTree(), true) })
	b.Run("w3-wide-site", func(b *testing.B) { benchWorkloadEngine(b, w3WideSite(), true) })
	b.Run("w1-small-static", func(b *testing.B) { benchWorkloadEngine(b, w1SmallStatic(), true) })
}
