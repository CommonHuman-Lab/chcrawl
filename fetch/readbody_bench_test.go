package fetch

import (
	"bytes"
	"io"
	"testing"
)

// benchReadAllBaseline isolates io.ReadAll's own cost with no size hint —
// the exact code path readBody replaces when contentLength is unknown, and
// the pre-existing behavior this whole file's benchmarks are measured
// against.
func benchReadAllBaseline(b *testing.B, size int) {
	data := bytes.Repeat([]byte{'a'}, size)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		body, err := io.ReadAll(bytes.NewReader(data))
		if err != nil {
			b.Fatal(err)
		}
		if len(body) != size {
			b.Fatalf("len=%d, want %d", len(body), size)
		}
	}
}

// benchReadBodyKnownLength isolates readBody's pre-sized path — what a real
// response with an accurate Content-Length header exercises.
func benchReadBodyKnownLength(b *testing.B, size int) {
	data := bytes.Repeat([]byte{'a'}, size)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		body, err := readBody(bytes.NewReader(data), int64(size), int64(size)+1)
		if err != nil {
			b.Fatal(err)
		}
		if len(body) != size {
			b.Fatalf("len=%d, want %d", len(body), size)
		}
	}
}

func BenchmarkReadAllBaseline_1KB(b *testing.B)   { benchReadAllBaseline(b, 1<<10) }
func BenchmarkReadAllBaseline_100KB(b *testing.B) { benchReadAllBaseline(b, 100<<10) }
func BenchmarkReadAllBaseline_5MB(b *testing.B)   { benchReadAllBaseline(b, 5<<20) }

func BenchmarkReadBodyKnownLength_1KB(b *testing.B)   { benchReadBodyKnownLength(b, 1<<10) }
func BenchmarkReadBodyKnownLength_100KB(b *testing.B) { benchReadBodyKnownLength(b, 100<<10) }
func BenchmarkReadBodyKnownLength_5MB(b *testing.B)   { benchReadBodyKnownLength(b, 5<<20) }
