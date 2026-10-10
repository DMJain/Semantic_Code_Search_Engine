// Package walk walks the pinned Kubernetes corpus and defines the slice: the
// subset of the repo that gets vectors and summaries and is the eval scope
// (architecture.md#corpus-and-slice, D-005). The walk stage (S1-T03) and
// callgraph-gt (S1-T08) both consume these definitions, and the slice is frozen
// once at S1-T13.
package walk

import "strings"

// SlicePrefixes is the ordered list of repo-root-relative path prefixes that
// make up the slice. Order is significant: the stratification of sampling
// follows it. Frozen at S1-T13.
var SlicePrefixes = []string{
	"pkg/scheduler",
	"pkg/kubelet",
	"pkg/controller",
	"staging/src/k8s.io/client-go",
}

// SliceDropPrefixes is the FROZEN ordered list of slice subpath prefixes
// removed from the slice, in the D-005 shrink order (S1-T13, D-070). The slice
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
