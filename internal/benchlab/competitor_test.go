package benchlab

import (
	"context"
	"os/exec"
	"reflect"
	"testing"
)

// fakeToolUsing "true" (a standard POSIX no-op that exits 0 immediately)
// lets these tests exercise RunCompetitorInterleaved's orchestration —
// seed reproducibility, interleaving, sample bookkeeping — without needing
// a real external crawler binary or spawning the chcrawl build.
func fakeTool(name string) Tool {
	return Tool{
		Name:   name,
		Binary: "true",
		Build: func(ctx context.Context, binary, seedURL string, maxDepth int) *exec.Cmd {
			return exec.CommandContext(ctx, binary)
		},
		Parse: func(seedURL string, stdout []byte) map[string]bool {
			return map[string]bool{seedURL: true}
		},
	}
}

func TestRunCompetitorInterleaved_SameSeedReproducesSameOrder(t *testing.T) {
	site := w11SinglePage()
	tools := []Tool{fakeTool("a"), fakeTool("b"), fakeTool("c")}
	ctx := context.Background()

	_, log1 := RunCompetitorInterleaved(ctx, site, 5, 2, 3, tools, 42)
	_, log2 := RunCompetitorInterleaved(ctx, site, 5, 2, 3, tools, 42)

	if !reflect.DeepEqual(log1.Repetitions, log2.Repetitions) {
		t.Fatalf("same seed produced different orders:\n%+v\n%+v", log1.Repetitions, log2.Repetitions)
	}
}

func TestRunCompetitorInterleaved_DifferentSeedsCanDiffer(t *testing.T) {
	site := w11SinglePage()
	tools := []Tool{fakeTool("a"), fakeTool("b"), fakeTool("c"), fakeTool("d"), fakeTool("e")}
	ctx := context.Background()

	_, log1 := RunCompetitorInterleaved(ctx, site, 5, 0, 8, tools, 1)
	_, log2 := RunCompetitorInterleaved(ctx, site, 5, 0, 8, tools, 2)

	if reflect.DeepEqual(log1.Repetitions, log2.Repetitions) {
		t.Fatalf("different seeds produced identical orders across %d repetitions — suspiciously coincidental or the seed isn't wired through", len(log1.Repetitions))
	}
}

func TestRunCompetitorInterleaved_NotBlockedByTool(t *testing.T) {
	site := w11SinglePage()
	tools := []Tool{fakeTool("a"), fakeTool("b")}
	ctx := context.Background()

	_, log := RunCompetitorInterleaved(ctx, site, 5, 0, 6, tools, 7)

	// A blocked order runs every "a" before any "b" (or vice versa); an
	// interleaved order should not, with overwhelming probability across 6
	// repetitions of a 2-tool shuffle (a run this uniform would be a real
	// bug, not bad luck: seed 7 is fixed and asserted deterministic above).
	firstToolCounts := map[string]int{}
	for _, rep := range log.Repetitions {
		firstToolCounts[rep.ToolOrder[0]]++
	}
	if len(firstToolCounts) != 2 {
		t.Fatalf("expected both tools to lead at least one repetition out of 6, got lead counts: %+v", firstToolCounts)
	}
}

func TestRunCompetitorInterleaved_SampleCountsMatchRuns(t *testing.T) {
	site := w11SinglePage()
	tools := []Tool{fakeTool("a"), fakeTool("b")}
	ctx := context.Background()

	const warmups, runs = 2, 5
	stats, log := RunCompetitorInterleaved(ctx, site, 5, warmups, runs, tools, 99)

	for _, name := range []string{"a", "b"} {
		cs := stats[name]
		if !cs.Available {
			t.Fatalf("tool %s: expected Available (binary is \"true\")", name)
		}
		if len(cs.Samples) != runs {
			t.Errorf("tool %s: got %d samples, want %d measured runs", name, len(cs.Samples), runs)
		}
		if cs.Runs != runs {
			t.Errorf("tool %s: cs.Runs = %d, want %d", name, cs.Runs, runs)
		}
		if cs.PassCount != runs {
			t.Errorf("tool %s: PassCount = %d, want %d (fakeTool always reports the seed URL found)", name, cs.PassCount, runs)
		}
	}
	if got := len(log.Repetitions); got != warmups+runs {
		t.Errorf("log has %d repetitions, want %d (warmups+runs)", got, warmups+runs)
	}
	measured := 0
	for _, rep := range log.Repetitions {
		if rep.Measured {
			measured++
		}
	}
	if measured != runs {
		t.Errorf("log recorded %d measured repetitions, want %d", measured, runs)
	}
}

func TestRunCompetitorInterleaved_UnavailableToolStaysInShuffleButUncounted(t *testing.T) {
	site := w11SinglePage()
	tools := []Tool{fakeTool("a"), {Name: "missing", Binary: "definitely-not-a-real-binary-xyz"}}
	ctx := context.Background()

	stats, log := RunCompetitorInterleaved(ctx, site, 5, 0, 3, tools, 5)

	if stats["missing"].Available {
		t.Error("expected missing tool to be marked unavailable")
	}
	if len(stats["missing"].Samples) != 0 {
		t.Errorf("expected 0 samples for an unavailable tool, got %d", len(stats["missing"].Samples))
	}
	if len(stats["a"].Samples) != 3 {
		t.Errorf("expected the available tool's own run count unaffected by the other tool's absence, got %d", len(stats["a"].Samples))
	}
	// The unavailable tool must still appear in the recorded order — its
	// absence from results shouldn't also erase it from the audit log.
	for _, rep := range log.Repetitions {
		found := false
		for _, name := range rep.ToolOrder {
			if name == "missing" {
				found = true
			}
		}
		if !found {
			t.Fatalf("repetition %+v missing the unavailable tool from ToolOrder", rep)
		}
	}
}
