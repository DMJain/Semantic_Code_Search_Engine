// Command eval will run the queries in eval/queries.dev.jsonl against the query
// service and write scored results to eval/results/.
//
// Not implemented yet.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "eval: not implemented yet")
	os.Exit(1)
}
