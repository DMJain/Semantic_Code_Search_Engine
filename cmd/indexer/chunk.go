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

// chunkStage turns every function, method, struct and interface of the stale
// files into chunks (a type pairs with its same-file constructor as one group
// chunk), one transaction per file. Only files whose graph exists and is current
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

	// Recompute every chunk's context header from the current graph on every
	// run, even when no file was re-split: a header reads other files' edges,
	// which a file's own chunk marker cannot see (D-081).
	if _, err := recomputeHeaders(dbCtx, pool); err != nil {
		return err
	}

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

	// oversized_failed and header_degraded are table-wide, so a --limit or
	// no-op run still matches select count(*) (D-082).
	failed, err := countChunks(dbCtx, pool, `SELECT count(*) FROM chunks WHERE token_count > $1`, chunk.EmbeddableContentMax)
	if err != nil {
		return err
	}
	degraded, err := countChunks(dbCtx, pool, `SELECT count(*) FROM chunks WHERE header_degraded > 0`)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "oversized_failed: %d\n", failed)
	fmt.Fprintf(out, "header_degraded: %d\n", degraded)
	return nil
}

// countChunks runs one counting query with optional args.
func countChunks(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) (int, error) {
	var n int
	if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count chunks: %w", err)
	}
	return n, nil
}

// recomputeHeaders rebuilds the context header of every stored chunk from the
// current tree-sitter edges and updates a row only when its header, degradation
// marker, content hash or lexical text would change (D-081). It covers every
// chunk of the filtered repo, not just the files re-split this run, and runs in
// one transaction. It returns the number of rows changed.
func recomputeHeaders(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	callees, callers, err := rankedCalls(ctx, pool)
	if err != nil {
		return 0, err
	}

	rows, err := pool.Query(ctx, `
		SELECT c.id, c.symbol_id, c.content, c.token_count, c.header, c.header_degraded,
		       c.content_hash, coalesce(s.signature, ''), f.path, f.package
		FROM chunks c
		JOIN symbols s ON s.id = c.symbol_id
		JOIN files f ON f.id = c.file_id`)
	if err != nil {
		return 0, fmt.Errorf("list chunks for headers: %w", err)
	}
	defer rows.Close()

	type chunkRow struct {
		id        int64
		symbolID  int64
		content   string
		tokens    int
		header    string
		degraded  int
		hash      string
		signature string
		path      string
		pkg       string
	}
	var chunks []chunkRow
	for rows.Next() {
		var c chunkRow
		if err := rows.Scan(&c.id, &c.symbolID, &c.content, &c.tokens, &c.header,
			&c.degraded, &c.hash, &c.signature, &c.path, &c.pkg); err != nil {
			return 0, fmt.Errorf("scan chunk for header: %w", err)
		}
		chunks = append(chunks, c)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin header pass: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// ponytail: one UPDATE per chunk over the whole corpus. The spec's
	// set-based query is the edge ranking above; upgrade this loop to a single
	// batched/unnest UPDATE if the pass becomes a bottleneck.
	changed := 0
	for _, c := range chunks {
		in := chunk.HeaderInput{
			Package:   c.pkg,
			File:      c.path,
			Signature: c.signature,
			Callees:   callees[c.symbolID],
			Callers:   callers[c.symbolID],
		}
		header, degraded := chunk.BuildHeader(in, c.tokens)
		hash := chunk.ContentHash(header, c.content)
		lexical := chunk.LexicalText(header, c.content)
		tag, err := tx.Exec(ctx, `
			UPDATE chunks
			SET header = $2, header_degraded = $3, content_hash = $4, search_tsv = to_tsvector('simple', $5)
			WHERE id = $1
			  AND (header IS DISTINCT FROM $2
			       OR header_degraded IS DISTINCT FROM $3
			       OR content_hash IS DISTINCT FROM $4
			       OR search_tsv IS DISTINCT FROM to_tsvector('simple', $5))`,
			c.id, header, degraded, hash, lexical)
		if err != nil {
			return 0, fmt.Errorf("update header of chunk %d: %w", c.id, err)
		}
		changed += int(tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit header pass: %w", err)
	}
	return changed, nil
}

// rankedCalls returns the top-five callees and callers per symbol from the
// tree-sitter edges, keyed by symbol id, rendered as short names. The window
// query limits the work to five rows per symbol per side, so the full edge set
// is never pulled into Go (D-081).
//
// The ordering columns use COLLATE "C" so the row_number() selection uses byte
// order, exactly like the Go comparator in internal/chunk (lessCall/TopNames).
// The database's default collation can order `_`, `.`, case and non-ASCII
// differently from byte order, which would make the SQL pick a different top-5
// than a pure-Go rank of the same edges.
func rankedCalls(ctx context.Context, pool *pgxpool.Pool) (callees, callers map[int64][]string, err error) {
	rows, err := pool.Query(ctx, `
		WITH dedup AS (
			SELECT caller_id, callee_id, max((resolution = 'exact')::int) AS exact
			FROM edges
			WHERE source = 'treesitter' AND resolution IN ('exact', 'name_only')
			GROUP BY caller_id, callee_id
		),
		ranked AS (
			SELECT d.caller_id AS symbol_id, false AS caller_side, s.id AS ref_id,
			       s.name, s.qualified_name, (d.exact = 1) AS exact,
			       row_number() OVER (PARTITION BY d.caller_id
			           ORDER BY d.exact DESC, s.name COLLATE "C",
			                    s.qualified_name COLLATE "C", s.id) AS rn
			FROM dedup d JOIN symbols s ON s.id = d.callee_id
			UNION ALL
			SELECT d.callee_id, true, s.id,
			       s.name, s.qualified_name, (d.exact = 1),
			       row_number() OVER (PARTITION BY d.callee_id
			           ORDER BY d.exact DESC, s.name COLLATE "C",
			                    s.qualified_name COLLATE "C", s.id)
			FROM dedup d JOIN symbols s ON s.id = d.caller_id
		)
		SELECT symbol_id, caller_side, name, qualified_name, ref_id, exact
		FROM ranked WHERE rn <= $1`, chunk.HeaderListMax)
	if err != nil {
		return nil, nil, fmt.Errorf("rank header edges: %w", err)
	}
	defer rows.Close()

	refs := map[bool]map[int64][]chunk.CallRef{
		false: {},
		true:  {},
	}
	for rows.Next() {
		var symbolID, refID int64
		var callerSide, exact bool
		var name, qualified string
		if err := rows.Scan(&symbolID, &callerSide, &name, &qualified, &refID, &exact); err != nil {
			return nil, nil, fmt.Errorf("scan ranked edge: %w", err)
		}
		resolution := chunk.ResolutionNameOnly
		if exact {
			resolution = chunk.ResolutionExact
		}
		refs[callerSide][symbolID] = append(refs[callerSide][symbolID], chunk.CallRef{
			Name: name, QualifiedName: qualified, ID: refID, Resolution: resolution,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	toNames := func(m map[int64][]chunk.CallRef) map[int64][]string {
		out := make(map[int64][]string, len(m))
		for id, rs := range m {
			out[id] = chunk.TopNames(rs)
		}
		return out
	}
	return toNames(refs[false]), toNames(refs[true]), nil
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

// chunkFileTx reads one file, splits its symbols into chunks (one whole chunk
// per struct/interface, a type grouped with its same-file constructor as one
// chunk, and single-or-parts chunks per function/method), and writes them in a
// single transaction. The upsert updates a row only when its content or line
// span changed, so a no-op re-split rewrites nothing; header, header_degraded
// and content_hash are owned by the header pass, which a new row's provisional
// empty-header hash is immediately replaced by (D-081). Vanished parts and
// symbols are deleted, and the file is stamped last, so a rerun never changes a
// surviving chunk id.
func chunkFileTx(ctx context.Context, pool *pgxpool.Pool, root string, f chunkFile) (chunkStats, error) {
	var stats chunkStats
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.path)))
	if err != nil {
		return stats, fmt.Errorf("read %s: %w", f.path, err)
	}
	lines := strings.Split(string(src), "\n")

	facts := parse.FileFacts(src)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return stats, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	symbols, err := fileChunkSymbols(ctx, tx, f.id)
	if err != nil {
		return stats, err
	}
	chunks := chunk.SplitFile(symbols, lines, facts)

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
				token_count = EXCLUDED.token_count,
				start_line = EXCLUDED.start_line,
				end_line = EXCLUDED.end_line,
				search_tsv = EXCLUDED.search_tsv
			WHERE chunks.content IS DISTINCT FROM EXCLUDED.content
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
			// A conflict with identical content and span: left untouched.
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

// fileChunkSymbols loads the function, method, struct and interface symbols of
// one file in source order, with the span and doc the chunker needs.
func fileChunkSymbols(ctx context.Context, tx pgx.Tx, fileID int64) ([]chunk.Symbol, error) {
	rows, err := tx.Query(ctx, `
		SELECT id, file_id, kind, name, coalesce(receiver, ''), coalesce(signature, ''),
		       start_line, end_line, coalesce(doc, '')
		FROM symbols
		WHERE file_id = $1 AND kind IN ($2, $3, $4, $5)
		ORDER BY start_line, id`, fileID,
		parse.KindFunc, parse.KindMethod, parse.KindStruct, parse.KindInterface)
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
