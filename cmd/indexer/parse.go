package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/summarize"
)

func runParse(ctx context.Context, args []string) error {
	fs := newFlagSet("parse")
	limit := fs.Int("limit", 0, "parse at most N files, whole packages (0 = no limit)")
	stats := fs.Bool("stats", false, "also print symbol counts by kind")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return parseCorpus(ctx, pool, cfg.KubeDir, *limit, *stats, os.Stdout)
}

// staleFile is one files row selected for (re)parsing.
type staleFile struct {
	id   int64
	path string
	pkg  string
}

// parsedFile is a file whose source has been parsed and whose symbols have
// their final package-wide qualified names.
type parsedFile struct {
	staleFile
	symbols []parse.Symbol
}

// parseStats counts one parse run for the printed summary.
type parseStats struct {
	filesToParse int
	filesParsed  int
	funcs        int
	methods      int
	structs      int
	interfaces   int
	parseErrors  int
}

// parseCorpus parses every file whose parsed_sha differs from sha. Any package
// with a stale file is re-parsed completely, so #n suffixes are reassigned
// correctly. Each package is written in one transaction; an interrupt lets
// the current package finish, then stops and exits 0, leaving the remaining
// packages stale for the next run.
func parseCorpus(ctx context.Context, pool *pgxpool.Pool, root string, limit int, showKinds bool, out io.Writer) error {
	// DB work uses a context that a signal does not cancel, so the in-flight
	// package always commits; ctx is checked only at package boundaries.
	dbCtx := context.WithoutCancel(ctx)

	// Symbols of files removed from the tree cascade away in walk, which cannot
	// delete their summaries (no foreign key); sweep those orphans first.
	if _, err := pool.Exec(dbCtx,
		`DELETE FROM summaries s WHERE s.level = $1
		   AND NOT EXISTS (SELECT 1 FROM symbols y WHERE y.id = s.target_id)`,
		summarize.LevelFunction); err != nil {
		return fmt.Errorf("delete orphan summaries: %w", err)
	}

	stale, err := selectStaleFiles(dbCtx, pool)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%d files to parse\n", len(stale))
	st := parseStats{filesToParse: len(stale)}

groups:
	for _, group := range groupByPackage(stale) {
		if ctx.Err() != nil || (limit > 0 && st.filesParsed >= limit) {
			break
		}
		files, err := packageFiles(dbCtx, pool, group[0].pkg)
		if err != nil {
			return err
		}
		parsed, err := parsePackage(root, files, &st)
		if err != nil {
			return err
		}
		if err := writePackage(dbCtx, pool, parsed); err != nil {
			return err
		}
		for _, pf := range parsed {
			st.filesParsed++
			for _, s := range pf.symbols {
				switch s.Kind {
				case parse.KindFunc:
					st.funcs++
				case parse.KindMethod:
					st.methods++
				case parse.KindStruct:
					st.structs++
				case parse.KindInterface:
					st.interfaces++
				}
			}
		}
		if ctx.Err() != nil {
			break groups
		}
	}

	fmt.Fprintf(out, "parsed %d files, %d symbols, %d parse errors\n",
		st.filesParsed, st.funcs+st.methods+st.structs+st.interfaces, st.parseErrors)
	if showKinds {
		fmt.Fprintf(out, "symbols by kind: func %d, method %d, struct %d, interface %d\n",
			st.funcs, st.methods, st.structs, st.interfaces)
	}
	return nil
}

// selectStaleFiles returns every file whose parse marker does not match its
// content hash, ordered by package then path.
func selectStaleFiles(ctx context.Context, pool *pgxpool.Pool) ([]staleFile, error) {
	return queryFiles(ctx, pool,
		`SELECT id, path, package FROM files WHERE parsed_sha IS DISTINCT FROM sha ORDER BY package, path`)
}

// packageFiles returns every file of one package, ordered by path, so the
// whole package is re-parsed whenever any of its files is stale.
func packageFiles(ctx context.Context, pool *pgxpool.Pool, pkg string) ([]staleFile, error) {
	return queryFiles(ctx, pool,
		`SELECT id, path, package FROM files WHERE package = $1 ORDER BY path`, pkg)
}

// queryFiles runs a files query and scans the rows.
func queryFiles(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) ([]staleFile, error) {
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	defer rows.Close()

	var out []staleFile
	for rows.Next() {
		var f staleFile
		if err := rows.Scan(&f.id, &f.path, &f.pkg); err != nil {
			return nil, fmt.Errorf("scan file: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// groupByPackage splits files, already ordered by package then path, into
// contiguous per-package groups.
func groupByPackage(files []staleFile) [][]staleFile {
	var groups [][]staleFile
	for _, f := range files {
		last := len(groups) - 1
		if last < 0 || groups[last][0].pkg != f.pkg {
			groups = append(groups, []staleFile{f})
			continue
		}
		groups[last] = append(groups[last], f)
	}
	return groups
}

// parsePackage reads and parses every file of one package, assigns
// package-wide qualified names and records stats.
func parsePackage(root string, files []staleFile, st *parseStats) ([]parsedFile, error) {
	parsed := make([]parsedFile, 0, len(files))
	fsyms := make([]parse.FileSymbols, 0, len(files))
	for _, f := range files {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.path)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f.path, err)
		}
		syms, perr := parse.File(f.path, src)
		if perr != nil {
			if !errors.Is(perr, parse.ErrSyntax) {
				return nil, fmt.Errorf("parse %s: %w", f.path, perr)
			}
			st.parseErrors++
		}
		parsed = append(parsed, parsedFile{staleFile: f, symbols: syms})
		fsyms = append(fsyms, parse.FileSymbols{Path: f.path, Symbols: syms})
	}

	parse.AssignQualifiedNames(fsyms)
	for i := range parsed {
		parsed[i].symbols = fsyms[i].Symbols
	}
	return parsed, nil
}

// writePackage writes every file of one package in a single transaction. New
// and changed symbols are upserted first (on qualified_name, keeping row ids
// stable across re-parses and across moves between files); only afterwards are
// symbols of the package's files that are absent from the whole package parse
// deleted together with their function summaries (D-065). Files are
// marked parsed last.
func writePackage(ctx context.Context, pool *pgxpool.Pool, files []parsedFile) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	fileIDs := make([]int64, 0, len(files))
	seen := []int64{}
	for _, pf := range files {
		fileIDs = append(fileIDs, pf.id)
		for _, s := range pf.symbols {
			var id int64
			err := tx.QueryRow(ctx, `
				INSERT INTO symbols (file_id, kind, name, qualified_name, receiver, signature, start_line, end_line, doc, raw_calls)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
				ON CONFLICT (qualified_name) DO UPDATE SET
					file_id = EXCLUDED.file_id,
					kind = EXCLUDED.kind,
					name = EXCLUDED.name,
					receiver = EXCLUDED.receiver,
					signature = EXCLUDED.signature,
					start_line = EXCLUDED.start_line,
					end_line = EXCLUDED.end_line,
					doc = EXCLUDED.doc,
					raw_calls = EXCLUDED.raw_calls
				RETURNING id`,
				pf.id, s.Kind, s.Name, s.QualifiedName,
				store.NullIfEmpty(s.Receiver), s.Signature, s.StartLine, s.EndLine, store.NullIfEmpty(s.Doc),
				rawCallsOrEmpty(s.RawCalls),
			).Scan(&id)
			if err != nil {
				return fmt.Errorf("upsert symbol %s: %w", s.QualifiedName, err)
			}
			seen = append(seen, id)
		}
	}

	deleted, err := deleteVanished(ctx, tx, fileIDs, seen)
	if err != nil {
		return err
	}
	if len(deleted) > 0 {
		if _, err := tx.Exec(ctx,
			`DELETE FROM summaries WHERE level = $2 AND target_id = ANY($1::bigint[])`,
			deleted, summarize.LevelFunction); err != nil {
			return fmt.Errorf("delete orphan summaries: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE files SET parsed_sha = sha WHERE id = ANY($1::bigint[])`, fileIDs); err != nil {
		return fmt.Errorf("mark files parsed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// deleteVanished removes symbols of the given files that were not seen in the
// whole package parse and returns their ids. The synthetic [parse.KindDecl]
// owner the chunk stage creates is never swept: it is not produced by the
// parse, so a re-parse of the package (triggered by any sibling edit) must
// leave it, and its residual chunk, in place (D-080).
func deleteVanished(ctx context.Context, tx pgx.Tx, fileIDs, seen []int64) ([]int64, error) {
	rows, err := tx.Query(ctx,
		`DELETE FROM symbols WHERE file_id = ANY($1::bigint[]) AND id <> ALL($2::bigint[]) AND kind <> $3 RETURNING id`,
		fileIDs, seen, parse.KindDecl)
	if err != nil {
		return nil, fmt.Errorf("delete vanished symbols: %w", err)
	}
	defer rows.Close()

	var deleted []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan deleted symbol: %w", err)
		}
		deleted = append(deleted, id)
	}
	return deleted, rows.Err()
}

// rawCallsOrEmpty maps a nil slice to an empty one, because raw_calls is
// NOT NULL DEFAULT '{}' and pgx would otherwise send NULL.
func rawCallsOrEmpty(calls []string) []string {
	if calls == nil {
		return []string{}
	}
	return calls
}
