// Command callgraph-gt builds a ground-truth call graph for the Kubernetes
// checkout with golang.org/x/tools/go/packages and measures the accuracy of the
// tree-sitter call graph (edges.source = 'gopackages').
//
// The default action stores the ground truth: it loads the slice, builds SSA
// and the VTA call graph, maps each in-slice edge to symbols and replaces every
// source='gopackages' row in one transaction. --check proves the files the walk
// selected for the frozen slice roots are exactly the files go/packages loads
// under the identical build context (D-067). --measure is the read-only memory
// measurement (Q-04): it loads the slice, builds SSA and the VTA call graph
// over the slice functions only and writes result metadata; it never touches
// the database.
//
// The workspace-mode fallback is kept on purpose (D-071, Q-05): the pinned tag
// has no vendored client-go, so the GOWORK=off -mod=vendor load fails and the
// workspace (GOWORK=auto) load is what actually loads the slice.
package main

import (
	"flag"
	"os"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run dispatches the flags and returns the process exit code. The default
// (store) action and --measure use 0 (done) and 2 (load, mapping or database
// failure). --check uses 0 (equal), 1 (unequal) and 2 (cannot check).
func run(args []string) int {
	fs := flag.NewFlagSet("callgraph-gt", flag.ContinueOnError)
	check := fs.Bool("check", false, "check that the walk and go/packages file sets are equal (falls back to the workspace load because the pinned tag has no vendored client-go; Q-05)")
	measure := fs.Bool("measure", false, "read-only run: load the slice, build SSA and the VTA call graph over the slice functions only, and write result metadata (Q-04)")
	out := fs.String("out", defaultMeasureOut, "path for --measure result metadata (ignored by --check and the store)")
	storeOut := fs.String("store-out", config.DefaultGTStorePath, "path for the store action's GT partition result file (read and asserted by `eval callgraph`)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch {
	case *measure:
		return runMeasure(*out)
	case *check:
		return runCheck()
	default:
		return runStore(*storeOut)
	}
}
