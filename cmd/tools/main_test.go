package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/cli"
)

// The one-off quality-gate helper lives in the tools binary, not in the
// production indexer (D-071).
func TestToolsRegistersGate(t *testing.T) {
	var errOut bytes.Buffer
	code := cli.App{Name: "tools", Commands: commands, Stderr: &errOut}.Run([]string{"help"})
	if code != 2 {
		t.Fatalf("help exit = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "gate") {
		t.Fatalf("tools usage does not list gate:\n%s", errOut.String())
	}
}

func TestToolsGateHelp(t *testing.T) {
	if code := run([]string{"gate", "-h"}); code != 2 {
		t.Fatalf("gate -h exit = %d, want 2", code)
	}
}
