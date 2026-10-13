// Package graph re-parses Go files with tree-sitter and resolves call
// expressions to edges. It is the S2 call-graph extraction and resolution
// layer (D-019, D-073, D-076): extraction is per file, and resolution is Rule 1
// (same-package function by name), Rule 2 (alias-qualified `alias.Name` through
// the import map: a function in the aliased package is exact, an unindexed
// import path is external, and an indexed package without the function is a
// conversion or no_match), Rule 3 (a selector on a receiver, parameter or
// inferable local whose type is a known named type resolves to its value or
// pointer method, exact concrete_method; the type environment also covers
// `var x T`, composite literals, same-package single-result constructors and
// same-package struct field chains; a known interface type is interface_method,
// counted deferred_interface until Rule 4), and Rule 5 (a selector whose
// receiver is still unknown is looked up among all methods of that name in the
// caller's package: one is name_only, several are ambiguous, none is unresolved
// with reason unknown_receiver), plus honest deferral of what later tickets
// handle.
package graph

// ResolverVersion identifies the extraction and resolution output. Bump it on
// any change that can change the edges (a new rule, a changed classification),
// because it is part of the freshness fingerprint (D-076): old edges are then
// stale instead of silently reused.
const ResolverVersion = 4
