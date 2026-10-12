package summarize

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	// The failed row failed on unchanged input, so it is terminal (S1C-10).
	setCurrentHash(t, r, ids[2])
	rep, err := r.Status(ctx, false)
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

// TestRunSetsCompletedAtWhenDone proves a row gets a completion timestamp when
// it becomes done and none when it fails.
func TestRunSetsCompletedAtWhenDone(t *testing.T) {
	pool := testdb.New(t)
	dir := t.TempDir()
	source, spans := funcFile("A", "B")
	seedSource(t, pool, dir, "pkg/scheduler/ts.go", source, spans)

	fake := newFakeOllama(t, nil)
	r := newRunner(pool, fake.URL, dir, 4096, io.Discard)
	before := time.Now().Add(-time.Minute)
	if _, err := r.Run(context.Background(), RunOptions{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	var stamped int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM summaries
		WHERE status = 'done' AND completed_at BETWEEN $1 AND now() + interval '1 minute'`, before).Scan(&stamped); err != nil {
		t.Fatal(err)
	}
	if stamped != 2 {
		t.Errorf("done rows with completed_at = %d, want 2", stamped)
	}

	big, bigSpans := largeFile("Huge", 400)
	seedSource(t, pool, dir, "pkg/scheduler/big.go", big, bigSpans)
	small := newRunner(pool, fake.URL, dir, 64, io.Discard)
	if _, err := small.Run(context.Background(), RunOptions{}); err != nil {
		t.Fatalf("run big: %v", err)
	}
	var failedStamped int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM summaries WHERE status = 'failed' AND completed_at IS NOT NULL`).Scan(&failedStamped); err != nil {
		t.Fatal(err)
	}
	if failedStamped != 0 {
		t.Errorf("failed rows with completed_at = %d, want 0", failedStamped)
	}
}

// seedDoneAt inserts a done row for symbol id at the given completion time
// (nil = a row that was done before timestamps existed).
func seedDoneAt(t *testing.T, pool *pgxpool.Pool, id int64, version string, at *time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO summaries (level, target_id, text, model, prompt_version, input_hash, status, completed_at)
		VALUES ('function', $1, 't', 'test-model', $2, 'h', 'done', $3)`, id, version, at); err != nil {
		t.Fatal(err)
	}
}

func statusFixture(t *testing.T, funcs int) (*Runner, []int64) {
	t.Helper()
	pool := testdb.New(t)
	dir := t.TempDir()
	names := make([]string, funcs)
	for i := range names {
		names[i] = fmt.Sprintf("F%03d", i)
	}
	source, spans := funcFile(names...)
	seedSource(t, pool, dir, "pkg/scheduler/eta.go", source, spans)
	return newRunner(pool, "http://127.0.0.1:0", dir, 4096, io.Discard), symbolIDs(t, pool)
}

// TestStatusETAFollowsLast100Completions proves the rate comes from the most
// recent 100 timestamped completions, not older ones: 50 old completions an
// hour apart are followed by 100 ten seconds apart (99 intervals over 990s),
// so 10 remaining rows take 100s.
func TestStatusETAFollowsLast100Completions(t *testing.T) {
	r, ids := statusFixture(t, 160)
	base := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 50; i++ {
		at := base.Add(time.Duration(i) * time.Hour)
		seedDoneAt(t, r.Pool, ids[i], PromptVersion, &at)
	}
	recent := base.Add(100 * time.Hour)
	for i := 0; i < 100; i++ {
		at := recent.Add(time.Duration(i) * 10 * time.Second)
		seedDoneAt(t, r.Pool, ids[50+i], PromptVersion, &at)
	}

	start := time.Now()
	rep, err := r.Status(context.Background(), false)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("status took %s, want immediate", time.Since(start))
	}
	if !rep.Measured {
		t.Fatalf("not measured, reason %q", rep.Reason)
	}
	if rep.Remaining != 10 {
		t.Errorf("remaining = %d, want 10", rep.Remaining)
	}
	if rep.PerHour != 360 {
		t.Errorf("per hour = %v, want 360", rep.PerHour)
	}
	if rep.ETA != 100*time.Second {
		t.Errorf("eta = %s, want 1m40s", rep.ETA)
	}
}

// TestStatusETAUnknownBelowTwoTimestamps proves fewer than two timestamped
// completions gives no ETA and a reason (rows done before timestamps existed
// do not count).
func TestStatusETAUnknownBelowTwoTimestamps(t *testing.T) {
	r, ids := statusFixture(t, 5)
	at := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	seedDoneAt(t, r.Pool, ids[0], PromptVersion, nil)
	seedDoneAt(t, r.Pool, ids[1], PromptVersion, nil)
	seedDoneAt(t, r.Pool, ids[2], PromptVersion, &at)

	rep, err := r.Status(context.Background(), false)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if rep.Measured || rep.ETA != 0 {
		t.Errorf("measured = %v eta = %s, want unknown", rep.Measured, rep.ETA)
	}
	if !strings.Contains(rep.Reason, "timestamped") {
		t.Errorf("reason = %q, want it to mention timestamped completions", rep.Reason)
	}
	if rep.Remaining != 2 {
		t.Errorf("remaining = %d, want 2", rep.Remaining)
	}
}

// TestStatusETAIgnoresOtherPromptVersion proves timestamped rows of another
// prompt version never feed the rate.
func TestStatusETAIgnoresOtherPromptVersion(t *testing.T) {
	r, ids := statusFixture(t, 6)
	base := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		at := base.Add(time.Duration(i) * time.Second)
		seedDoneAt(t, r.Pool, ids[i], "fn-v0", &at)
	}
	only := base.Add(time.Hour)
	seedDoneAt(t, r.Pool, ids[3], PromptVersion, &only)

	rep, err := r.Status(context.Background(), false)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if rep.Measured {
		t.Errorf("measured from another version's rows: per hour %v", rep.PerHour)
	}
	if rep.Counts["done"] != 1 {
		t.Errorf("done = %d, want 1", rep.Counts["done"])
	}

	second := base.Add(time.Hour + 30*time.Second)
	seedDoneAt(t, r.Pool, ids[4], PromptVersion, &second)
	rep, err = r.Status(context.Background(), false)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !rep.Measured || rep.PerHour != 120 {
		t.Errorf("measured = %v per hour = %v, want true/120", rep.Measured, rep.PerHour)
	}
}

// TestStatusRetryFailedKeepsFailedRemaining proves failed rows are terminal
// (excluded from remaining) unless a retry run will re-pick them.
func TestStatusRetryFailedKeepsFailedRemaining(t *testing.T) {
	r, ids := statusFixture(t, 10)
	for i := 0; i < 3; i++ {
		seedDoneAt(t, r.Pool, ids[i], PromptVersion, nil)
	}
	for i := 3; i < 5; i++ {
		insertSummary(t, r.Pool, "function", ids[i], PromptVersion, "failed")
		// Failed on unchanged input: terminal unless retried (S1C-10).
		setCurrentHash(t, r, ids[i])
	}
	for retry, want := range map[bool]int{false: 5, true: 7} {
		rep, err := r.Status(context.Background(), retry)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if rep.Remaining != want {
			t.Errorf("retryFailed=%v remaining = %d, want %d", retry, rep.Remaining, want)
		}
	}
}

// TestStatusETAUnknownWhenCompletionsShareOneInstant proves a zero time span
// gives "unknown" with a reason instead of dividing by zero.
func TestStatusETAUnknownWhenCompletionsShareOneInstant(t *testing.T) {
	r, ids := statusFixture(t, 5)
	at := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	seedDoneAt(t, r.Pool, ids[0], PromptVersion, &at)
	seedDoneAt(t, r.Pool, ids[1], PromptVersion, &at)

	rep, err := r.Status(context.Background(), false)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if rep.Measured || rep.ETA != 0 {
		t.Errorf("measured = %v eta = %s, want unknown", rep.Measured, rep.ETA)
	}
	if !strings.Contains(rep.Reason, "one instant") {
		t.Errorf("reason = %q, want it to mention one instant", rep.Reason)
	}
}

// TestRerunClearsOldCompletedAtWhilePending proves a row sent back to pending
// by a changed input hash loses its old completion time until it is done again.
func TestRerunClearsOldCompletedAtWhilePending(t *testing.T) {
	pool := testdb.New(t)
	dir := t.TempDir()
	source, spans := funcFile("A")
	seedSource(t, pool, dir, "pkg/scheduler/redo.go", source, spans)

	var sawStamp atomic.Int64
	fake := newFakeOllama(t, func(int64) {
		var n int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM summaries WHERE status = 'pending' AND completed_at IS NOT NULL`).Scan(&n); err != nil {
			t.Error(err)
		}
		sawStamp.Add(int64(n))
	})
	r := newRunner(pool, fake.URL, dir, 4096, io.Discard)
	if _, err := r.Run(context.Background(), RunOptions{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	// Change the function body (hash changes) and rerun.
	changed := strings.Replace(source, `v := "A"`, `v := "A2"`, 1)
	if err := os.WriteFile(filepath.Join(dir, "pkg/scheduler/redo.go"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	fake.requests.Store(0)
	sawStamp.Store(0)
	if _, err := r.Run(context.Background(), RunOptions{}); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if fake.requests.Load() != 1 {
		t.Fatalf("rerun requests = %d, want 1 (hash should have changed)", fake.requests.Load())
	}
	if got := sawStamp.Load(); got != 0 {
		t.Errorf("pending row still had completed_at during the rerun request (%d)", got)
	}
}
