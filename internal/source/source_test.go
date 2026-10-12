package source_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/source"
)

func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "a.go"), []byte("l1\nl2\nl3\nl4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestReadRangeIsOneBasedInclusive(t *testing.T) {
	got, err := source.ReadRange(tree(t), "pkg/a.go", 2, 3)
	if err != nil || got != "l2\nl3" {
		t.Errorf("ReadRange(2,3) = %q, %v; want %q", got, err, "l2\nl3")
	}
}

func TestReadRangeClamps(t *testing.T) {
	root := tree(t)
	if got, _ := source.ReadRange(root, "pkg/a.go", 0, 99); got != "l1\nl2\nl3\nl4\n" {
		t.Errorf("clamped = %q", got)
	}
	if got, _ := source.ReadRange(root, "pkg/a.go", 4, 2); got != "" {
		t.Errorf("empty range = %q, want empty", got)
	}
}

func TestReadRangeMissingFileNamesThePath(t *testing.T) {
	_, err := source.ReadRange(t.TempDir(), "pkg/none.go", 1, 2)
	if err == nil || !contains(err.Error(), "pkg/none.go") {
		t.Errorf("err = %v, want it to name the path", err)
	}
}

func contains(s, sub string) bool { return len(sub) == 0 || (len(s) >= len(sub) && index(s, sub) >= 0) }
func index(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
