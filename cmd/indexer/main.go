// Command indexer builds the offline search index over a Kubernetes checkout.
//
// Usage:
//
//	indexer <command> [flags]
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
	{"migrate", "apply database migrations", runMigrate},
	{"doctor", "check Postgres, extensions, Ollama and models", runDoctor},
	{"fetch-repo", "shallow-clone Kubernetes at KUBE_TAG into KUBE_DIR", runFetchRepo},
	{"walk", "walk the corpus and fill the files table", runWalk},
	{"parse", "parse Go files into function and method symbols", runParse},
	{"audit", "check a seeded sample of stored symbols against the source", runAudit},
	{"chunk", "split symbols into chunks (not implemented)", stub("chunk")},
	{"summarize", "summarize functions, files and packages (not implemented)", stub("summarize")},
	{"embed", "embed chunks and summaries (not implemented)", stub("embed")},
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
		default:
			fmt.Fprintf(os.Stderr, "indexer %s: %v\n", name, err)
			return 1
		}
	}
	fmt.Fprintf(os.Stderr, "indexer: unknown command %q\n\n", name)
	usage()
	return 2
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: indexer <command> [flags]\n\nCommands:")
	for _, c := range commands {
		fmt.Fprintf(os.Stderr, "  %-12s %s\n", c.name, c.short)
	}
}

// parseFlags parses a subcommand's flags with the shared flag set, so -h
// works consistently everywhere.
func parseFlags(name string, args []string) error {
	return newFlagSet(name).Parse(args)
}

// newFlagSet returns the flag set every subcommand uses, so help text and
// error handling stay the same everywhere.
func newFlagSet(name string) *flag.FlagSet {
	return flag.NewFlagSet("indexer "+name, flag.ContinueOnError)
}

func stub(name string) func(context.Context, []string) error {
	return func(_ context.Context, args []string) error {
		if err := parseFlags(name, args); err != nil {
			return err
		}
		return errors.New("not implemented yet")
	}
}
