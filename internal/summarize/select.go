package summarize

import (
	"context"
	"fmt"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
)

// This file reads the symbols a run or status report works over; it changes
// only when the selection query changes.

// symbolRow is one in-slice symbol as read from the database together with its
// stored summary status and hash (empty when there is no current row).
type symbolRow struct {
	id            int64
	qualifiedName string
	path          string
	sym           parse.Symbol
	storedStatus  Status
	storedHash    string
}

// eligible returns every in-slice function or method whose body is at least
// [MinBodyLines] lines, with its current function-summary status and hash, ordered by
// qualified_name (the sampling order).
func (r *Runner) eligible(ctx context.Context) ([]symbolRow, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT s.id, s.qualified_name, f.path, s.kind, f.in_slice,
		       s.start_line, s.end_line, coalesce(s.signature, ''),
		       coalesce(s.doc, ''), s.raw_calls,
		       coalesce(sm.status, ''), coalesce(sm.input_hash, '')
		FROM symbols s
		JOIN files f ON f.id = s.file_id
		LEFT JOIN summaries sm
		  ON sm.level = $2 AND sm.target_id = s.id AND sm.prompt_version = $1
		WHERE f.in_slice
		ORDER BY s.qualified_name`, PromptVersion, LevelFunction)
	if err != nil {
		return nil, fmt.Errorf("list eligible symbols: %w", err)
	}
	defer rows.Close()

	var out []symbolRow
	for rows.Next() {
		var sr symbolRow
		var kind, storedStatus string
		var inSlice bool
		if err := rows.Scan(&sr.id, &sr.qualifiedName, &sr.path, &kind, &inSlice,
			&sr.sym.StartLine, &sr.sym.EndLine, &sr.sym.Signature,
			&sr.sym.Doc, &sr.sym.RawCalls, &storedStatus, &sr.storedHash); err != nil {
			return nil, fmt.Errorf("scan symbol: %w", err)
		}
		sr.sym.Kind = kind
		sr.storedStatus = Status(storedStatus)
		sr.sym.QualifiedName = sr.qualifiedName
		if !Eligible(sr.sym, inSlice) {
			continue
		}
		out = append(out, sr)
	}
	return out, rows.Err()
}
