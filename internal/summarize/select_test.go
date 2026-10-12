package summarize

import (
	"context"
	"io"
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
