package summarize

import (
	"context"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
)

// TestSelectionSkipsSymbolsOfUnparsedFiles proves a symbol whose file has not
// been re-parsed since its content hash changed (for example after walk
// re-homed it) is not selected by the runner or by the shared SQL predicates,
// and is selected again once the file is parsed.
func TestSelectionSkipsSymbolsOfUnparsedFiles(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO files (path, package, sha, loc, in_slice, parsed_sha) VALUES ('p/a.go', 'p', 'x', 10, true, NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO symbols (file_id, kind, name, qualified_name, signature, start_line, end_line)
		 SELECT id, $1, 'F', 'p.F', 'func F()', 1, 9 FROM files`, parse.KindFunc); err != nil {
		t.Fatal(err)
	}

	count := func() (runner, eligible, inSlice, skipped int64) {
		t.Helper()
		rows, err := (&Runner{Pool: pool}).eligible(ctx)
		if err != nil {
			t.Fatal(err)
		}
		runner = int64(len(rows))
		for _, q := range []struct {
			dst   *int64
			where string
		}{{&eligible, EligibleWhere()}, {&inSlice, InSliceFuncWhere()}, {&skipped, SkippedWhere()}} {
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM symbols s JOIN files f ON f.id = s.file_id WHERE `+q.where).Scan(q.dst); err != nil {
				t.Fatal(err)
			}
		}
		return
	}

	if r, e, i, s := count(); r+e+i+s != 0 {
		t.Errorf("unparsed file selected: runner %d, eligible %d, in-slice %d, skipped %d, want all 0", r, e, i, s)
	}
	if _, err := pool.Exec(ctx, `UPDATE files SET parsed_sha = sha`); err != nil {
		t.Fatal(err)
	}
	if r, e, i, _ := count(); r != 1 || e != 1 || i != 1 {
		t.Errorf("parsed file: runner %d, eligible %d, in-slice %d, want 1 each", r, e, i)
	}
}
