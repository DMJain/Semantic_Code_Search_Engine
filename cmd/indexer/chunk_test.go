package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/chunk"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
)

// chunkFixture walks, parses and graphs the committed chunk fixture into a
// throwaway database, so the chunk stage runs over a real Postgres with real
// symbol spans (Seam 1).
func chunkFixture(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	pool := testdb.New(t)
	root := filepath.Join("testdata", "chunk")
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)
	runGraphStage(t, pool, root, graphOptions{})
	return pool, root
}

// runChunkStage drives the chunk stage and returns its stdout.
func runChunkStage(t *testing.T, pool *pgxpool.Pool, root string, opts chunkOptions) string {
	t.Helper()
	var buf bytes.Buffer
	if err := chunkStage(context.Background(), pool, root, opts, &buf); err != nil {
		t.Fatalf("chunkStage: %v", err)
	}
	return buf.String()
}

// fileLines reads a fixture file and splits it into 1-based lines.
func fileLines(t *testing.T, root, rel string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return strings.Split(string(b), "\n")
}

// TestChunkStageOneChunkPerFuncMethod proves every function and method of the
// fixture becomes exactly one chunk, whose content is the source lines from
// start_line through end_line (1-based, read from disk), and that no stored
// chunk violates the span invariant.
func TestChunkStageOneChunkPerFuncMethod(t *testing.T) {
	pool, root := chunkFixture(t)
	out := runChunkStage(t, pool, root, chunkOptions{})

	symbols := scalarInt(t, pool, `SELECT count(*) FROM symbols WHERE kind IN ('func', 'method')`)
	if got := scalarInt(t, pool, `SELECT count(*) FROM chunks`); got != symbols {
		t.Fatalf("chunk count = %d, want one per func/method = %d\n%s", got, symbols, out)
	}

	rows, err := pool.Query(context.Background(), `
		SELECT f.path, c.start_line, c.end_line, c.content
		FROM chunks c JOIN files f ON f.id = c.file_id
		ORDER BY f.path, c.start_line`)
	if err != nil {
		t.Fatalf("query chunks: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var path, content string
		var start, end int
		if err := rows.Scan(&path, &start, &end, &content); err != nil {
			t.Fatalf("scan chunk: %v", err)
		}
		lines := fileLines(t, root, path)
		if start < 1 || end > len(lines) {
			t.Errorf("%s: chunk span %d..%d outside %d lines", path, start, end, len(lines))
			continue
		}
		want := strings.Join(lines[start-1:end], "\n")
		if content != want {
			t.Errorf("%s:%d-%d content mismatch\ngot  %q\nwant %q", path, start, end, content, want)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("chunks rows: %v", err)
	}
	if n != int(symbols) {
		t.Errorf("verified %d chunks, want %d", n, symbols)
	}

	if bad := scalarInt(t, pool, `SELECT count(*) FROM chunks WHERE start_line <= 0 OR end_line < start_line`); bad != 0 {
		t.Errorf("chunks with a bad span = %d, want 0", bad)
	}
	if !strings.Contains(out, "2 files re-split, 10 chunks changed") {
		t.Errorf("output missing the re-split summary line:\n%s", out)
	}
}

// TestChunkStageDocCases pins the D-083 doc-comment boundary against the
// fixture: a block comment above a function is excluded, a marker line plus a
// doc line starts the chunk at the marker, a detached comment is excluded, and
// an adjacent //go:noinline is included.
func TestChunkStageDocCases(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})

	want := map[string][2]int{
		"Documented":     {3, 4},
		"Plain":          {6, 6},
		"Method":         {10, 10},
		"BlockCommented": {13, 13},
		"MarkerDoc":      {15, 17},
		"Detached":       {21, 21},
		"NoInline":       {23, 24},
		"UsesPod":        {26, 28},
	}
	rows, err := pool.Query(context.Background(), `
		SELECT s.name, c.start_line, c.end_line
		FROM chunks c
		JOIN symbols s ON s.id = c.symbol_id
		JOIN files f ON f.id = c.file_id
		WHERE f.path = 'basic/basic.go'`)
	if err != nil {
		t.Fatalf("query chunks: %v", err)
	}
	defer rows.Close()
	got := map[string][2]int{}
	for rows.Next() {
		var name string
		var start, end int
		if err := rows.Scan(&name, &start, &end); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[name] = [2]int{start, end}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	for name, span := range want {
		if got[name] != span {
			t.Errorf("%s span = %v, want %v", name, got[name], span)
		}
	}
	if len(got) != len(want) {
		t.Errorf("chunked symbols = %v, want exactly %v", got, want)
	}

	// Doc is not a chunk column, so assert the D-083 boundary where it is
	// observable: the block comment is not the symbol's doc, the marker plus
	// // line is.
	var blockDoc, markerDoc string
	if err := pool.QueryRow(context.Background(),
		`SELECT coalesce(doc, '') FROM symbols WHERE name = 'BlockCommented'`).Scan(&blockDoc); err != nil {
		t.Fatalf("read BlockCommented doc: %v", err)
	}
	if blockDoc != "" {
		t.Errorf("BlockCommented stored doc = %q, want empty (block comments are excluded)", blockDoc)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT coalesce(doc, '') FROM symbols WHERE name = 'MarkerDoc'`).Scan(&markerDoc); err != nil {
		t.Fatalf("read MarkerDoc doc: %v", err)
	}
	if markerDoc == "" {
		t.Errorf("MarkerDoc stored doc is empty, want the marker + doc lines")
	}
}

// TestChunkSpanRefreshOnResplit proves a re-split refreshes the stored line span
// even when the function's own content is byte-identical: a content-hash-only
// guard would leave the span stale when a change elsewhere in the file shifts
// it (the invariant is content == lines[start_line..end_line]).
func TestChunkSpanRefreshOnResplit(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})

	var chunkID, fileID int64
	var start, end int
	if err := pool.QueryRow(context.Background(), `
		SELECT c.id, c.file_id, c.start_line, c.end_line
		FROM chunks c JOIN symbols s ON s.id = c.symbol_id
		WHERE s.name = 'UsesPod'`).Scan(&chunkID, &fileID, &start, &end); err != nil {
		t.Fatalf("find UsesPod chunk: %v", err)
	}

	// Corrupt the span without touching the content hash, as a stale row would
	// be, then force the file stale so the next run re-splits it.
	if _, err := pool.Exec(context.Background(),
		`UPDATE chunks SET start_line = 1, end_line = 1 WHERE id = $1`, chunkID); err != nil {
		t.Fatalf("corrupt span: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE files SET chunked_sha = NULL WHERE id = $1`, fileID); err != nil {
		t.Fatalf("force file stale: %v", err)
	}
	runChunkStage(t, pool, root, chunkOptions{})

	var gotStart, gotEnd int
	var content string
	if err := pool.QueryRow(context.Background(),
		`SELECT start_line, end_line, content FROM chunks WHERE id = $1`, chunkID).
		Scan(&gotStart, &gotEnd, &content); err != nil {
		t.Fatalf("read chunk after re-split: %v", err)
	}
	if gotStart != start || gotEnd != end {
		t.Errorf("span after re-split = %d..%d, want %d..%d", gotStart, gotEnd, start, end)
	}
	lines := fileLines(t, root, "basic/basic.go")
	if want := strings.Join(lines[gotStart-1:gotEnd], "\n"); content != want {
		t.Errorf("content after re-split does not match span %d..%d:\ngot  %q\nwant %q",
			gotStart, gotEnd, content, want)
	}
}

// TestChunkStageSearchTSV proves every stored chunk has a non-empty tsvector,
// that the identifier-split tokens make the schedulePod chunk findable, and
// that token_count matches the shared estimate.
func TestChunkStageSearchTSV(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})

	if n := scalarInt(t, pool, `SELECT count(*) FROM chunks WHERE search_tsv = ''`); n != 0 {
		t.Errorf("chunks with an empty search_tsv = %d, want 0", n)
	}
	if n := scalarInt(t, pool, `
		SELECT count(*) FROM chunks c JOIN symbols s ON s.id = c.symbol_id
		WHERE s.name = 'UsesPod' AND c.search_tsv @@ to_tsquery('simple', 'pod')`); n != 1 {
		t.Errorf("UsesPod chunk matching tsquery 'pod' = %d, want 1", n)
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM chunks WHERE search_tsv @@ to_tsquery('simple', 'pod')`); n != 1 {
		t.Errorf("chunks matching tsquery 'pod' = %d, want 1", n)
	}

	rows, err := pool.Query(context.Background(), `SELECT content, token_count FROM chunks`)
	if err != nil {
		t.Fatalf("query chunks: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var content string
		var count int
		if err := rows.Scan(&content, &count); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if want := chunk.EstimateTokens(content); count != want {
			t.Errorf("token_count = %d, want EstimateTokens = %d", count, want)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
}

// chunkSnapshot is the id-bearing identity of every chunk, keyed by symbol, for
// cross-run and cross-database comparisons.
func chunkSnapshot(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var s string
	err := pool.QueryRow(context.Background(), `
		SELECT coalesce(string_agg(s.qualified_name || '|' || c.part_idx || '|' || c.id, ';'
			ORDER BY s.qualified_name, c.part_idx), '')
		FROM chunks c JOIN symbols s ON s.id = c.symbol_id`).Scan(&s)
	if err != nil {
		t.Fatalf("snapshot chunks: %v", err)
	}
	return s
}

// TestChunkStageIdempotent proves a second run over an unchanged tree is a
// no-op: it prints the settled summary and every chunk id is unchanged.
func TestChunkStageIdempotent(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})
	before := chunkSnapshot(t, pool)

	second := runChunkStage(t, pool, root, chunkOptions{})
	if !strings.Contains(second, "0 files to chunk") {
		t.Errorf("second run should find nothing to chunk:\n%s", second)
	}
	if !strings.Contains(second, "0 files re-split, 0 chunks changed") {
		t.Errorf("second run should report the settled summary:\n%s", second)
	}
	if after := chunkSnapshot(t, pool); after != before {
		t.Errorf("chunk ids changed on rerun:\nbefore %s\nafter  %s", before, after)
	}
}

// TestChunkStageResumeMatchesUninterrupted proves a limited run followed by a
// full run ends with exactly the chunk count and ids of one uninterrupted run.
func TestChunkStageResumeMatchesUninterrupted(t *testing.T) {
	full, fullRoot := chunkFixture(t)
	runChunkStage(t, full, fullRoot, chunkOptions{})
	want := chunkSnapshot(t, full)

	resumed, resumedRoot := chunkFixture(t)
	first := runChunkStage(t, resumed, resumedRoot, chunkOptions{Limit: 1})
	if !strings.Contains(first, "1 files re-split") {
		t.Fatalf("limited run should split exactly one file:\n%s", first)
	}
	runChunkStage(t, resumed, resumedRoot, chunkOptions{})

	if got := chunkSnapshot(t, resumed); got != want {
		t.Errorf("resumed chunks differ from uninterrupted:\ngot  %s\nwant %s", got, want)
	}
}

// TestChunkCeilingChangeResplitsAll proves the chunker parameters are part of
// files.chunked_sha (D-078): stamping a different ceiling makes every file
// stale and re-split, while the unchanged content leaves every chunk id alone.
func TestChunkCeilingChangeResplitsAll(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})
	before := chunkSnapshot(t, pool)

	rows, err := pool.Query(context.Background(), `SELECT id, sha FROM files`)
	if err != nil {
		t.Fatalf("query files: %v", err)
	}
	type fileSHA struct {
		id  int64
		sha string
	}
	var files []fileSHA
	for rows.Next() {
		var f fileSHA
		if err := rows.Scan(&f.id, &f.sha); err != nil {
			t.Fatalf("scan file: %v", err)
		}
		files = append(files, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("files rows: %v", err)
	}
	for _, f := range files {
		marker := chunk.ParamsSHA(f.sha, 2000, chunk.GroupMaxTokens, chunk.ChunkerVersion)
		if _, err := pool.Exec(context.Background(),
			`UPDATE files SET chunked_sha = $2 WHERE id = $1`, f.id, marker); err != nil {
			t.Fatalf("restamp file: %v", err)
		}
	}

	out := runChunkStage(t, pool, root, chunkOptions{})
	want := fmt.Sprintf("%d files re-split, 0 chunks changed", len(files))
	if !strings.Contains(out, want) {
		t.Errorf("output missing %q:\n%s", want, out)
	}
	if after := chunkSnapshot(t, pool); after != before {
		t.Errorf("chunk ids changed when only the ceiling changed:\nbefore %s\nafter  %s", before, after)
	}
}

// TestChunkWholeRepoCoverage proves the fixture files are out of slice yet
// still receive chunks: chunking is whole-repo, not slice-only.
func TestChunkWholeRepoCoverage(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})

	if n := scalarInt(t, pool, `SELECT count(*) FROM files WHERE in_slice`); n != 0 {
		t.Fatalf("fixture has %d in-slice files, want 0 (the test would not prove whole-repo)", n)
	}
	total := scalarInt(t, pool, `SELECT count(*) FROM files`)
	chunked := scalarInt(t, pool, `SELECT count(DISTINCT file_id) FROM chunks`)
	if chunked != total {
		t.Errorf("files with chunks = %d, want every file = %d", chunked, total)
	}
}
