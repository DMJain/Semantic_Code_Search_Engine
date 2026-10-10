package summarize

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
)

// symbolIDs returns every symbol id in insert order.
func symbolIDs(t *testing.T, pool *pgxpool.Pool) []int64 {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT id FROM symbols ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

// insertSummary inserts one summary row for a prompt version and status.
func insertSummary(t *testing.T, pool *pgxpool.Pool, level string, id int64, version, status string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO summaries (level, target_id, text, model, prompt_version, input_hash, status)
		VALUES ($1, $2, NULL, 'test-model', $3, 'h', $4)`,
		level, id, version, status); err != nil {
		t.Fatal(err)
	}
}

// TestStatusCountsCurrentVersionOnly proves --status counts only the current
// prompt version (D-068): rows of another version, and rows of another level,
// are not counted.
func TestStatusCountsCurrentVersionOnly(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dir := t.TempDir()
	source, spans := funcFile("A", "B", "C")
	seedSource(t, pool, dir, "pkg/scheduler/status.go", source, spans)

	ids := symbolIDs(t, pool)
	if len(ids) != 3 {
		t.Fatalf("seeded %d symbols, want 3", len(ids))
	}
	// Current version: one done, one pending, one failed.
	insertSummary(t, pool, "function", ids[0], PromptVersion, "done")
	insertSummary(t, pool, "function", ids[1], PromptVersion, "pending")
	insertSummary(t, pool, "function", ids[2], PromptVersion, "failed")
	// Another prompt version: three done rows that must not be counted.
	for _, id := range ids {
		insertSummary(t, pool, "function", id, "fn-v0", "done")
	}
	// Another level for the current version: must not be counted either.
	insertSummary(t, pool, "file", ids[0], PromptVersion, "done")

	r := newRunner(pool, "http://127.0.0.1:0", dir, 4096, io.Discard)
	rep, err := r.Status(ctx, time.Millisecond)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if rep.PromptVersion != PromptVersion {
		t.Fatalf("prompt version = %q, want %q", rep.PromptVersion, PromptVersion)
	}
	want := map[string]int{"done": 1, "pending": 1, "failed": 1}
	for status, n := range want {
		if rep.Counts[status] != n {
			t.Errorf("count[%s] = %d, want %d (all counts: %v)", status, rep.Counts[status], n, rep.Counts)
		}
	}
	if rep.Remaining != 1 {
		t.Errorf("remaining = %d, want 1", rep.Remaining)
	}
}

// TestStatusReportETA pins the ETA arithmetic: a 10-row increase over 5s is
// 2 rows/s; 200 pending rows then take 100s.
func TestStatusReportETA(t *testing.T) {
	before := map[string]int{"done": 100, "pending": 0}
	after := map[string]int{"done": 110, "pending": 200}
	rep := statusReport(before, after, 5*time.Second)
	if !rep.Measured {
		t.Fatal("report not measured despite progress")
	}
	if rep.PerHour != 7200 {
		t.Errorf("per hour = %v, want 7200", rep.PerHour)
	}
	if rep.ETA != 100*time.Second {
		t.Errorf("eta = %s, want 100s", rep.ETA)
	}
	if rep.Remaining != 200 {
		t.Errorf("remaining = %d, want 200", rep.Remaining)
	}
	if rep.Counts["done"] != 110 {
		t.Errorf("counts done = %d, want 110", rep.Counts["done"])
	}
}

// TestStatusReportNoProgress proves a window without completions reports an
// unknown ETA rather than dividing by zero.
func TestStatusReportNoProgress(t *testing.T) {
	rep := statusReport(
		map[string]int{"done": 100},
		map[string]int{"done": 100, "pending": 5},
		time.Second)
	if rep.Measured {
		t.Fatal("report measured with no completions")
	}
	if rep.ETA != 0 {
		t.Errorf("eta = %s, want 0", rep.ETA)
	}
	if rep.Remaining != 5 {
		t.Errorf("remaining = %d, want 5", rep.Remaining)
	}
}
