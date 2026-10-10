// Command eval runs evaluation tooling. The first subcommand, mine-yield,
// probes GitHub for real merged pull requests that can be mined into semantic
// evaluation queries for the frozen candidate slice (S1-T12).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

type command struct {
	name  string
	short string
	run   func(ctx context.Context, args []string) error
}

var commands = []command{
	{"mine-yield", "probe GitHub for minable merged PRs in the candidate slice", runMineYield},
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	os.Exit(run(os.Args[1:]))
}

// run dispatches to a subcommand and returns the process exit code.
func run(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage()
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	name := args[0]
	for _, c := range commands {
		if c.name != name {
			continue
		}
		err := c.run(ctx, args[1:])
		switch {
		case err == nil:
			return 0
		case errors.Is(err, flag.ErrHelp):
			return 2
		case errors.Is(err, errNoToken):
			// The exact message the ticket requires, with no prefix.
			fmt.Fprintln(os.Stderr, errNoToken.Error())
			return 1
		default:
			fmt.Fprintf(os.Stderr, "eval %s: %v\n", name, err)
			return 1
		}
	}
	fmt.Fprintf(os.Stderr, "eval: unknown command %q\n\n", name)
	usage()
	return 2
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: eval <command> [flags]\n\nCommands:")
	for _, c := range commands {
		fmt.Fprintf(os.Stderr, "  %-12s %s\n", c.name, c.short)
	}
}

// newFlagSet returns the flag set every subcommand uses, so help text and
// error handling stay consistent.
func newFlagSet(name string) *flag.FlagSet {
	return flag.NewFlagSet("eval "+name, flag.ContinueOnError)
}
