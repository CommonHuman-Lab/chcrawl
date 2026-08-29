package fetch

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestReadBody_KnownLengthMatchesReadAll(t *testing.T) {
	data := strings.Repeat("x", 5000)
	got, err := readBody(strings.NewReader(data), int64(len(data)), 1<<20)
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	if string(got) != data {
		t.Fatalf("got %d bytes, want %d", len(got), len(data))
	}
}

func TestReadBody_UnknownLengthFallsBackToReadAll(t *testing.T) {
	data := strings.Repeat("y", 5000)
	got, err := readBody(strings.NewReader(data), -1, 1<<20)
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	if string(got) != data {
		t.Fatalf("got %d bytes, want %d", len(got), len(data))
	}
}

func TestReadBody_HintLargerThanMaxBodyBytesFallsBack(t *testing.T) {
	// The caller already wraps r in io.LimitReader(maxBodyBytes+1), so this
	// exercises readBody's own fallback branch in isolation: a contentLength
	// hint exceeding maxBodyBytes must not pre-allocate that much.
	data := strings.Repeat("z", 100)
	got, err := readBody(strings.NewReader(data), 10_000_000, 1<<20)
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	if string(got) != data {
		t.Fatalf("got %q, want %q", got, data)
	}
}

func TestReadBody_HintShorterThanActualBodyStillReadsEverythingAvailable(t *testing.T) {
	// A lying/stale Content-Length must not truncate the read — bytes.Buffer
	// grows past its initial capacity exactly like io.ReadAll would.
	data := strings.Repeat("w", 10_000)
	got, err := readBody(strings.NewReader(data), 10, 1<<20)
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	if string(got) != data {
		t.Fatalf("got %d bytes, want %d", len(got), len(data))
	}
}

func TestReadBody_EmptyBody(t *testing.T) {
	got, err := readBody(strings.NewReader(""), 0, 1<<20)
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d bytes, want 0", len(got))
	}
}

func TestReadBody_MatchesIOReadAllAcrossSizes(t *testing.T) {
	sizes := []int{0, 1, 511, 512, 513, 4096, 100_000}
	hints := []int64{-1, 0, 1, 512, 100_000, 10_000_000}
	for _, n := range sizes {
		data := bytes.Repeat([]byte{'a'}, n)
		want, err := io.ReadAll(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("io.ReadAll: %v", err)
		}
		for _, hint := range hints {
			got, err := readBody(bytes.NewReader(data), hint, 1<<20)
			if err != nil {
				t.Fatalf("readBody(size=%d, hint=%d): %v", n, hint, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("readBody(size=%d, hint=%d) = %d bytes, want %d bytes", n, hint, len(got), len(want))
			}
		}
	}
}
