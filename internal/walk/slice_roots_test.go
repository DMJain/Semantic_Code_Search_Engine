package walk_test

import (
	"slices"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

// TestSliceRootsAreTheUndroppedPrefixes: the roots samples are stratified by
// come from the slice definition, in its order, without the dropped root, so
// the frozen slice has three roots today.
func TestSliceRootsAreTheUndroppedPrefixes(t *testing.T) {
	want := []string{"pkg/scheduler", "pkg/kubelet", "pkg/controller"}
	if got := walk.SliceRoots(); !slices.Equal(got, want) {
		t.Errorf("SliceRoots() = %v, want %v", got, want)
	}
}

// TestSliceRootOf: a path maps to the slice root that contains it, and to ""
// when it is outside the slice, including under the dropped root.
func TestSliceRootOf(t *testing.T) {
	cases := map[string]string{
		"pkg/kubelet/cm/a.go":                           "pkg/kubelet",
		"pkg/scheduler/x.go":                            "pkg/scheduler",
		"pkg/controller/deployment/d.go":                "pkg/controller",
		"staging/src/k8s.io/client-go/tools/cache/c.go": "",
		"pkg/schedulerX/x.go":                           "",
		"cmd/kubelet/main.go":                           "",
	}
	for path, want := range cases {
		if got := walk.SliceRootOf(path); got != want {
			t.Errorf("SliceRootOf(%q) = %q, want %q", path, got, want)
		}
	}
}
