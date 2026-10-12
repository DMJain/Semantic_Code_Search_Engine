package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/summarize"
)

// TestPrintStatusETAUnknownShowsReason proves the status output says
// "ETA unknown" with the reason when no rate could be computed.
func TestPrintStatusETAUnknownShowsReason(t *testing.T) {
	var b bytes.Buffer
	printStatus(&b, summarize.StatusReport{
		PromptVersion: "fn-v1",
		Counts:        map[string]int{"done": 3},
		Remaining:     10,
		Reason:        "only 1 timestamped completion(s)",
	})
	if !strings.Contains(b.String(), "ETA unknown: only 1 timestamped completion(s)") {
		t.Errorf("output = %q, want ETA unknown with reason", b.String())
	}
}

// TestPrintStatusShowsETA proves a measured rate prints the rate and ETA.
func TestPrintStatusShowsETA(t *testing.T) {
	var b bytes.Buffer
	printStatus(&b, summarize.StatusReport{
		PromptVersion: "fn-v1",
		Counts:        map[string]int{"done": 3},
		Remaining:     10,
		Measured:      true,
		PerHour:       360,
		ETA:           100 * time.Second,
	})
	for _, want := range []string{"rate 360.0 fn/h", "eta 1m40s"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("output %q missing %q", b.String(), want)
		}
	}
}
