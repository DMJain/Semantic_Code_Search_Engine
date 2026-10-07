// Command callgraph-gt will build a ground-truth call graph for the
// Kubernetes checkout with golang.org/x/tools/go/packages, to measure the
// accuracy of the tree-sitter call graph (edges.source = 'gopackages').
//
// Not implemented yet.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "callgraph-gt: not implemented yet")
	os.Exit(1)
}
