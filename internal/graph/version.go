// Package graph re-parses Go files with tree-sitter and resolves call
// expressions to edges. It is the S2 call-graph extraction and resolution
// layer (D-019, D-073, D-076): extraction is per file, resolution is Rule 1
// (same-package function by name) plus honest deferral of what later tickets
// handle.
package graph

// ResolverVersion identifies the extraction and resolution output. Bump it on
// any change that can change the edges (a new rule, a changed classification),
// because it is part of the freshness fingerprint (D-076): old edges are then
// stale instead of silently reused.
const ResolverVersion = 1
