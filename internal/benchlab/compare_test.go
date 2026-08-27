package benchlab

import "testing"

func TestScoreAgainstGround(t *testing.T) {
	ground := map[string]bool{"/": true, "/about": true, "/search": true}
	found := map[string]bool{
		"http://127.0.0.1:1/":                    true,
		"http://127.0.0.1:1/about":               true,
		"http://127.0.0.1:1/search?q=x&sort=asc": true, // query dropped, still counts
		"http://127.0.0.1:1/nonexistent":         true, // extra
		"http://other-host:1/about":              true, // out of scope, ignored
	}
	r := &CompareResult{Total: len(ground)}
	scoreAgainstGround(r, "http://127.0.0.1:1/", found, ground)
	if r.Found != 3 {
		t.Errorf("Found = %d, want 3", r.Found)
	}
	if r.Extra != 1 {
		t.Errorf("Extra = %d, want 1", r.Extra)
	}
}

func TestCompareResult_Recall(t *testing.T) {
	r := CompareResult{Found: 3, Total: 4}
	if got := r.Recall(); got != 75.0 {
		t.Errorf("Recall() = %v, want 75.0", got)
	}
	if got := (CompareResult{Total: 0}).Recall(); got != 0 {
		t.Errorf("Recall() with Total=0 = %v, want 0", got)
	}
}
