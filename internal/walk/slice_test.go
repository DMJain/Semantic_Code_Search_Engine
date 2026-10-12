package walk_test

import (
	"slices"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

// TestSlicePrefixesOrdered pins the four slice prefixes from
// architecture.md#corpus-and-slice in order.
func TestSlicePrefixesOrdered(t *testing.T) {
	want := []string{
		"pkg/scheduler",
		"pkg/kubelet",
		"pkg/controller",
		"staging/src/k8s.io/client-go",
	}
	if !slices.Equal(walk.SlicePrefixes, want) {
		t.Errorf("SlicePrefixes = %v, want %v", walk.SlicePrefixes, want)
	}
}

// TestSliceDropPrefixesFrozen pins the frozen D-005 drop list (D-070).
func TestSliceDropPrefixesFrozen(t *testing.T) {
	want := []string{"staging/src/k8s.io/client-go"}
	if !slices.Equal(walk.SliceDropPrefixes, want) {
		t.Errorf("SliceDropPrefixes = %v, want %v", walk.SliceDropPrefixes, want)
	}
}

// TestInSlice checks the membership helper on segment boundaries and the
// frozen drop list.
func TestInSlice(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"pkg/scheduler/scheduler.go", true},
		{"pkg/scheduler", true},
		{"pkg/kubelet/kubelet.go", true},
		{"pkg/controller/deployment/deployment.go", true},
		{"staging/src/k8s.io/client-go/tools/cache/store.go", false},
		{"staging/src/k8s.io/client-go", false},
		{"staging/src/k8s.io/client-goX/x.go", false},
		{"pkg/schedulerX/x.go", false},
		{"pkg/api/types.go", false},
		{"vendor/k8s.io/client-go/x.go", false},
	}
	for _, c := range cases {
		if got := walk.InSlice(c.path); got != c.want {
			t.Errorf("InSlice(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
