// Command callgraph-gt builds a ground-truth call graph for the Kubernetes
// checkout with golang.org/x/tools/go/packages and measures the accuracy of the
// tree-sitter call graph (edges.source = 'gopackages').
//
// Only the --check mode exists: it proves the files the walk selected
// for the frozen slice roots are exactly the files go/packages loads under the
// identical build context (D-067). Building the VTA graph itself is S2.
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
func run(args []string) int {
	fs := flag.NewFlagSet("callgraph-gt", flag.ContinueOnError)
	check := fs.Bool("check", false, "check that the walk and go/packages file sets are equal (falls back to the workspace load because the pinned tag has no vendored client-go; Q-05)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*check {
		fmt.Fprintln(os.Stderr, "callgraph-gt: pass --check (building the VTA graph is S2)")
		return 2
	}
	return runCheck()
}
