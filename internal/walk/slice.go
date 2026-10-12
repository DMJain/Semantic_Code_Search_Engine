package walk

import "strings"

// SlicePrefixes is the ordered list of repo-root-relative path prefixes that
// make up the slice. Order is significant: the stratification of sampling
// follows it. Frozen (D-070).
var SlicePrefixes = []string{
	"pkg/scheduler",
	"pkg/kubelet",
	"pkg/controller",
	"staging/src/k8s.io/client-go",
}

// SliceDropPrefixes is the FROZEN ordered list of slice subpath prefixes
// removed from the slice, in the D-005 shrink order (D-070). The slice
// never grows; a re-freeze needs an owner-authorized superseding decision.
var SliceDropPrefixes = []string{"staging/src/k8s.io/client-go"}

// InSlice reports whether a repo-root-relative path (slash separators) is
// inside the slice: it starts with one of SlicePrefixes and none of
// SliceDropPrefixes. Matching is on path-segment boundaries, so
// "pkg/schedulerX" is not in the slice.
func InSlice(path string) bool {
	return hasAnyPrefix(path, SlicePrefixes) && !hasAnyPrefix(path, SliceDropPrefixes)
}

func hasAnyPrefix(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// SliceRoots returns the slice's roots: SlicePrefixes in order, without any
// prefix the drop list removes whole. Samples are stratified by these roots.
func SliceRoots() []string {
	var roots []string
	for _, p := range SlicePrefixes {
		if InSlice(p) {
			roots = append(roots, p)
		}
	}
	return roots
}

// SliceRootOf returns the slice root path belongs to, or "" when the path is
// outside the slice.
func SliceRootOf(path string) string {
	if !InSlice(path) {
		return ""
	}
	for _, p := range SlicePrefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return p
		}
	}
	return ""
}
