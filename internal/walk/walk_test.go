package walk_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

// TestWalkGoldenFixture asserts the exact kept-file list and the per-rule
// excluded counts over the committed fixture tree. The tree mirrors the
// sprint's negatives: a _windows.go file, a //go:build windows file and a file
// that imports "C".
func TestWalkGoldenFixture(t *testing.T) {
	files, stats, err := walk.Walk("testdata/module")
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}

	wantKept := []string{
		"pkg/api/types.go",
		"pkg/scheduler/scheduler.go",
		"staging/src/k8s.io/client-go/tools/cache/store.go",
	}
	gotKept := make([]string, len(files))
	for i, f := range files {
		gotKept[i] = f.Path
	}
	if !slices.Equal(gotKept, wantKept) {
		t.Errorf("kept files = %v, want %v", gotKept, wantKept)
	}

	wantExcluded := map[string]int{
		"vendor":              1,
		"zz_generated":        1,
		"pb_go":               1,
		"testdata":            1,
		"test_go":             1,
		"client_go_generated": 2,
		"build_constraint":    3,
	}
	if !maps.Equal(stats.Excluded, wantExcluded) {
		t.Errorf("excluded counts = %v, want %v", stats.Excluded, wantExcluded)
	}

	if stats.Indexed != 3 {
		t.Errorf("Indexed = %d, want 3", stats.Indexed)
	}
	if stats.InSlice != 2 {
		t.Errorf("InSlice = %d, want 2", stats.InSlice)
	}

	for _, f := range files {
		if f.SHA == "" {
			t.Errorf("file %s has empty sha", f.Path)
		}
		if f.LOC <= 0 {
			t.Errorf("file %s has loc %d, want > 0", f.Path, f.LOC)
		}
	}
}
