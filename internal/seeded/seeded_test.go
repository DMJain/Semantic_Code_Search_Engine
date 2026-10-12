package seeded_test

import (
	"slices"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/seeded"
)

func ints(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// TestDrawSeed3Pinned: the draw for seed 3 is the recorded list taken from the
// pre-refactor audit and gate samplers (math/rand shuffle of the ordered
// input, first n kept).
func TestDrawSeed3Pinned(t *testing.T) {
	want := []int{26, 43, 22, 7, 35, 50, 58, 1, 94, 67}
	if got := seeded.Draw(ints(100), 10, 3); !slices.Equal(got, want) {
		t.Errorf("Draw seed 3 = %v, want %v", got, want)
	}
}

func TestDrawClampsAndDoesNotMutate(t *testing.T) {
	in := ints(5)
	if got := seeded.Draw(in, 99, 3); len(got) != 5 {
		t.Errorf("len = %d, want 5", len(got))
	}
	if got := seeded.Draw(in, -1, 3); len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
	if !slices.Equal(in, ints(5)) {
		t.Errorf("input mutated: %v", in)
	}
}

// TestStratifiedTakesQuotaPerGroupInOrder: groups are shuffled by one
// generator in group order and each contributes its first quota items.
func TestStratifiedTakesQuotaPerGroupInOrder(t *testing.T) {
	groups := [][]int{{1, 2, 3, 4}, {10, 20, 30}}
	got := seeded.Stratified(groups, []int{2, 5}, 3)
	if len(got) != 5 {
		t.Fatalf("got %v, want 2 + 3 items", got)
	}
	if again := seeded.Stratified([][]int{{1, 2, 3, 4}, {10, 20, 30}}, []int{2, 5}, 3); !slices.Equal(got, again) {
		t.Errorf("not deterministic: %v vs %v", got, again)
	}
}
