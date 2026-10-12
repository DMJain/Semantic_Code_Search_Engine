// Package walk walks the pinned Kubernetes corpus and defines the slice: the
// subset of the repo that gets vectors and summaries and is the eval scope
// (architecture.md#corpus-and-slice, D-005). The walk stage and callgraph-gt
// both consume these definitions, and the slice is frozen (D-070).
package walk
