package cli_test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/cli"
)

func app(run func(context.Context, []string) error) (cli.App, *bytes.Buffer) {
	var errOut bytes.Buffer
	return cli.App{
		Name:     "tool",
		Commands: []cli.Command{{Name: "go", Short: "does it", Run: run}},
		Stderr:   &errOut,
	}, &errOut
}

func TestRunDispatchesAndReturnsZero(t *testing.T) {
	var got []string
	a, _ := app(func(_ context.Context, args []string) error { got = args; return nil })
	if code := a.Run([]string{"go", "-x", "1"}); code != 0 || strings.Join(got, " ") != "-x 1" {
		t.Errorf("code=%d args=%v", code, got)
	}
}

func TestRunUsageAndUnknown(t *testing.T) {
	a, errOut := app(nil)
	if code := a.Run(nil); code != 2 || !strings.Contains(errOut.String(), "Usage: tool <command> [flags]") || !strings.Contains(errOut.String(), "does it") {
		t.Errorf("no args: code=%d out=%q", code, errOut.String())
	}
	errOut.Reset()
	if code := a.Run([]string{"nope"}); code != 2 || !strings.Contains(errOut.String(), `tool: unknown command "nope"`) {
		t.Errorf("unknown: code=%d out=%q", code, errOut.String())
	}
}

func TestRunErrorsAndHelp(t *testing.T) {
	a, errOut := app(func(context.Context, []string) error { return errors.New("boom") })
	if code := a.Run([]string{"go"}); code != 1 || !strings.Contains(errOut.String(), "tool go: boom") {
		t.Errorf("code=%d out=%q", code, errOut.String())
	}
	a, _ = app(func(context.Context, []string) error { return flag.ErrHelp })
	if code := a.Run([]string{"go"}); code != 2 {
		t.Errorf("help code = %d, want 2", code)
	}
}

func TestRunBareMessage(t *testing.T) {
	sentinel := errors.New("no token")
	a, errOut := app(func(context.Context, []string) error { return sentinel })
	a.Bare = []error{sentinel}
	if code := a.Run([]string{"go"}); code != 1 || errOut.String() != "no token\n" {
		t.Errorf("code=%d out=%q", code, errOut.String())
	}
}

func TestRunExitErrorCode(t *testing.T) {
	cases := []struct {
		code int
		msg  string
	}{
		{2, "have 4, want 5"},
		{3, "tune ef_search"},
	}
	for _, tc := range cases {
		a, errOut := app(func(context.Context, []string) error {
			return cli.Exit(tc.code, errors.New(tc.msg))
		})
		if code := a.Run([]string{"go"}); code != tc.code || errOut.String() != tc.msg+"\n" {
			t.Errorf("Exit(%d): code=%d out=%q", tc.code, code, errOut.String())
		}
	}
}

func TestNewFlagSetNamesTheCommand(t *testing.T) {
	if got := cli.NewFlagSet("tool", "go").Name(); got != "tool go" {
		t.Errorf("name = %q", got)
	}
}
