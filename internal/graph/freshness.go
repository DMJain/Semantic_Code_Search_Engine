package graph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Staleness reasons printed by graph --status and used to classify files (D-076).
const (
	ReasonNeverGraphed  = "never graphed"
	ReasonContentChange = "content changed"
	ReasonSymbolsChange = "symbols changed"
)

// StaleReasons returns why a file is stale against the current fingerprint fp,
// or nil when fresh (D-076). graphedSHA is nil when the file was never graphed,
// which is reported as such alone. This is the shared staleness predicate behind
// `indexer graph --status` and the eval callgraph report's refusal to score a
// graph with a stale file.
func StaleReasons(sha string, graphedSHA, graphedFP *string, fp string) []string {
	if graphedSHA == nil {
		return []string{ReasonNeverGraphed}
	}
	var reasons []string
	if *graphedSHA != sha {
		reasons = append(reasons, ReasonContentChange)
	}
	if graphedFP == nil || *graphedFP != fp {
		reasons = append(reasons, ReasonSymbolsChange)
	}
	return reasons
}

// LoadSymbols reads the whole symbol index, ordered by id, joined to its file so
// each symbol carries its package and path. It is the input to Fingerprint, so
// both `indexer graph` and the eval callgraph report build the same fingerprint.
func LoadSymbols(ctx context.Context, pool *pgxpool.Pool) ([]Symbol, error) {
	rows, err := pool.Query(ctx, `
		SELECT s.id, s.file_id, f.path, f.package, s.name, s.qualified_name,
		       coalesce(s.receiver, ''), s.kind, s.start_line
		FROM symbols s JOIN files f ON f.id = s.file_id
		ORDER BY s.id`)
	if err != nil {
		return nil, fmt.Errorf("load symbols: %w", err)
	}
	defer rows.Close()

	var out []Symbol
	for rows.Next() {
		var s Symbol
		if err := rows.Scan(&s.ID, &s.FileID, &s.Path, &s.Package, &s.Name,
			&s.QualifiedName, &s.Receiver, &s.Kind, &s.StartLine); err != nil {
			return nil, fmt.Errorf("scan symbol: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// BuildNameIndex builds the cross-package declared-name index (spec story 101)
// by scanning the source of every indexed file once. It covers every indexed
// package, not only the selected --package, so alias-qualified calls resolve
// against the whole indexed set (an import path absent from it is external). The
// package lookup already stores the Go import path, so no staging/vendor mapping
// is needed here.
func BuildNameIndex(ctx context.Context, pool *pgxpool.Pool, root string) (*NameIndex, error) {
	rows, err := pool.Query(ctx, `SELECT path, package FROM files ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("list indexed files: %w", err)
	}
	defer rows.Close()

	names := NewNameIndex()
	for rows.Next() {
		var path, pkg string
		if err := rows.Scan(&path, &pkg); err != nil {
			return nil, fmt.Errorf("scan indexed file: %w", err)
		}
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		facts, err := SourceFacts(src)
		if err != nil {
			return nil, fmt.Errorf("facts %s: %w", path, err)
		}
		names.Add(pkg, facts.Declared)
		names.AddFields(pkg, facts.Fields)
		names.AddResults(pkg, facts.Results)
		names.AddInterfaces(pkg, facts.Interfaces)
	}
	return names, rows.Err()
}
