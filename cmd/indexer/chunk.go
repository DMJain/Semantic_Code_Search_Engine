package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/chunk"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
)

func runChunk(ctx context.Context, args []string) error {
	fs := newFlagSet("chunk")
	limit := fs.Int("limit", 0, "chunk at most N stale files (0 = no limit)")
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
	return chunkStage(ctx, pool, cfg.KubeDir, chunkOptions{Limit: *limit}, os.Stdout)
}

// chunkOptions configures one chunk run.
type chunkOptions struct {
	// Limit caps the number of stale files processed (0 = no limit), so an
	// interrupted run can be resumed.
	Limit int
}

// chunkFile is one files row selected for chunking, with the freshness markers
// it was last graphed and chunked at (nil = never).
type chunkFile struct {
	id         int64
	path       string
	sha        string
	graphedSHA *string
	chunkedSHA *string
}

// chunkStage turns every function and method of the stale files into one chunk
// each, one transaction per file. Only files whose graph exists and is current
// and whose chunk marker is stale are processed, so a rerun over an unchanged
// tree writes nothing (D-078, D-081). DB work uses a context a signal does not
// cancel, so the in-flight file always commits; ctx is checked only between
// files.
func chunkStage(ctx context.Context, pool *pgxpool.Pool, root string, opts chunkOptions, out io.Writer) error {
	dbCtx := context.WithoutCancel(ctx)

	files, err := selectChunkFiles(dbCtx, pool)
	if err != nil {
		return err
	}

	var stale []chunkFile
	for _, f := range files {
		if chunkStale(f) {
			stale = append(stale, f)
		}
	}
	fmt.Fprintf(out, "%d files to chunk\n", len(stale))

	reSplit, changed := 0, 0
	extraParts := 0
	for _, f := range stale {
		if ctx.Err() != nil || (opts.Limit > 0 && reSplit >= opts.Limit) {
			break
		}
		stats, err := chunkFileTx(dbCtx, pool, root, f)
		if err != nil {
			return err
		}
		reSplit++
		changed += stats.changed
		extraParts += stats.extraParts
	}

	fmt.Fprintf(out, "%d files re-split, %d chunks changed\n", reSplit, changed)
	fmt.Fprintf(out, "parts above one: %d\n", extraParts)

	// Oversized statements are reported from the table, not the run, so a
	// --limit or no-op run still matches select count(*) (D-078, D-082).
	oversized, err := listOversized(dbCtx, pool)
	if err != nil {
		return err
	}
	for _, o := range oversized {
		fmt.Fprintf(out, "oversized: %s %s part %d/%d %d tokens\n",
			o.path, o.symbol, o.partIdx+1, o.parts, o.tokens)
	}
	fmt.Fprintf(out, "oversized_statements: %d\n", len(oversized))
	return nil
}

// oversizedPart is one stored chunk over ChunkMaxTokens, with the number of
// parts its symbol has, for the deterministic run log.
type oversizedPart struct {
	path    string
	symbol  string
	partIdx int
	parts   int
	tokens  int
}

// listOversized returns every stored chunk over the ceiling in a deterministic
// order, so the printed oversized_statements always equals the DB-wide count.
func listOversized(ctx context.Context, pool *pgxpool.Pool) ([]oversizedPart, error) {
	rows, err := pool.Query(ctx, `
		SELECT f.path, s.name, c.part_idx, c.token_count,
		       (SELECT count(*) FROM chunks c2 WHERE c2.symbol_id = c.symbol_id)
		FROM chunks c
		JOIN symbols s ON s.id = c.symbol_id
		JOIN files f ON f.id = c.file_id
		WHERE c.token_count > $1
		ORDER BY f.path, s.name, c.part_idx`, chunk.ChunkMaxTokens)
	if err != nil {
		return nil, fmt.Errorf("list oversized parts: %w", err)
	}
	defer rows.Close()

	var out []oversizedPart
	for rows.Next() {
		var o oversizedPart
		if err := rows.Scan(&o.path, &o.symbol, &o.partIdx, &o.parts, &o.tokens); err != nil {
			return nil, fmt.Errorf("scan oversized part: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// chunkStale reports whether a file needs (re)splitting: its graph must exist
// and be current, and its chunk marker must differ from the current chunker
// parameters. A NULL marker is stale.
func chunkStale(f chunkFile) bool {
	if f.graphedSHA == nil || *f.graphedSHA != f.sha {
		return false
	}
	return f.chunkedSHA == nil || *f.chunkedSHA != chunk.ChunkedSHA(f.sha)
}

// selectChunkFiles returns every file with the markers that decide chunk
// freshness, ordered by path so a limited run resumes deterministically.
func selectChunkFiles(ctx context.Context, pool *pgxpool.Pool) ([]chunkFile, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, path, sha, graphed_sha, chunked_sha FROM files ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	defer rows.Close()

	var out []chunkFile
	for rows.Next() {
		var f chunkFile
		if err := rows.Scan(&f.id, &f.path, &f.sha, &f.graphedSHA, &f.chunkedSHA); err != nil {
			return nil, fmt.Errorf("scan file: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// chunkStats is one file's contribution to the run summary.
type chunkStats struct {
	changed    int
	extraParts int
}

// chunkFileTx reads one file, splits its functions and methods into parts, and
// writes the chunks in a single transaction. Unchanged chunks are left
// untouched (the upsert updates only on a content-hash change), vanished parts
// and symbols are deleted, and the file is stamped last, so a rerun never
// changes a surviving chunk id (D-081).
func chunkFileTx(ctx context.Context, pool *pgxpool.Pool, root string, f chunkFile) (chunkStats, error) {
	var stats chunkStats
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.path)))
	if err != nil {
		return stats, fmt.Errorf("read %s: %w", f.path, err)
	}
	lines := strings.Split(string(src), "\n")

	statements := parse.BodyStatements(f.path, src)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return stats, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	symbols, err := fileChunkSymbols(ctx, tx, f.id)
	if err != nil {
		return stats, err
	}
	chunks := chunk.SplitFile(symbols, lines, statements)

	seen := make([]int64, 0, len(symbols))
	partCount := make(map[int64]int)
	for _, c := range chunks {
		seen = append(seen, c.SymbolID)
		partCount[c.SymbolID]++
		if c.PartIdx > 0 {
			stats.extraParts++
		}
		lexical := chunk.LexicalText("", c.Content)
		var id int64
		err := tx.QueryRow(ctx, `
			INSERT INTO chunks (symbol_id, file_id, part_idx, content, header, content_hash, token_count, start_line, end_line, search_tsv)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, to_tsvector('simple', $10))
			ON CONFLICT (symbol_id, part_idx) DO UPDATE SET
				file_id = EXCLUDED.file_id,
				content = EXCLUDED.content,
				header = EXCLUDED.header,
				content_hash = EXCLUDED.content_hash,
				token_count = EXCLUDED.token_count,
				start_line = EXCLUDED.start_line,
				end_line = EXCLUDED.end_line,
				search_tsv = EXCLUDED.search_tsv
			WHERE chunks.content_hash IS DISTINCT FROM EXCLUDED.content_hash
			   OR chunks.start_line IS DISTINCT FROM EXCLUDED.start_line
			   OR chunks.end_line IS DISTINCT FROM EXCLUDED.end_line
			RETURNING id`,
			c.SymbolID, f.id, c.PartIdx, c.Content, "", c.ContentHash,
			c.TokenCount, c.StartLine, c.EndLine, lexical,
		).Scan(&id)
		switch {
		case err == nil:
			stats.changed++
		case errors.Is(err, pgx.ErrNoRows):
			// A conflict with an identical content hash and span: left untouched.
		default:
			return stats, fmt.Errorf("upsert chunk for %s symbol %d: %w", f.path, c.SymbolID, err)
		}
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM chunks WHERE file_id = $1 AND symbol_id <> ALL($2::bigint[])`,
		f.id, seen); err != nil {
		return stats, fmt.Errorf("delete chunks of removed symbols in %s: %w", f.path, err)
	}
	// A symbol that now has fewer parts keeps its surviving rows untouched and
	// drops only the vanished tail, so a shrink never renumbers part ids.
	for symbolID, n := range partCount {
		if _, err := tx.Exec(ctx,
			`DELETE FROM chunks WHERE symbol_id = $1 AND part_idx >= $2`,
			symbolID, n); err != nil {
			return stats, fmt.Errorf("delete vanished parts of symbol %d in %s: %w", symbolID, f.path, err)
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE files SET chunked_sha = $2 WHERE id = $1`,
		f.id, chunk.ChunkedSHA(f.sha)); err != nil {
		return stats, fmt.Errorf("mark file chunked: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return stats, fmt.Errorf("commit: %w", err)
	}
	return stats, nil
}

// fileChunkSymbols loads the function and method symbols of one file in source
// order, with the span and doc the chunker needs.
func fileChunkSymbols(ctx context.Context, tx pgx.Tx, fileID int64) ([]chunk.Symbol, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, file_id, kind, name, coalesce(receiver, ''), coalesce(signature, ''),
		       start_line, end_line, coalesce(doc, '')
		FROM symbols
		WHERE file_id = $1 AND kind IN ($2, $3)
		ORDER BY start_line, id`, fileID, parse.KindFunc, parse.KindMethod)
	if err != nil {
		return nil, fmt.Errorf("list symbols: %w", err)
	}
	defer rows.Close()

	var out []chunk.Symbol
	for rows.Next() {
		var s chunk.Symbol
		if err := rows.Scan(&s.ID, &s.FileID, &s.Kind, &s.Name, &s.Receiver,
			&s.Signature, &s.StartLine, &s.EndLine, &s.Doc); err != nil {
			return nil, fmt.Errorf("scan symbol: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
