package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/chunk"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
)

// updateChunkGoldens regenerates the part golden. It is manual only, so a
// normal go test never rewrites the golden.
var updateChunkGoldens = flag.Bool("update", false, "regenerate chunk golden files (manual only)")

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
// fixture becomes at least one chunk, and every symbol that fits the ceiling
// is exactly one chunk whose content is the source lines from start_line
// through end_line (1-based, read from disk). Split symbols are covered by
// TestChunkStageParts.
func TestChunkStageOneChunkPerFuncMethod(t *testing.T) {
	pool, root := chunkFixture(t)
	out := runChunkStage(t, pool, root, chunkOptions{})

	symbols := scalarInt(t, pool, `SELECT count(*) FROM symbols WHERE kind IN ('func', 'method')`)
	chunks := scalarInt(t, pool, `SELECT count(*) FROM chunks`)
	if chunks < symbols {
		t.Fatalf("chunk count = %d, want at least one per func/method = %d\n%s", chunks, symbols, out)
	}

	rows, err := pool.Query(context.Background(), `
		SELECT f.path, c.start_line, c.end_line, c.content
		FROM chunks c JOIN files f ON f.id = c.file_id
		WHERE (SELECT count(*) FROM chunks c2 WHERE c2.symbol_id = c.symbol_id) = 1
		  AND c.token_count <= $1
		ORDER BY f.path, c.start_line`, chunk.ChunkMaxTokens)
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
	if n == 0 {
		t.Errorf("verified 0 single-part chunks, want at least one")
	}

	if bad := scalarInt(t, pool, `SELECT count(*) FROM chunks WHERE start_line <= 0 OR end_line < start_line`); bad != 0 {
		t.Errorf("chunks with a bad span = %d, want 0", bad)
	}
	wantSummary := fmt.Sprintf("5 files re-split, %d chunks changed", chunks)
	if !strings.Contains(out, wantSummary) {
		t.Errorf("output missing %q:\n%s", wantSummary, out)
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
		"Widget":         {8, 8},
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

// storedPart is one chunks row as the part assertions read it.
type storedPart struct {
	partIdx int
	start   int
	end     int
	tokens  int
	content string
}

// symbolParts returns the stored parts of one symbol in part order.
func symbolParts(t *testing.T, pool *pgxpool.Pool, name string) []storedPart {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT c.part_idx, c.start_line, c.end_line, c.token_count, c.content
		FROM chunks c JOIN symbols s ON s.id = c.symbol_id
		WHERE s.name = $1
		ORDER BY c.part_idx`, name)
	if err != nil {
		t.Fatalf("query parts of %s: %v", name, err)
	}
	defer rows.Close()
	var out []storedPart
	for rows.Next() {
		var p storedPart
		if err := rows.Scan(&p.partIdx, &p.start, &p.end, &p.tokens, &p.content); err != nil {
			t.Fatalf("scan part: %v", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("parts rows: %v", err)
	}
	return out
}

// TestChunkStageParts proves a small and a generic function stay single-part
// with the exact source span, while the long function and the giant-switch
// function split at top-level statement boundaries with the chosen part format.
func TestChunkStageParts(t *testing.T) {
	pool, root := chunkFixture(t)
	out := runChunkStage(t, pool, root, chunkOptions{})
	lines := fileLines(t, root, "parts/parts.go")

	for _, name := range []string{"SmallStay", "Generic"} {
		parts := symbolParts(t, pool, name)
		if len(parts) != 1 {
			t.Fatalf("%s parts = %d, want 1", name, len(parts))
		}
		p := parts[0]
		if want := strings.Join(lines[p.start-1:p.end], "\n"); p.content != want {
			t.Errorf("%s content mismatch\ngot  %q\nwant %q", name, p.content, want)
		}
	}

	var symID int64
	var symStart, symEnd int
	var doc string
	if err := pool.QueryRow(context.Background(), `
		SELECT id, start_line, end_line, coalesce(doc, '')
		FROM symbols WHERE name = 'Long'`).Scan(&symID, &symStart, &symEnd, &doc); err != nil {
		t.Fatalf("read Long symbol: %v", err)
	}
	src := []byte(strings.Join(lines, "\n"))
	ranges := parse.FileFacts("parts/parts.go", src).Statements[symStart]
	if len(ranges) < 2 {
		t.Fatalf("Long statement ranges = %d, want at least 2", len(ranges))
	}
	parts := symbolParts(t, pool, "Long")
	if len(parts) < 2 {
		t.Fatalf("Long parts = %d, want at least 2", len(parts))
	}
	header := strings.Join(lines[symStart-1:ranges[0].StartLine-1], "\n")
	docLines := 0
	if doc != "" {
		docLines = strings.Count(doc, "\n") + 1
	}
	for i, p := range parts {
		if p.partIdx != i {
			t.Errorf("Long part_idx = %d, want %d", p.partIdx, i)
		}
		marker := fmt.Sprintf("// ... part %d/%d", i+1, len(parts))
		bodyStart := p.start
		if i == 0 {
			bodyStart = ranges[0].StartLine
		}
		want := header + "\n" + marker + "\n" + strings.Join(lines[bodyStart-1:p.end], "\n")
		if i == 0 && doc != "" {
			want = doc + "\n" + want
		}
		if p.content != want {
			t.Errorf("Long part %d content mismatch\ngot  %q\nwant %q", i, p.content, want)
		}
		if p.start < symStart-docLines || p.end > symEnd || p.end < p.start {
			t.Errorf("Long part %d span %d..%d outside %d..%d", i, p.start, p.end, symStart-docLines, symEnd)
		}
		if p.tokens != chunk.EstimateTokens(p.content) {
			t.Errorf("Long part %d token_count = %d, want %d", i, p.tokens, chunk.EstimateTokens(p.content))
		}
		if p.tokens > chunk.ChunkMaxTokens {
			t.Errorf("Long part %d token_count = %d, want <= %d", i, p.tokens, chunk.ChunkMaxTokens)
		}
		// Every statement falls wholly inside exactly one part.
		for _, r := range ranges {
			covered := 0
			for _, q := range parts {
				if q.start <= r.StartLine && r.EndLine <= q.end {
					covered++
				}
			}
			if covered != 1 {
				t.Errorf("statement %d..%d covered by %d parts, want 1", r.StartLine, r.EndLine, covered)
			}
		}
	}

	giant := symbolParts(t, pool, "GiantSwitch")
	if len(giant) != 1 {
		t.Fatalf("GiantSwitch parts = %d, want 1 (one switch statement)", len(giant))
	}
	var giantStart int
	if err := pool.QueryRow(context.Background(),
		`SELECT start_line FROM symbols WHERE name = 'GiantSwitch'`).Scan(&giantStart); err != nil {
		t.Fatalf("read GiantSwitch symbol: %v", err)
	}
	giantRanges := parse.FileFacts("parts/parts.go", src).Statements[giantStart]
	g := giant[0]
	if g.tokens <= chunk.ChunkMaxTokens {
		t.Errorf("GiantSwitch token_count = %d, want > %d (oversized statement)", g.tokens, chunk.ChunkMaxTokens)
	}
	if !strings.Contains(g.content, "// ... part 1/1") {
		t.Errorf("GiantSwitch part missing marker:\n%q", g.content)
	}
	if want := strings.Join(lines[giantRanges[0].StartLine-1:g.end], "\n"); !strings.HasSuffix(g.content, want) {
		t.Errorf("GiantSwitch part does not end with its covered source lines")
	}

	// SharedLine's first statement shares the declaration line, so the source
	// gap is empty: every part must still start with the synthetic header
	// (after the doc for part 0).
	shared := symbolParts(t, pool, "SharedLine")
	if len(shared) < 2 {
		t.Fatalf("SharedLine parts = %d, want at least 2", len(shared))
	}
	var sharedDoc string
	if err := pool.QueryRow(context.Background(),
		`SELECT coalesce(doc, '') FROM symbols WHERE name = 'SharedLine'`).Scan(&sharedDoc); err != nil {
		t.Fatalf("read SharedLine doc: %v", err)
	}
	for i, p := range shared {
		wantPrefix := "func SharedLine() {\n" + fmt.Sprintf("// ... part %d/%d", i+1, len(shared)) + "\n"
		if i == 0 && sharedDoc != "" {
			wantPrefix = sharedDoc + "\n" + wantPrefix
		}
		if !strings.HasPrefix(p.content, wantPrefix) {
			t.Errorf("SharedLine part %d lost the signature header:\n%q", i, p.content)
		}
	}

	if printed := printedOversized(t, out); int64(printed) != dbOversized(t, pool) {
		t.Errorf("printed oversized_statements = %d, DB count = %d", printed, dbOversized(t, pool))
	}
	if !strings.Contains(out, "oversized: parts/parts.go GiantSwitch part 1/1") {
		t.Errorf("output missing the oversized part log line:\n%s", out)
	}

	printedExtra, dbExtra := -1, scalarInt(t, pool, `SELECT count(*) FROM chunks WHERE part_idx > 0`)
	for _, line := range strings.Split(out, "\n") {
		fmt.Sscanf(line, "parts above one: %d", &printedExtra)
	}
	if int64(printedExtra) != dbExtra {
		t.Errorf("printed parts above one = %d, DB count = %d", printedExtra, dbExtra)
	}
}

// printedOversized reads the oversized_statements count from a run's output.
func printedOversized(t *testing.T, out string) int {
	t.Helper()
	n := -1
	for _, line := range strings.Split(out, "\n") {
		fmt.Sscanf(line, "oversized_statements: %d", &n)
	}
	if n < 0 {
		t.Fatalf("output missing oversized_statements:\n%s", out)
	}
	return n
}

// dbOversized is the DB-wide count the printed oversized_statements must equal.
func dbOversized(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	return scalarInt(t, pool, `SELECT count(*) FROM chunks WHERE token_count > $1`, chunk.ChunkMaxTokens)
}

// TestChunkStageOversizedCountIsDBWide proves the printed oversized_statements
// matches the table after a --limit run and after the full run, not just the
// rows written in one run.
func TestChunkStageOversizedCountIsDBWide(t *testing.T) {
	pool, root := chunkFixture(t)

	limited := runChunkStage(t, pool, root, chunkOptions{Limit: 2})
	if printed, want := printedOversized(t, limited), dbOversized(t, pool); int64(printed) != want {
		t.Errorf("limit run: printed oversized_statements = %d, DB count = %d\n%s", printed, want, limited)
	}

	full := runChunkStage(t, pool, root, chunkOptions{})
	if printed, want := printedOversized(t, full), dbOversized(t, pool); int64(printed) != want {
		t.Errorf("full run: printed oversized_statements = %d, DB count = %d\n%s", printed, want, full)
	}
	if dbOversized(t, pool) == 0 {
		t.Errorf("fixture has no oversized chunks; the test proves nothing")
	}
}

// TestChunkStagePartShrinkDeletesVanishedOnly proves a re-split after stale
// source keeps every surviving part id and deletes only a part above the new
// count.
func TestChunkStagePartShrinkDeletesVanishedOnly(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})

	var symID, fileID int64
	if err := pool.QueryRow(context.Background(), `
		SELECT s.id, f.id FROM symbols s JOIN files f ON f.id = s.file_id
		WHERE s.name = 'Long'`).Scan(&symID, &fileID); err != nil {
		t.Fatalf("find Long symbol: %v", err)
	}
	before := map[int]int64{}
	rows, err := pool.Query(context.Background(),
		`SELECT part_idx, id FROM chunks WHERE symbol_id = $1 ORDER BY part_idx`, symID)
	if err != nil {
		t.Fatalf("query Long parts: %v", err)
	}
	for rows.Next() {
		var idx int
		var id int64
		if err := rows.Scan(&idx, &id); err != nil {
			t.Fatalf("scan Long part: %v", err)
		}
		before[idx] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("Long part rows: %v", err)
	}

	// A part left over from a previously longer function.
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO chunks (symbol_id, file_id, part_idx, content, content_hash, token_count, start_line, end_line)
		VALUES ($1, $2, 99, 'stale', 'deadbeef', 1, 1, 1)`, symID, fileID); err != nil {
		t.Fatalf("insert vanished part: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE files SET chunked_sha = NULL WHERE id = $1`, fileID); err != nil {
		t.Fatalf("force file stale: %v", err)
	}
	runChunkStage(t, pool, root, chunkOptions{})

	after := map[int]int64{}
	rows, err = pool.Query(context.Background(),
		`SELECT part_idx, id FROM chunks WHERE symbol_id = $1 ORDER BY part_idx`, symID)
	if err != nil {
		t.Fatalf("query Long parts after re-split: %v", err)
	}
	for rows.Next() {
		var idx int
		var id int64
		if err := rows.Scan(&idx, &id); err != nil {
			t.Fatalf("scan Long part after re-split: %v", err)
		}
		after[idx] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("Long part rows after re-split: %v", err)
	}

	if _, ok := after[99]; ok {
		t.Errorf("vanished part 99 survived the re-split")
	}
	for idx, id := range before {
		if got := after[idx]; got != id {
			t.Errorf("surviving part %d id = %d, want %d", idx, got, id)
		}
	}
	if len(after) != len(before) {
		t.Errorf("part count after re-split = %d, want %d", len(after), len(before))
	}
}

// partGolden is the read-only expectation for one stored part.
type partGolden struct {
	PartIdx    int `json:"part_idx"`
	StartLine  int `json:"start_line"`
	EndLine    int `json:"end_line"`
	TokenCount int `json:"token_count"`
}

// TestChunkPartGolden pins the split shape of all five fixture functions
// (small, generic, long, giant switch, shared declaration line). Regenerate
// manually with -update only.
func TestChunkPartGolden(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})

	got := map[string][]partGolden{}
	for _, name := range []string{"GiantSwitch", "Generic", "Long", "SharedLine", "SmallStay"} {
		for _, p := range symbolParts(t, pool, name) {
			got[name] = append(got[name], partGolden{
				PartIdx: p.partIdx, StartLine: p.start, EndLine: p.end, TokenCount: p.tokens,
			})
		}
	}
	data, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}
	data = append(data, '\n')

	golden := filepath.Join("testdata", "chunk", "golden", "parts.json")
	if *updateChunkGoldens {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(golden, data, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(data, want) {
		t.Errorf("part golden mismatch (regenerate with -update if intended):\n--- got ---\n%s\n--- want ---\n%s", data, want)
	}
}

// symbolSpan reads one symbol's stored span and doc, so a group-span
// expectation is derived from the same stored facts the chunker used.
func symbolSpan(t *testing.T, pool *pgxpool.Pool, name string) (start, end int, doc string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT start_line, end_line, coalesce(doc, '') FROM symbols WHERE name = $1`, name).
		Scan(&start, &end, &doc); err != nil {
		t.Fatalf("read %s symbol: %v", name, err)
	}
	return start, end, doc
}

// docStartLine is the first line of a declaration including its doc comment.
func docStartLine(start int, doc string) int {
	n := 0
	if doc != "" {
		n = strings.Count(doc, "\n") + 1
	}
	return start - n
}

// TestChunkStageGroupChunks proves the struct/interface group rules over the
// real fixture tree and Postgres: a type with a same-file constructor is one
// group chunk owned by the type spanning both declarations (the constructor gets
// no chunk); a type with no constructor is one chunk; a pair over GroupMaxTokens
// is two chunks; a constructor in another file is never grouped; the first of
// two candidates groups and the second keeps its own chunk; a function is a
// constructor by return type alone; no symbol owns two part-0 chunks; and a
// rerun leaves every chunk id, including the group chunk, unchanged.
func TestChunkStageGroupChunks(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})
	ctx := context.Background()

	for _, g := range []struct{ typ, ctor string }{
		{"Gadget", "NewGadget"},
		{"Reader", "NewReader"},
		{"Twin", "NewTwin"},
		{"Zed", "buildZed"},
	} {
		parts := symbolParts(t, pool, g.typ)
		if len(parts) != 1 || parts[0].partIdx != 0 {
			t.Fatalf("%s chunks = %+v, want exactly one part 0", g.typ, parts)
		}
		if ctor := symbolParts(t, pool, g.ctor); len(ctor) != 0 {
			t.Errorf("grouped constructor %s still has a chunk: %+v", g.ctor, ctor)
		}
		ts, _, tdoc := symbolSpan(t, pool, g.typ)
		_, ce, _ := symbolSpan(t, pool, g.ctor)
		wantStart := docStartLine(ts, tdoc)
		p := parts[0]
		if p.start != wantStart || p.end != ce {
			t.Errorf("%s group span = %d..%d, want %d..%d", g.typ, p.start, p.end, wantStart, ce)
		}
		lines := fileLines(t, root, "group/group.go")
		if p.start < 1 || p.end > len(lines) {
			t.Errorf("%s group span %d..%d outside %d lines", g.typ, p.start, p.end, len(lines))
			continue
		}
		if want := strings.Join(lines[p.start-1:p.end], "\n"); p.content != want {
			t.Errorf("%s group content mismatch\ngot  %q\nwant %q", g.typ, p.content, want)
		}
		// The group span must cover both declarations, not just the type.
		if p.end < ce {
			t.Errorf("%s group span ends at %d, before the constructor end %d", g.typ, p.end, ce)
		}
	}

	// A struct with no constructor: one chunk of its own declaration.
	s, e, doc := symbolSpan(t, pool, "Solo")
	if parts := symbolParts(t, pool, "Solo"); len(parts) != 1 || parts[0].start != docStartLine(s, doc) || parts[0].end != e {
		t.Errorf("Solo chunks = %+v, want one span %d..%d", parts, docStartLine(s, doc), e)
	}

	// A pair over GroupMaxTokens: the type and the constructor keep separate
	// chunks; the constructor is not grouped.
	big, newBig := symbolParts(t, pool, "Big"), symbolParts(t, pool, "NewBig")
	if len(big) != 1 || len(newBig) != 1 {
		t.Fatalf("Big parts = %d, NewBig parts = %d, want 1 each (not grouped)", len(big), len(newBig))
	}
	if big[0].end >= newBig[0].start {
		t.Errorf("Big chunk end %d reaches NewBig start %d; the pair was grouped", big[0].end, newBig[0].start)
	}

	// A constructor in another file: never grouped.
	if far, newFar := symbolParts(t, pool, "Far"), symbolParts(t, pool, "NewFar"); len(far) != 1 || len(newFar) != 1 {
		t.Errorf("Far parts = %d, NewFar parts = %d, want 1 each", len(far), len(newFar))
	}

	// The second of two candidates keeps its own chunk.
	if made := symbolParts(t, pool, "MakeTwin"); len(made) != 1 {
		t.Errorf("MakeTwin chunks = %d, want 1 (second candidate not grouped)", len(made))
	}

	// A function matched by two types (NewBeta by name matches Beta, by return
	// type matches Alpha) joins exactly one group: it owns no chunk, its body
	// sits in one chunk only, and the Alpha group and the Beta chunk do not
	// overlap.
	if nb := symbolParts(t, pool, "NewBeta"); len(nb) != 0 {
		t.Errorf("NewBeta owns a chunk: %+v", nb)
	}
	alpha, beta := symbolParts(t, pool, "Alpha"), symbolParts(t, pool, "Beta")
	if len(alpha) != 1 || len(beta) != 1 {
		t.Fatalf("Alpha parts = %d, Beta parts = %d, want 1 each", len(alpha), len(beta))
	}
	lines := fileLines(t, root, "group/group.go")
	_, nbEnd, _ := symbolSpan(t, pool, "NewBeta")
	nbStart, _, _ := symbolSpan(t, pool, "NewBeta")
	if nbStart < 1 || nbEnd > len(lines) {
		t.Fatalf("NewBeta span %d..%d outside %d lines", nbStart, nbEnd, len(lines))
	}
	body := strings.Join(lines[nbStart-1:nbEnd], "\n")
	hits := 0
	all := append(append([]storedPart{}, alpha...), beta...)
	for _, p := range all {
		if strings.Contains(p.content, body) {
			hits++
		}
	}
	if hits != 1 {
		t.Errorf("NewBeta body appears in %d group chunks, want exactly 1", hits)
	}
	if alpha[0].start <= beta[0].end && beta[0].start <= alpha[0].end {
		t.Errorf("Alpha group %d..%d overlaps Beta chunk %d..%d",
			alpha[0].start, alpha[0].end, beta[0].start, beta[0].end)
	}

	// No symbol owns more than one part-0 chunk.
	if n := scalarInt(t, pool, `
		SELECT count(*) FROM (
			SELECT symbol_id FROM chunks WHERE part_idx = 0 GROUP BY symbol_id HAVING count(*) > 1
		) dup`); n != 0 {
		t.Errorf("%d symbols have more than one part 0 chunk", n)
	}

	// A rerun over the unchanged tree changes no id, the group chunk included.
	var gadgetBefore int64
	if err := pool.QueryRow(ctx, `
		SELECT c.id FROM chunks c JOIN symbols s ON s.id = c.symbol_id WHERE s.name = 'Gadget'`).
		Scan(&gadgetBefore); err != nil {
		t.Fatalf("read Gadget chunk id: %v", err)
	}
	out := runChunkStage(t, pool, root, chunkOptions{})
	if !strings.Contains(out, "0 files re-split, 0 chunks changed") {
		t.Errorf("rerun summary not settled:\n%s", out)
	}
	var gadgetAfter int64
	if err := pool.QueryRow(ctx, `
		SELECT c.id FROM chunks c JOIN symbols s ON s.id = c.symbol_id WHERE s.name = 'Gadget'`).
		Scan(&gadgetAfter); err != nil {
		t.Fatalf("read Gadget chunk id after rerun: %v", err)
	}
	if gadgetAfter != gadgetBefore {
		t.Errorf("group chunk id changed on rerun: before %d, after %d", gadgetBefore, gadgetAfter)
	}
}

// TestChunkGroupGolden pins the split shape of the group fixture: the grouped
// types (one chunk each), the ungrouped type and constructors, and the second
// candidate's own chunk. Regenerate manually with -update only.
func TestChunkGroupGolden(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})

	got := map[string][]partGolden{}
	for _, name := range []string{
		"Alpha", "Big", "Beta", "Far", "Gadget", "MakeTwin", "NewBig", "NewFar", "Reader", "Solo", "Twin", "Zed",
	} {
		for _, p := range symbolParts(t, pool, name) {
			got[name] = append(got[name], partGolden{
				PartIdx: p.partIdx, StartLine: p.start, EndLine: p.end, TokenCount: p.tokens,
			})
		}
	}
	data, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}
	data = append(data, '\n')

	golden := filepath.Join("testdata", "chunk", "golden", "groups.json")
	if *updateChunkGoldens {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(golden, data, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(data, want) {
		t.Errorf("group golden mismatch (regenerate with -update if intended):\n--- got ---\n%s\n--- want ---\n%s", data, want)
	}
}
