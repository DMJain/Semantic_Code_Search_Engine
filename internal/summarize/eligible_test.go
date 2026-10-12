package summarize

import (
	"context"
	"fmt"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
)

// The Go predicate and the SQL form of the eligibility rule (D-014) must
// agree on every fixture: body-length boundary, kind, and slice membership.
func TestEligibilityGoAndSQLFormsAgree(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	type fixture struct {
		name    string
		kind    string
		inSlice bool
		lines   int // end_line - start_line
		want    bool
	}
	fixtures := []fixture{
		{"func_2", parse.KindFunc, true, 2, false},
		{"func_3", parse.KindFunc, true, 3, true},
		{"func_4", parse.KindFunc, true, 4, true},
		{"method_2", parse.KindMethod, true, 2, false},
		{"method_3", parse.KindMethod, true, 3, true},
		{"method_4", parse.KindMethod, true, 4, true},
		{"struct_4", parse.KindStruct, true, 4, false},
		{"func_4_out", parse.KindFunc, false, 4, false},
		{"func_2_out", parse.KindFunc, false, 2, false},
	}

	for i, f := range fixtures {
		var fileID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO files (path, package, sha, loc, in_slice)
			VALUES ($1, 'p', 'sha', 100, $2) RETURNING id`,
			fmt.Sprintf("f%d.go", i), f.inSlice).Scan(&fileID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO symbols (file_id, kind, name, qualified_name, start_line, end_line, raw_calls)
			VALUES ($1, $2, $3, $3, 1, $4, '{}')`,
			fileID, f.kind, f.name, 1+f.lines); err != nil {
			t.Fatal(err)
		}
	}

	names := func(where string) map[string]bool {
		rows, err := pool.Query(ctx, `
			SELECT s.name FROM symbols s JOIN files f ON f.id = s.file_id WHERE `+where)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		got := map[string]bool{}
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			got[n] = true
		}
		return got
	}
	sqlEligible := names(EligibleWhere())
	sqlSkipped := names(SkippedWhere())

	for _, f := range fixtures {
		sym := parse.Symbol{Kind: f.kind, StartLine: 1, EndLine: 1 + f.lines}
		if got := Eligible(sym, f.inSlice); got != f.want {
			t.Errorf("%s: Go Eligible = %v, want %v", f.name, got, f.want)
		}
		if got := sqlEligible[f.name]; got != f.want {
			t.Errorf("%s: SQL eligible = %v, want %v", f.name, got, f.want)
		}
		wantSkipped := f.inSlice && f.kind != parse.KindStruct && !f.want
		if got := sqlSkipped[f.name]; got != wantSkipped {
			t.Errorf("%s: SQL skipped = %v, want %v", f.name, got, wantSkipped)
		}
	}
}
