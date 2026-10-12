package summarize

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
)

// seedFailedRow seeds one function and a failed summary row whose stored hash
// is the symbol's current hash when current is true, and a stale one otherwise.
func seedFailedRow(t *testing.T, current bool) (*Runner, *fakeOllama, int64) {
	t.Helper()
	pool := testdb.New(t)
	ctx := context.Background()
	dir := t.TempDir()
	source, spans := funcFile("Failing")
	seedSource(t, pool, dir, "pkg/scheduler/failing.go", source, spans)

	fake := newFakeOllama(t, nil)
	r := newRunner(pool, fake.URL, dir, 4096, io.Discard)

	rows, err := r.eligible(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("eligible rows = %d err = %v, want 1", len(rows), err)
	}
	hash := "stale-hash"
	if current {
		in, err := BuildInput(dir, rows[0].path, rows[0].sym)
		if err != nil {
			t.Fatal(err)
		}
		hash = in.Hash()
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO summaries (level, target_id, text, model, prompt_version, input_hash, status, reason)
		 VALUES ($1, $2, NULL, 'test-model', $3, $4, $5, $6)`,
		LevelFunction, rows[0].id, PromptVersion, hash, StatusFailed, ReasonHTTPError); err != nil {
		t.Fatal(err)
	}
	return r, fake, rows[0].id
}

func TestFailedRowWithChangedHashIsReattemptedWithoutRetryFlag(t *testing.T) {
	r, fake, id := seedFailedRow(t, false)
	ctx := context.Background()

	st, err := r.Run(ctx, RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if st.Done != 1 || st.Skipped != 0 {
		t.Fatalf("done = %d skipped = %d, want 1/0", st.Done, st.Skipped)
	}
	if got := fake.requests.Load(); got != 1 {
		t.Fatalf("HTTP calls = %d, want 1", got)
	}
	var status string
	if err := r.Pool.QueryRow(ctx,
		`SELECT status FROM summaries WHERE level = $1 AND target_id = $2`, LevelFunction, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != StatusDone.String() {
		t.Fatalf("status = %q, want done", status)
	}
}

func TestFailedRowWithUnchangedHashNeedsRetryFlag(t *testing.T) {
	r, fake, _ := seedFailedRow(t, true)
	ctx := context.Background()

	st, err := r.Run(ctx, RunOptions{})
	if err != nil {
		t.Fatalf("run without flag: %v", err)
	}
	if st.Done != 0 || st.Skipped != 1 || fake.requests.Load() != 0 {
		t.Fatalf("without flag done = %d skipped = %d calls = %d, want 0/1/0",
			st.Done, st.Skipped, fake.requests.Load())
	}

	st, err = r.Run(ctx, RunOptions{RetryFailed: true})
	if err != nil {
		t.Fatalf("run with flag: %v", err)
	}
	if st.Done != 1 || fake.requests.Load() != 1 {
		t.Fatalf("with flag done = %d calls = %d, want 1/1", st.Done, fake.requests.Load())
	}
}

func TestStatusRemainingCountsFailedRowWithChangedHash(t *testing.T) {
	r, _, _ := seedFailedRow(t, false)
	for _, retry := range []bool{false, true} {
		rep, err := r.Status(context.Background(), retry)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if rep.Remaining != 1 {
			t.Errorf("retryFailed=%v remaining = %d, want 1 (stale failed row is re-attempted)", retry, rep.Remaining)
		}
	}
}

func TestStatusRemainingExcludesFailedRowWithUnchangedHashUnlessRetry(t *testing.T) {
	r, _, _ := seedFailedRow(t, true)
	for retry, want := range map[bool]int{false: 0, true: 1} {
		rep, err := r.Status(context.Background(), retry)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if rep.Remaining != want {
			t.Errorf("retryFailed=%v remaining = %d, want %d", retry, rep.Remaining, want)
		}
	}
}

func TestSymbolRowSkip(t *testing.T) {
	const cur, old = "h-current", "h-old"
	cases := []struct {
		status Status
		hash   string
		retry  bool
		want   bool
	}{
		{StatusDone, cur, false, true},
		{StatusDone, cur, true, true},
		{StatusDone, old, false, false},
		{StatusDone, old, true, false},
		{StatusFailed, cur, false, true},
		{StatusFailed, cur, true, false},
		{StatusFailed, old, false, false},
		{StatusFailed, old, true, false},
		{StatusPending, cur, false, false},
		{StatusPending, cur, true, false},
		{StatusPending, old, false, false},
		{StatusPending, old, true, false},
	}
	for _, c := range cases {
		sr := symbolRow{storedStatus: c.status, storedHash: c.hash}
		if got := sr.skip(cur, c.retry); got != c.want {
			t.Errorf("status=%s hash=%s retry=%v: skip = %v, want %v", c.status, c.hash, c.retry, got, c.want)
		}
	}
}

// A failed row whose source cannot be read is counted as remaining rather than
// failing the whole report: the next run re-attempts it and surfaces the read
// error there.
func TestStatusFailedRowWithMissingSourceCountsAsRemaining(t *testing.T) {
	r, _, _ := seedFailedRow(t, true)
	if err := os.Remove(filepath.Join(r.KubeDir, "pkg/scheduler/failing.go")); err != nil {
		t.Fatal(err)
	}
	rep, err := r.Status(context.Background(), false)
	if err != nil {
		t.Fatalf("status with unreadable source: %v", err)
	}
	if rep.Remaining != 1 {
		t.Errorf("remaining = %d, want 1", rep.Remaining)
	}
}
