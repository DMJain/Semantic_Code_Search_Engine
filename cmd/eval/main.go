// Command eval runs evaluation tooling. The first subcommand, mine-yield,
// probes GitHub for real merged pull requests that can be mined into semantic
// evaluation queries for the frozen candidate slice (S1-T12).
package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/cli"
)

var commands = []cli.Command{
	{Name: "mine-yield", Short: "probe GitHub for minable merged PRs in the candidate slice", Run: runMineYield},
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	os.Exit(run(os.Args[1:]))
}

// run dispatches to a subcommand and returns the process exit code. The
// no-token error prints as its bare message, the exact text the ticket requires.
func run(args []string) int {
	return cli.App{Name: "eval", Commands: commands, Bare: []error{errNoToken}}.Run(args)
}

// newFlagSet returns the flag set every subcommand uses, so help text and
// error handling stay consistent.
func newFlagSet(name string) *flag.FlagSet {
	return cli.NewFlagSet("eval", name)
}
