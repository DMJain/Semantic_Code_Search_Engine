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
	rep, err := r.Status(ctx, time.Millisecond, false)
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
	// Three eligible symbols, one done and one failed: one still remains.
	if rep.Remaining != 1 {
		t.Errorf("remaining = %d, want 1", rep.Remaining)
	}
}

// TestStatusReportETA pins the ETA arithmetic against the real run: eligible
// 6673, done 379 leaves 6294 remaining; a rate of 600 fn/h makes that ~10.5h.
func TestStatusReportETA(t *testing.T) {
	before := map[string]int{"done": 378}
	after := map[string]int{"done": 379}
	rep := statusReport(6673, before, after, false, 6*time.Second)
	if !rep.Measured {
		t.Fatal("report not measured despite progress")
	}
	if rep.PerHour != 600 {
		t.Errorf("per hour = %v, want 600", rep.PerHour)
	}
	if rep.Remaining != 6294 {
		t.Errorf("remaining = %d, want 6294", rep.Remaining)
	}
	if want := 6294 * 6 * time.Second; rep.ETA != want {
		t.Errorf("eta = %s, want %s", rep.ETA, want)
	}
}

// TestStatusReportFailedRemaining proves failed rows are excluded from
// remaining (they are terminal) unless a retry run will re-pick them.
func TestStatusReportFailedRemaining(t *testing.T) {
	counts := map[string]int{"done": 100, "failed": 5, "pending": 1}
	if got := statusReport(200, counts, counts, false, time.Second).Remaining; got != 95 {
		t.Errorf("remaining without retry = %d, want 95", got)
	}
	if got := statusReport(200, counts, counts, true, time.Second).Remaining; got != 100 {
		t.Errorf("remaining with retry = %d, want 100", got)
	}
}

// TestStatusReportNoProgress proves a window without completions (or with a
// shrinking done count) reports an unknown ETA rather than dividing by zero.
func TestStatusReportNoProgress(t *testing.T) {
	for _, before := range []map[string]int{
		{"done": 100},
		{"done": 101},
	} {
		rep := statusReport(
			105,
			before,
			map[string]int{"done": 100, "pending": 5},
			false,
			time.Second)
		if rep.Measured {
			t.Fatal("report measured without progress")
		}
		if rep.ETA != 0 {
			t.Errorf("eta = %s, want 0", rep.ETA)
		}
		if rep.Remaining != 5 {
			t.Errorf("remaining = %d, want 5", rep.Remaining)
		}
	}
}
