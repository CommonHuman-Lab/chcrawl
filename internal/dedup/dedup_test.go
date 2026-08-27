package dedup

import (
	"sync"
	"testing"
)

func TestVisitedSet_MarkIfNew_FirstTrueThenFalse(t *testing.T) {
	s := New()
	if !s.MarkIfNew("a") {
		t.Error("first MarkIfNew(a) should return true")
	}
	if s.MarkIfNew("a") {
		t.Error("second MarkIfNew(a) should return false")
	}
	if !s.MarkIfNew("b") {
		t.Error("MarkIfNew(b) should return true for a distinct key")
	}
	if s.Len() != 2 {
		t.Errorf("Len() = %d, want 2", s.Len())
	}
}

func TestVisitedSet_ConcurrentMarkIfNew_ExactlyOneWinnerPerKey(t *testing.T) {
	s := New()
	const workers = 50
	const keys = 20

	var wins [keys]int
	var mu sync.Mutex
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < keys; k++ {
				if s.MarkIfNew(keyFor(k)) {
					mu.Lock()
					wins[k]++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	for k, w := range wins {
		if w != 1 {
			t.Errorf("key %d: MarkIfNew returned true %d times across %d concurrent callers, want exactly 1 (dedup must be atomic)", k, w, workers)
		}
	}
	if s.Len() != keys {
		t.Errorf("Len() = %d, want %d", s.Len(), keys)
	}
}

func keyFor(i int) string {
	return string(rune('a' + i))
}
