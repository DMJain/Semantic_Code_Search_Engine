// Command callgraph-gt builds a ground-truth call graph for the Kubernetes
// checkout with golang.org/x/tools/go/packages and measures the accuracy of the
// tree-sitter call graph (edges.source = 'gopackages').
//
// Two modes exist. --check proves the files the walk selected for the frozen
// slice roots are exactly the files go/packages loads under the identical
// build context (D-067). --measure is the read-only memory measurement (Q-04):
// it loads the slice, builds SSA and the VTA call graph over the slice
// functions only and writes result metadata; it never touches the database.
//
// The workspace-mode fallback is kept on purpose (D-071, Q-05): the pinned tag
// has no vendored client-go, so the GOWORK=off -mod=vendor load fails and the
// workspace (GOWORK=auto) load is what actually loads the slice.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run dispatches the flags and returns the process exit code. --check uses 0
// (equal), 1 (unequal) and 2 (cannot check: load or database failure).
// --measure uses 0 (measured and written) and 2 (load or measurement failure).
func run(args []string) int {
	fs := flag.NewFlagSet("callgraph-gt", flag.ContinueOnError)
	check := fs.Bool("check", false, "check that the walk and go/packages file sets are equal (falls back to the workspace load because the pinned tag has no vendored client-go; Q-05)")
	measure := fs.Bool("measure", false, "read-only run: load the slice, build SSA and the VTA call graph over the slice functions only, and write result metadata (Q-04)")
	out := fs.String("out", defaultMeasureOut, "path for --measure result metadata (ignored by --check)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch {
	case *measure:
		return runMeasure(*out)
	case *check:
		return runCheck()
	default:
		fmt.Fprintln(os.Stderr, "callgraph-gt: pass --check or --measure")
		return 2
	}
}
