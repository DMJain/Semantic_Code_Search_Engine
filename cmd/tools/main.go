// Command tools holds one-off author tooling that must not ship in the
// production indexer binary (D-071). Its first command, gate,
// reproduces the fn-v1 quality-gate sample and writes the grading sheet.
//
// Usage:
//
//	tools <command> [flags]
package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/cli"
)

var commands = []cli.Command{
	{Name: "gate", Short: "print the quality-gate sample with code and write the grade sheet", Run: runGate},
	{Name: "nomic-fit", Short: "measure chunk and header documents against a registry model's real context (default nomic-embed-text)", Run: runNomicFit},
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	os.Exit(run(os.Args[1:]))
}

// run dispatches to a subcommand and returns the process exit code.
func run(args []string) int {
	return cli.App{Name: "tools", Commands: commands}.Run(args)
}

// newFlagSet returns the flag set every subcommand uses, so help text and
// error handling stay consistent.
func newFlagSet(name string) *flag.FlagSet {
	return cli.NewFlagSet("tools", name)
}
