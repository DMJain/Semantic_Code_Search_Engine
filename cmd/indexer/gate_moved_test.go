package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/cli"
)

// The one-off quality-gate helper moved to cmd/tools (D-071); the production
// indexer must not register it.
func TestIndexerDoesNotRegisterGate(t *testing.T) {
	for _, c := range commands {
		if c.Name == "gate" {
			t.Fatal("indexer still registers the gate command")
		}
	}
	var errOut bytes.Buffer
	code := cli.App{Name: "indexer", Commands: commands, Stderr: &errOut}.Run([]string{"gate"})
	if code != 2 || !strings.Contains(errOut.String(), "unknown command") {
		t.Fatalf("indexer gate: exit %d, stderr %q; want 2 and unknown command", code, errOut.String())
	}
}
