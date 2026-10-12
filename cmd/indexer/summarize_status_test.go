package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/summarize"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
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
	for _, want := range []string{"rate 360.0 fn/h (last 100 timestamped completions)", "eta 1m40s"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("output %q missing %q", b.String(), want)
		}
	}
}

// TestPrintStatusNothingPending pins the output when the queue is empty.
func TestPrintStatusNothingPending(t *testing.T) {
	var b bytes.Buffer
	printStatus(&b, summarize.StatusReport{
		PromptVersion: "fn-v1",
		Counts:        map[string]int{"done": 3},
	})
	if !strings.Contains(b.String(), "eta 0s (nothing pending)") {
		t.Errorf("output = %q, want the nothing-pending line", b.String())
	}
}

// TestStatusToPrintStatusEndToEnd runs Status against seeded rows and prints
// the report, so the query and the output text are checked together: 20
// eligible functions, 12 done ten seconds apart (360 per hour), 8 remaining
// and an ETA of 80s.
func TestStatusToPrintStatusEndToEnd(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var fileID int64
	if err := pool.QueryRow(ctx, `INSERT INTO files (path, package, sha, loc, in_slice)
		VALUES ('pkg/e2e.go', 'pkg', 'sha', 100, true) RETURNING id`).Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		var symID int64
		if err := pool.QueryRow(ctx, `INSERT INTO symbols (file_id, kind, name, qualified_name, signature, start_line, end_line, raw_calls)
			VALUES ($1, 'func', $2, $2, 'func()', 1, 10, '{}') RETURNING id`, fileID, "F"+string(rune('a'+i))).Scan(&symID); err != nil {
			t.Fatal(err)
		}
		if i < 12 {
			if _, err := pool.Exec(ctx, `INSERT INTO summaries (level, target_id, text, model, prompt_version, input_hash, status, completed_at)
				VALUES ('function', $1, 't', 'm', $2, 'h', 'done', $3)`,
				symID, summarize.PromptVersion, base.Add(time.Duration(i)*10*time.Second)); err != nil {
				t.Fatal(err)
			}
		}
	}
	rep, err := (&summarize.Runner{Pool: pool}).Status(ctx, false)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var b bytes.Buffer
	printStatus(&b, rep)
	for _, want := range []string{"done 12", "remaining 8", "rate 360.0 fn/h (last 100 timestamped completions)", "eta 1m20s"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("output %q missing %q", b.String(), want)
		}
	}
}
