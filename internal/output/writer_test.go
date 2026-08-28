package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestWriter_WritePage_EmitsTypedJSONLine(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)

	if err := w.WritePage(PageEvent{URL: "https://example.com", Depth: 2}); err != nil {
		t.Fatalf("WritePage: %v", err)
	}

	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("expected trailing newline, got %q", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("expected exactly one line, got %q", out)
	}

	var decoded PageEvent
	if err := json.Unmarshal([]byte(strings.TrimSuffix(out, "\n")), &decoded); err != nil {
		t.Fatalf("line is not valid JSON: %v", err)
	}
	if decoded.Type != "page" {
		t.Errorf("Type = %q, want %q", decoded.Type, "page")
	}
	if decoded.URL != "https://example.com" || decoded.Depth != 2 {
		t.Errorf("decoded event = %+v, want URL=https://example.com Depth=2", decoded)
	}
}

func TestWriter_WriteError_SetsTypeField(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)

	if err := w.WriteError(ErrorEvent{URL: "https://example.com/x", Stage: "fetch", Error: "boom"}); err != nil {
		t.Fatalf("WriteError: %v", err)
	}

	var decoded ErrorEvent
	if err := json.Unmarshal(bytes.TrimSuffix(buf.Bytes(), []byte("\n")), &decoded); err != nil {
		t.Fatalf("line is not valid JSON: %v", err)
	}
	if decoded.Type != "error" {
		t.Errorf("Type = %q, want %q", decoded.Type, "error")
	}
}

// TestWriter_ConcurrentWrites_NeverTearLines guards the invariant write()
// depends on now that JSON marshaling happens before the lock is taken
// (moved out of the critical section to cut mutex contention — see write's
// doc comment): every line written to the underlying io.Writer must still
// be a single, complete, validly-formed JSON object. If the lock were ever
// held over anything less than the whole marshaled-record-plus-newline
// byte slice, concurrent writers could interleave partial lines.
func TestWriter_ConcurrentWrites_NeverTearLines(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)

	const n = 200
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = w.WritePage(PageEvent{URL: "https://example.com", Depth: i})
		}(i)
	}
	wg.Wait()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("got %d lines, want %d (a torn/interleaved write would change this count)", len(lines), n)
	}
	seenDepths := make(map[int]bool, n)
	for _, line := range lines {
		var decoded PageEvent
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("line is not valid JSON: %v\nline: %q", err, line)
		}
		seenDepths[decoded.Depth] = true
	}
	if len(seenDepths) != n {
		t.Errorf("got %d distinct depths, want %d", len(seenDepths), n)
	}
}
