package summarize

import (
	"context"
	"testing"
)

// setCurrentHash stores on the symbol's summary row the input hash the symbol
// has now, so the row counts as current.
func setCurrentHash(t *testing.T, r *Runner, id int64) {
	t.Helper()
	ctx := context.Background()
	rows, err := r.eligible(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, sr := range rows {
		if sr.id != id {
			continue
		}
		in, err := BuildInput(r.KubeDir, sr.path, sr.sym)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Pool.Exec(ctx,
			`UPDATE summaries SET input_hash = $1 WHERE level = $2 AND target_id = $3`,
			in.Hash(), LevelFunction, id); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("symbol %d not eligible", id)
}
