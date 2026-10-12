// Package cli is the command-dispatch boilerplate shared by the indexer,
// eval and tools binaries: a command table, usage text and flag-set creation, so a new
// subcommand is one table entry.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// Command is one subcommand.
type Command struct {
	Name  string
	Short string
	Run   func(ctx context.Context, args []string) error
}

// App is a binary's command table.
type App struct {
	// Name is the binary name used in usage and error text.
	Name     string
	Commands []Command
	// Bare lists errors printed as their bare message, with no prefix.
	Bare []error
	// Stderr receives usage and errors; os.Stderr when nil.
	Stderr io.Writer
}

// Run dispatches args to a subcommand and returns the process exit code: 0 on
// success, 2 for usage, help and unknown commands, 1 for a command error.
func (a App) Run(args []string) int {
	errOut := a.Stderr
	if errOut == nil {
		errOut = os.Stderr
	}
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		a.usage(errOut)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	name := args[0]
	for _, c := range a.Commands {
		if c.Name != name {
			continue
		}
		err := c.Run(ctx, args[1:])
		switch {
		case err == nil:
			return 0
		case errors.Is(err, flag.ErrHelp):
			return 2
		case a.isBare(err):
			fmt.Fprintln(errOut, err.Error())
			return 1
		default:
			fmt.Fprintf(errOut, "%s %s: %v\n", a.Name, name, err)
			return 1
		}
	}
	fmt.Fprintf(errOut, "%s: unknown command %q\n\n", a.Name, name)
	a.usage(errOut)
	return 2
}

func (a App) isBare(err error) bool {
	for _, b := range a.Bare {
		if errors.Is(err, b) {
			return true
		}
	}
	return false
}

func (a App) usage(w io.Writer) {
	fmt.Fprintf(w, "Usage: %s <command> [flags]\n\nCommands:\n", a.Name)
	for _, c := range a.Commands {
		fmt.Fprintf(w, "  %-12s %s\n", c.Name, c.Short)
	}
}

// NewFlagSet returns the flag set every subcommand of program uses, so help
// text and error handling stay the same everywhere.
func NewFlagSet(program, command string) *flag.FlagSet {
	return flag.NewFlagSet(program+" "+command, flag.ContinueOnError)
}
