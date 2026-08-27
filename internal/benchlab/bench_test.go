package benchlab

import (
	"context"
	"strings"
	"testing"
	"time"
)

// BenchmarkWorkloads runs each workload as a Go benchmark, reporting
// throughput and discovery-quality metrics alongside the standard timing.
// Run with: go test ./internal/benchlab/ -bench=. -benchtime=5x -run=^$
func BenchmarkWorkloads(b *testing.B) {
	for name, site := range Workloads() {
		name, site := name, site
		b.Run(name, func(b *testing.B) {
			sameOrigin := !strings.Contains(name, "multi-host")
			for i := 0; i < b.N; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				result, err := Run(ctx, site, sameOrigin, RunOptions{})
				cancel()
				if err != nil {
					b.Fatalf("Run: %v", err)
				}
				if !result.Passed() {
					b.Fatalf("workload %s: discovery mismatch vs oracle:\n%s", name, strings.Join(result.Diffs, "\n"))
				}
				b.ReportMetric(float64(result.Summary.RequestsMade)/result.Duration.Seconds(), "requests/sec")
				b.ReportMetric(float64(result.Summary.URLsInScope)/result.Duration.Seconds(), "unique-discoveries/sec")
				b.ReportMetric(float64(result.PeakRSSBytes)/(1024*1024), "peak-MB")
			}
		})
	}
}
