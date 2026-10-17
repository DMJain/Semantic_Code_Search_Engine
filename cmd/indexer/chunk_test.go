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
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/graph"
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
	wantSummary := fmt.Sprintf("%d files re-split, %d chunks changed",
		scalarInt(t, pool, `SELECT count(*) FROM files`), chunks)
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
// still receive chunks: chunking is whole-repo, not slice-only. Every file with
// a function, method, struct or interface symbol has a chunk, and every file
// with none of those but with top-level const, var or type declarations gets
// exactly one residual chunk; a doc-only file gets none.
func TestChunkWholeRepoCoverage(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})

	if n := scalarInt(t, pool, `SELECT count(*) FROM files WHERE in_slice`); n != 0 {
		t.Fatalf("fixture has %d in-slice files, want 0 (the test would not prove whole-repo)", n)
	}
	total := scalarInt(t, pool, `
		SELECT count(DISTINCT file_id) FROM symbols
		WHERE kind IN ('func', 'method', 'struct', 'interface')`)
	chunked := scalarInt(t, pool, `SELECT count(DISTINCT file_id) FROM chunks`)

	// Files with no chunkable symbol add a residual chunk only when they have
	// top-level declarations; the rest stay chunkless.
	rows, err := pool.Query(context.Background(), `
		SELECT f.path FROM files f
		WHERE NOT EXISTS (
			SELECT 1 FROM symbols s WHERE s.file_id = f.id
			  AND s.kind IN ('func', 'method', 'struct', 'interface'))`)
	if err != nil {
		t.Fatalf("query files without a chunkable symbol: %v", err)
	}
	defer rows.Close()
	var residualFiles int64
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			t.Fatalf("scan file: %v", err)
		}
		if len(parse.TopLevelDecls([]byte(strings.Join(fileLines(t, root, path), "\n")))) > 0 {
			residualFiles++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("files rows: %v", err)
	}

	if chunked != total+residualFiles {
		t.Errorf("files with chunks = %d, want chunkable files %d plus residual files %d",
			chunked, total, residualFiles)
	}
	if residualFiles == 0 {
		t.Errorf("fixture has no residual file; the test proves nothing")
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
	ranges := parse.FileFacts(src).Statements[symStart]
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
	giantRanges := parse.FileFacts(src).Statements[giantStart]
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
	var giantLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "oversized: parts/parts.go GiantSwitch ") {
			giantLine = line
		}
	}
	if giantLine == "" {
		t.Errorf("output missing the oversized part log line:\n%s", out)
	} else {
		var path, symbol string
		var idx, parts, tokens int
		if _, err := fmt.Sscanf(giantLine, "oversized: %s %s part %d/%d %d tokens", &path, &symbol, &idx, &parts, &tokens); err != nil {
			t.Errorf("unparseable oversized line %q: %v", giantLine, err)
		}
		if idx != 1 || parts != 1 {
			t.Errorf("GiantSwitch oversized line = %q, want part 1/1", giantLine)
		}
		if tokens <= chunk.ChunkMaxTokens {
			t.Errorf("GiantSwitch oversized line tokens = %d, want > %d (the line must print the token count)",
				tokens, chunk.ChunkMaxTokens)
		}
	}

	printedExtra, dbExtra := -1, scalarInt(t, pool, `SELECT count(*) FROM chunks WHERE part_idx > 0`)
	for _, line := range strings.Split(out, "\n") {
		_, _ = fmt.Sscanf(line, "parts above one: %d", &printedExtra)
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
		_, _ = fmt.Sscanf(line, "oversized_statements: %d", &n)
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

// storedHeader reads one symbol's chunk header, content, hash, degradation and
// token count, the facts the header pass owns.
func storedHeader(t *testing.T, pool *pgxpool.Pool, name string) (header, content, hash string, degraded, tokens int) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `
		SELECT c.header, c.content, c.content_hash, c.header_degraded, c.token_count
		FROM chunks c JOIN symbols s ON s.id = c.symbol_id
		WHERE s.name = $1`, name).Scan(&header, &content, &hash, &degraded, &tokens); err != nil {
		t.Fatalf("read header of %s: %v", name, err)
	}
	return header, content, hash, degraded, tokens
}

// chunkIDOf returns the part-0 chunk id of one symbol.
func chunkIDOf(t *testing.T, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	return scalarInt(t, pool, `
		SELECT c.id FROM chunks c JOIN symbols s ON s.id = c.symbol_id
		WHERE s.name = $1 AND c.part_idx = 0`, name)
}

// chunkHashes returns every chunk id mapped to its stored content hash.
func chunkHashes(t *testing.T, pool *pgxpool.Pool) map[int64]string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT id, content_hash FROM chunks`)
	if err != nil {
		t.Fatalf("query chunk hashes: %v", err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var hash string
		if err := rows.Scan(&id, &hash); err != nil {
			t.Fatalf("scan chunk hash: %v", err)
		}
		out[id] = hash
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("chunk hash rows: %v", err)
	}
	return out
}

// printedCount reads one "<label>: %d" line from a run's output.
func printedCount(t *testing.T, out, label string) int {
	t.Helper()
	n := -1
	for _, line := range strings.Split(out, "\n") {
		_, _ = fmt.Sscanf(line, label+": %d", &n)
	}
	if n < 0 {
		t.Fatalf("output missing %s:\n%s", label, out)
	}
	return n
}

// TestChunkHeaderContentAndHash proves the stored header is the five-line
// format, that it carries the ranked callees and the caller, that content_hash
// is ContentHash(header, content), and that every chunk of the filtered repo
// (all fixture files are out of slice) has a header.
func TestChunkHeaderContentAndHash(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})

	header, content, hash, degraded, _ := storedHeader(t, pool, "FanOut")
	want := strings.Join([]string{
		"// package: k8s.io/kubernetes/extra",
		"// file: extra/extra.go",
		"// signature: func FanOut()",
		"// calls: alpha, bravo, charlie, delta, echo",
		"// called by: Entry",
	}, "\n")
	if header != want {
		t.Errorf("FanOut header\n got %q\nwant %q", header, want)
	}
	if degraded != 0 {
		t.Errorf("FanOut header_degraded = %d, want 0", degraded)
	}
	if want := chunk.ContentHash(header, content); hash != want {
		t.Errorf("FanOut content_hash = %s, want ContentHash(header, content) = %s", hash, want)
	}

	if n := scalarInt(t, pool, `SELECT count(*) FROM files WHERE in_slice`); n != 0 {
		t.Fatalf("fixture has %d in-slice files, want 0", n)
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM chunks WHERE header = ''`); n != 0 {
		t.Errorf("chunks with an empty header = %d, want 0 (headers must cover the whole repo)", n)
	}
}

// TestChunkHeaderEdgeChangeRecomputes proves the header pass runs on every run:
// deleting one tree-sitter edge changes content_hash for exactly the caller's
// and the callee's chunks, with no file re-split, and a further rerun is a no-op.
func TestChunkHeaderEdgeChangeRecomputes(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})
	entryID, fanOutID := chunkIDOf(t, pool, "Entry"), chunkIDOf(t, pool, "FanOut")
	before := chunkHashes(t, pool)

	if _, err := pool.Exec(context.Background(), `
		DELETE FROM edges e USING symbols caller, symbols callee
		WHERE e.caller_id = caller.id AND e.callee_id = callee.id
		  AND e.source = 'treesitter' AND caller.name = 'Entry' AND callee.name = 'FanOut'`); err != nil {
		t.Fatalf("delete edge: %v", err)
	}
	out := runChunkStage(t, pool, root, chunkOptions{})
	if !strings.Contains(out, "0 files re-split, 0 chunks changed") {
		t.Errorf("edge-only change should not re-split files:\n%s", out)
	}

	after := chunkHashes(t, pool)
	var changed []int64
	for id, hash := range after {
		if before[id] != hash {
			changed = append(changed, id)
		}
	}
	if len(changed) != 2 {
		t.Fatalf("chunks with a changed content_hash = %d (%v), want exactly 2", len(changed), changed)
	}
	got := map[int64]bool{changed[0]: true, changed[1]: true}
	if !got[entryID] || !got[fanOutID] {
		t.Errorf("changed chunks = %v, want the caller %d and the callee %d", changed, entryID, fanOutID)
	}

	// A further rerun changes nothing.
	settled := runChunkStage(t, pool, root, chunkOptions{})
	if !strings.Contains(settled, "0 files re-split, 0 chunks changed") {
		t.Errorf("rerun summary not settled:\n%s", settled)
	}
	for id, hash := range chunkHashes(t, pool) {
		if after[id] != hash {
			t.Errorf("chunk %d changed on a no-op rerun", id)
		}
	}
}

// TestChunkHeaderCountsAndCeilings proves the printed header_degraded and
// oversized_failed counts equal the table counts, and that the fixture covers a
// chunk that degrades near the ceiling and one that fails above it.
func TestChunkHeaderCountsAndCeilings(t *testing.T) {
	pool, root := chunkFixture(t)
	out := runChunkStage(t, pool, root, chunkOptions{})

	dbDegraded := scalarInt(t, pool, `SELECT count(*) FROM chunks WHERE header_degraded > 0`)
	if printed := printedCount(t, out, "header_degraded"); int64(printed) != dbDegraded {
		t.Errorf("printed header_degraded = %d, DB count = %d", printed, dbDegraded)
	}
	dbFailed := scalarInt(t, pool, `SELECT count(*) FROM chunks WHERE token_count > $1`, chunk.EmbeddableContentMax)
	if printed := printedCount(t, out, "oversized_failed"); int64(printed) != dbFailed {
		t.Errorf("printed oversized_failed = %d, DB count = %d", printed, dbFailed)
	}
	if dbDegraded == 0 {
		t.Errorf("fixture has no degraded header; the test proves nothing")
	}
	if dbFailed == 0 {
		t.Errorf("fixture has no oversized_failed chunk; the test proves nothing")
	}

	header, _, _, degraded, tokens := storedHeader(t, pool, "nearCeiling")
	if degraded == 0 {
		t.Errorf("nearCeiling header_degraded = 0, want > 0")
	}
	if tokens <= chunk.ChunkMaxTokens || tokens > chunk.EmbeddableContentMax {
		t.Errorf("nearCeiling tokens = %d, want in (%d, %d]", tokens, chunk.ChunkMaxTokens, chunk.EmbeddableContentMax)
	}
	// nearCeiling has no callees or callers, so its only dropped item is the
	// signature line: this proves the signature-drop stage end-to-end (D-082).
	if degraded != 1 {
		t.Errorf("nearCeiling header_degraded = %d, want 1 (the signature line only)", degraded)
	}
	if strings.Contains(header, "// signature:") {
		t.Errorf("nearCeiling header still has a signature line:\n%s", header)
	}

	_, _, _, _, overTokens := storedHeader(t, pool, "overBudget")
	if overTokens <= chunk.EmbeddableContentMax {
		t.Errorf("overBudget tokens = %d, want > %d", overTokens, chunk.EmbeddableContentMax)
	}
}

// TestRecomputeHeadersChangedCount proves the header pass reports the rows it
// changes: an edge deletion changes exactly the caller's and the callee's
// chunk, and a second pass changes nothing.
func TestRecomputeHeadersChangedCount(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})

	if _, err := pool.Exec(context.Background(), `
		DELETE FROM edges e USING symbols caller, symbols callee
		WHERE e.caller_id = caller.id AND e.callee_id = callee.id
		  AND e.source = 'treesitter' AND caller.name = 'Entry' AND callee.name = 'FanOut'`); err != nil {
		t.Fatalf("delete edge: %v", err)
	}
	changed, err := recomputeHeaders(context.Background(), pool)
	if err != nil {
		t.Fatalf("recomputeHeaders: %v", err)
	}
	if changed != 2 {
		t.Errorf("recomputeHeaders changed = %d, want exactly 2", changed)
	}
	again, err := recomputeHeaders(context.Background(), pool)
	if err != nil {
		t.Fatalf("recomputeHeaders (again): %v", err)
	}
	if again != 0 {
		t.Errorf("second recomputeHeaders changed = %d, want 0", again)
	}
}

// TestRankedCallsByteOrder proves the SQL window ranks names in byte order,
// exactly like the Go comparator, so the top-five it selects is the same five
// TopNames would. Names differing only by case sort differently under a
// locale-aware database collation, so this guards the COLLATE "C" ordering.
func TestRankedCallsByteOrder(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	var fileID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO files (path, package, sha, loc) VALUES ('pkg/tricky.go', 'pkg', 'sha', 1)
		RETURNING id`).Scan(&fileID); err != nil {
		t.Fatalf("insert file: %v", err)
	}
	insertSymbol := func(name string) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO symbols (file_id, kind, name, qualified_name, start_line, end_line)
			VALUES ($1, 'func', $2, $3, 1, 1)
			RETURNING id`, fileID, name, "pkg."+name).Scan(&id); err != nil {
			t.Fatalf("insert symbol %s: %v", name, err)
		}
		return id
	}
	caller := insertSymbol("Caller")
	names := []string{"Zed", "apple", "Banana", "cherry", "date", "elder", "fig"}
	refs := make([]chunk.CallRef, 0, len(names))
	for i, name := range names {
		callee := insertSymbol(name)
		refs = append(refs, chunk.CallRef{
			Name: name, QualifiedName: "pkg." + name, ID: callee, Resolution: chunk.ResolutionExact,
		})
		if _, err := pool.Exec(ctx, `
			INSERT INTO edges (caller_id, callee_id, callee_raw, resolution, source, call_line, call_kind)
			VALUES ($1, $2, $3, 'exact', 'treesitter', $4, 'func')`,
			caller, callee, name, i+1); err != nil {
			t.Fatalf("insert edge to %s: %v", name, err)
		}
	}

	callees, _, err := rankedCalls(ctx, pool)
	if err != nil {
		t.Fatalf("rankedCalls: %v", err)
	}
	var collate string
	if err := pool.QueryRow(ctx, `SELECT datcollate FROM pg_database WHERE datname = current_database()`).Scan(&collate); err == nil {
		t.Logf("database collation: %s", collate)
	}
	got := callees[caller]
	want := chunk.TopNames(refs)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("rankedCalls top-5 = %v, want Go TopNames = %v", got, want)
	}
	// Byte order (uppercase before lowercase) must decide the five.
	if byteOrder := []string{"Banana", "Zed", "apple", "cherry", "date"}; strings.Join(got, ",") != strings.Join(byteOrder, ",") {
		t.Errorf("rankedCalls byte order = %v, want %v", got, byteOrder)
	}
}

// TestChunkStageChunkTotalsSplitBySlice proves the printed chunk totals (all,
// in slice and outside) equal independent SQL counts over the chunks table.
func TestChunkStageChunkTotalsSplitBySlice(t *testing.T) {
	pool, root := chunkFixture(t)
	out := runChunkStage(t, pool, root, chunkOptions{})

	var wantTotal, wantInSlice int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*), count(*) FILTER (WHERE f.in_slice)
		FROM chunks c JOIN files f ON f.id = c.file_id`).Scan(&wantTotal, &wantInSlice); err != nil {
		t.Fatalf("count chunk totals: %v", err)
	}
	total, inSlice, outside := printedChunkTotals(t, out)
	if total != wantTotal || inSlice != wantInSlice || outside != wantTotal-wantInSlice {
		t.Errorf("printed chunks: %d (%d in slice, %d outside), want %d (%d, %d)",
			total, inSlice, outside, wantTotal, wantInSlice, wantTotal-wantInSlice)
	}
	if total == 0 {
		t.Fatalf("fixture has no chunks; the test proves nothing")
	}
}

// TestChunkStageTokenPercentiles proves the printed token p50/p99/max equal the
// nearest-rank percentiles computed independently from the chunks table.
func TestChunkStageTokenPercentiles(t *testing.T) {
	pool, root := chunkFixture(t)
	out := runChunkStage(t, pool, root, chunkOptions{})

	wantP50, wantP99, wantMax := dbTokenPercentiles(t, pool)
	p50, p99, maxTokens := printedTokenPercentiles(t, out)
	if p50 != wantP50 || p99 != wantP99 || maxTokens != wantMax {
		t.Errorf("printed tokens p50/p99/max: %d/%d/%d, want %d/%d/%d",
			p50, p99, maxTokens, wantP50, wantP99, wantMax)
	}
	if maxTokens == 0 {
		t.Fatalf("fixture has no chunks; the test proves nothing")
	}
}

// TestChunkStageMarkerAndBlockCommentCounts proves the printed marker-line and
// block-comment-above counts equal an independent recomputation from the stored
// content and the source tree.
func TestChunkStageMarkerAndBlockCommentCounts(t *testing.T) {
	pool, root := chunkFixture(t)
	out := runChunkStage(t, pool, root, chunkOptions{})

	rows, err := pool.Query(context.Background(), `
		SELECT c.content, c.start_line, f.path
		FROM chunks c JOIN files f ON f.id = c.file_id`)
	if err != nil {
		t.Fatalf("query chunks: %v", err)
	}
	defer rows.Close()
	wantMarker, wantBlock := 0, 0
	for rows.Next() {
		var content, path string
		var start int
		if err := rows.Scan(&content, &start, &path); err != nil {
			t.Fatalf("scan chunk: %v", err)
		}
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "// +") || strings.HasPrefix(trimmed, "//go:") {
				wantMarker++
				break
			}
		}
		lines := fileLines(t, root, path)
		if start >= 2 && start-1 <= len(lines) && strings.HasSuffix(strings.TrimSpace(lines[start-2]), "*/") {
			wantBlock++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("chunk rows: %v", err)
	}

	if got := printedCount(t, out, "chunks_with_marker_lines"); got != wantMarker {
		t.Errorf("printed chunks_with_marker_lines = %d, want %d", got, wantMarker)
	}
	if got := printedCount(t, out, "chunks_with_block_comment_above"); got != wantBlock {
		t.Errorf("printed chunks_with_block_comment_above = %d, want %d", got, wantBlock)
	}
	if wantMarker == 0 || wantBlock == 0 {
		t.Errorf("fixture covers %d marker chunks and %d block-comment-above chunks; the test proves nothing",
			wantMarker, wantBlock)
	}

	// Hand-derived against basic/basic.go, read off the source rather than the
	// production rule, so a definitional bug in the rule (or its helper) is
	// caught: exactly two chunks carry a marker/directive line — MarkerDoc
	// (line 15, "// +k8s:deepcopy-gen=true") and NoInline (line 23,
	// "//go:noinline") — and exactly one chunk, BlockCommented (line 13),
	// follows a /* */ block comment on the immediately preceding line.
	if line12 := fileLines(t, root, "basic/basic.go")[11]; !strings.HasSuffix(strings.TrimSpace(line12), "*/") {
		t.Fatalf("fixture changed: basic/basic.go line 12 is not the block comment: %q", line12)
	}
	basicRows, err := pool.Query(context.Background(), `
		SELECT s.name, c.content, c.start_line
		FROM chunks c JOIN symbols s ON s.id = c.symbol_id
		JOIN files f ON f.id = c.file_id
		WHERE f.path = 'basic/basic.go'`)
	if err != nil {
		t.Fatalf("query basic.go chunks: %v", err)
	}
	defer basicRows.Close()
	basicMarker, basicBlock := 0, 0
	for basicRows.Next() {
		var name, content string
		var start int
		if err := basicRows.Scan(&name, &content, &start); err != nil {
			t.Fatalf("scan basic.go chunk: %v", err)
		}
		if strings.Contains(content, "// +k8s:deepcopy-gen=true") || strings.Contains(content, "//go:noinline") {
			basicMarker++
		}
		if start == 13 {
			basicBlock++
			if name != "BlockCommented" {
				t.Errorf("basic/basic.go chunk at line 13 = %s, want BlockCommented", name)
			}
		}
	}
	if err := basicRows.Err(); err != nil {
		t.Fatalf("basic.go rows: %v", err)
	}
	if basicMarker != 2 {
		t.Errorf("basic/basic.go chunks with a hand-listed marker line = %d, want 2 (MarkerDoc and NoInline)", basicMarker)
	}
	if basicBlock != 1 {
		t.Errorf("basic/basic.go chunks starting at line 13 (BlockCommented) = %d, want 1", basicBlock)
	}
}

// TestChunkStageZeroChunkFigures proves the three zero-chunk figures are
// measured from the tables and the source tree only, equal an independent
// recomputation, split the new fixtures correctly (one with declarations, one
// without), and carry a fraction of the real slice-file denominator.
func TestChunkStageZeroChunkFigures(t *testing.T) {
	pool, root := chunkFixture(t)
	ctx := context.Background()

	// The whole fixture tree is out of slice; mark only the two zero-chunk
	// fixtures in slice, as the D-080 measurement is slice-only.
	if _, err := pool.Exec(ctx, `
		UPDATE files SET in_slice = true
		WHERE path IN ('zerofiles/decls.go', 'zerofiles/docs.go')`); err != nil {
		t.Fatalf("mark slice files: %v", err)
	}
	out := runChunkStage(t, pool, root, chunkOptions{})

	sliceFiles := scalarInt(t, pool, `SELECT count(*) FROM files WHERE in_slice`)
	rows, err := pool.Query(ctx, `
		SELECT f.path FROM files f
		WHERE f.in_slice AND NOT EXISTS (SELECT 1 FROM chunks c WHERE c.file_id = f.id)
		ORDER BY f.path`)
	if err != nil {
		t.Fatalf("query zero-chunk files: %v", err)
	}
	defer rows.Close()
	wantTotal, wantWith, wantNo := 0, 0, 0
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			t.Fatalf("scan zero-chunk file: %v", err)
		}
		wantTotal++
		lines := fileLines(t, root, path)
		if len(parse.TopLevelDecls([]byte(strings.Join(lines, "\n")))) > 0 {
			wantWith++
		} else {
			wantNo++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("zero-chunk rows: %v", err)
	}
	if wantWith != 0 || wantNo != 1 {
		t.Fatalf("fixture zero-chunk split = %d with decls, %d without, want 0 and 1", wantWith, wantNo)
	}

	for label, want := range map[string]int{
		"zero_chunk_files_total":      wantTotal,
		"zero_chunk_files_with_decls": wantWith,
		"zero_chunk_files_no_decls":   wantNo,
	} {
		if got := printedCount(t, out, label); got != want {
			t.Errorf("printed %s = %d, want %d", label, got, want)
		}
	}
	wantLine := fmt.Sprintf("zero_chunk_files_total: %d (%.2f%% of %d slice files)",
		wantTotal, 100*float64(wantTotal)/float64(sliceFiles), sliceFiles)
	if !strings.Contains(out, wantLine) {
		t.Errorf("output missing the real denominator line %q:\n%s", wantLine, out)
	}
}

// TestChunkStageZeroChunkNoSliceFiles proves the zero-chunk fractions print n/a
// when the fixture has no slice files, so the measurement still runs.
func TestChunkStageZeroChunkNoSliceFiles(t *testing.T) {
	pool, root := chunkFixture(t)
	out := runChunkStage(t, pool, root, chunkOptions{})

	if n := scalarInt(t, pool, `SELECT count(*) FROM files WHERE in_slice`); n != 0 {
		t.Fatalf("fixture has %d in-slice files, want 0", n)
	}
	if !strings.Contains(out, "zero_chunk_files_total: 0 (n/a, 0 slice files)") {
		t.Errorf("output missing the n/a zero-chunk line:\n%s", out)
	}
}

// printedChunkTotals reads the "chunks: total (in in slice, out outside)" line.
func printedChunkTotals(t *testing.T, out string) (total, inSlice, outside int) {
	t.Helper()
	ok := false
	for _, line := range strings.Split(out, "\n") {
		if _, err := fmt.Sscanf(line, "chunks: %d (%d in slice, %d outside)", &total, &inSlice, &outside); err == nil {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("output missing chunk totals:\n%s", out)
	}
	return total, inSlice, outside
}

// printedTokenPercentiles reads the "tokens p50/p99/max: a/b/c" line.
func printedTokenPercentiles(t *testing.T, out string) (p50, p99, maxTokens int) {
	t.Helper()
	ok := false
	for _, line := range strings.Split(out, "\n") {
		if _, err := fmt.Sscanf(line, "tokens p50/p99/max: %d/%d/%d", &p50, &p99, &maxTokens); err == nil {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("output missing token percentiles:\n%s", out)
	}
	return p50, p99, maxTokens
}

// dbTokenPercentiles computes the nearest-rank p50/p99 and max token counts
// independently from the chunks table.
func dbTokenPercentiles(t *testing.T, pool *pgxpool.Pool) (p50, p99, maxTokens int) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT token_count FROM chunks ORDER BY token_count`)
	if err != nil {
		t.Fatalf("query token counts: %v", err)
	}
	defer rows.Close()
	var tokens []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan token count: %v", err)
		}
		tokens = append(tokens, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("token count rows: %v", err)
	}
	nearest := func(p int) int {
		if len(tokens) == 0 {
			return 0
		}
		rank := (p*len(tokens) + 99) / 100
		if rank < 1 {
			rank = 1
		}
		if rank > len(tokens) {
			rank = len(tokens)
		}
		return tokens[rank-1]
	}
	if len(tokens) > 0 {
		maxTokens = tokens[len(tokens)-1]
	}
	return nearest(50), nearest(99), maxTokens
}

// chunkMetricSnapshot is the set of table-wide figures the chunk stage prints.
type chunkMetricSnapshot struct {
	total, inSlice, outside int
	p50, p99, max           int
	marker, block           int
	zeroTotal, zeroWith     int
	zeroNo                  int
}

// printedChunkMetricSnapshot reads every table-wide figure from a run's output.
func printedChunkMetricSnapshot(t *testing.T, out string) chunkMetricSnapshot {
	t.Helper()
	var s chunkMetricSnapshot
	s.total, s.inSlice, s.outside = printedChunkTotals(t, out)
	s.p50, s.p99, s.max = printedTokenPercentiles(t, out)
	s.marker = printedCount(t, out, "chunks_with_marker_lines")
	s.block = printedCount(t, out, "chunks_with_block_comment_above")
	s.zeroTotal = printedCount(t, out, "zero_chunk_files_total")
	s.zeroWith = printedCount(t, out, "zero_chunk_files_with_decls")
	s.zeroNo = printedCount(t, out, "zero_chunk_files_no_decls")
	return s
}

// dbChunkMetricSnapshot recomputes every printed table-wide figure independently
// from the chunks and files tables and the fixture source.
func dbChunkMetricSnapshot(t *testing.T, pool *pgxpool.Pool, root string) chunkMetricSnapshot {
	t.Helper()
	var s chunkMetricSnapshot
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*), count(*) FILTER (WHERE f.in_slice)
		FROM chunks c JOIN files f ON f.id = c.file_id`).Scan(&s.total, &s.inSlice); err != nil {
		t.Fatalf("count chunk totals: %v", err)
	}
	s.outside = s.total - s.inSlice
	s.p50, s.p99, s.max = dbTokenPercentiles(t, pool)

	rows, err := pool.Query(context.Background(), `
		SELECT c.content, c.start_line, f.path
		FROM chunks c JOIN files f ON f.id = c.file_id`)
	if err != nil {
		t.Fatalf("query chunks: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var content, path string
		var start int
		if err := rows.Scan(&content, &start, &path); err != nil {
			t.Fatalf("scan chunk: %v", err)
		}
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "// +") || strings.HasPrefix(trimmed, "//go:") {
				s.marker++
				break
			}
		}
		lines := fileLines(t, root, path)
		if start >= 2 && start-1 <= len(lines) && strings.HasSuffix(strings.TrimSpace(lines[start-2]), "*/") {
			s.block++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("chunk rows: %v", err)
	}

	zeroRows, err := pool.Query(context.Background(), `
		SELECT f.path FROM files f
		WHERE f.in_slice AND NOT EXISTS (SELECT 1 FROM chunks c WHERE c.file_id = f.id)`)
	if err != nil {
		t.Fatalf("query zero-chunk files: %v", err)
	}
	defer zeroRows.Close()
	for zeroRows.Next() {
		var path string
		if err := zeroRows.Scan(&path); err != nil {
			t.Fatalf("scan zero-chunk file: %v", err)
		}
		s.zeroTotal++
		lines := fileLines(t, root, path)
		if len(parse.TopLevelDecls([]byte(strings.Join(lines, "\n")))) > 0 {
			s.zeroWith++
		} else {
			s.zeroNo++
		}
	}
	if err := zeroRows.Err(); err != nil {
		t.Fatalf("zero-chunk rows: %v", err)
	}
	return s
}

// TestChunkStageMetricsAreTableWide proves the whole-repo metrics (chunk totals,
// token percentiles, marker and block-comment counts, and the zero-chunk
// figures) are measured from the tables and the source tree, not from the files
// a run re-split: a --limit run and a full run over the same settled table print
// identical numbers, and both equal an independent recomputation. Modeled on
// TestChunkStageOversizedCountIsDBWide.
func TestChunkStageMetricsAreTableWide(t *testing.T) {
	pool, root := chunkFixture(t)
	ctx := context.Background()

	// The zero-chunk figures are slice-only; mark the two zero-chunk fixtures in
	// slice so those figures are nonzero and covered.
	if _, err := pool.Exec(ctx, `
		UPDATE files SET in_slice = true
		WHERE path IN ('zerofiles/decls.go', 'zerofiles/docs.go')`); err != nil {
		t.Fatalf("mark slice files: %v", err)
	}
	runChunkStage(t, pool, root, chunkOptions{}) // settle the table fully

	want := dbChunkMetricSnapshot(t, pool, root)

	// Force every file stale so the limited run genuinely re-splits one file:
	// the table content is unchanged, so the table-wide figures must not move.
	if _, err := pool.Exec(ctx, `UPDATE files SET chunked_sha = NULL`); err != nil {
		t.Fatalf("force files stale: %v", err)
	}
	limited := runChunkStage(t, pool, root, chunkOptions{Limit: 1})
	if !strings.Contains(limited, "1 files re-split") {
		t.Errorf("limited run should re-split exactly one file:\n%s", limited)
	}
	gotLimited := printedChunkMetricSnapshot(t, limited)
	if gotLimited != want {
		t.Errorf("limited run metrics = %+v, want table-wide %+v\n%s", gotLimited, want, limited)
	}

	full := runChunkStage(t, pool, root, chunkOptions{})
	gotFull := printedChunkMetricSnapshot(t, full)
	if gotFull != want {
		t.Errorf("full run metrics = %+v, want table-wide %+v\n%s", gotFull, want, full)
	}
	if gotLimited != gotFull {
		t.Errorf("limited run metrics %+v differ from full run %+v", gotLimited, gotFull)
	}

	// A true no-op run: the tree is fully settled, so nothing is re-split. A
	// run-scoped computation would have nothing to measure here, yet the
	// table-wide snapshot must not move.
	noop := runChunkStage(t, pool, root, chunkOptions{})
	if !strings.Contains(noop, "0 files re-split, 0 chunks changed") {
		t.Errorf("no-op run should report the settled summary:\n%s", noop)
	}
	if gotNoop := printedChunkMetricSnapshot(t, noop); gotNoop != want {
		t.Errorf("no-op run metrics = %+v, want table-wide %+v\n%s", gotNoop, want, noop)
	}
	if want.total == 0 || want.marker == 0 || want.block == 0 || want.zeroTotal == 0 {
		t.Fatalf("fixture metrics %+v are degenerate; the test proves nothing", want)
	}
}

// residualPartsOf returns the stored residual chunks of one file in part order,
// read through the synthetic owner symbol keyed by the file path.
func residualPartsOf(t *testing.T, pool *pgxpool.Pool, path string) []storedPart {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT c.part_idx, c.start_line, c.end_line, c.token_count, c.content
		FROM chunks c JOIN symbols s ON s.id = c.symbol_id
		WHERE s.qualified_name = $1
		ORDER BY c.part_idx`, declQualifiedName(path))
	if err != nil {
		t.Fatalf("query residual parts of %s: %v", path, err)
	}
	defer rows.Close()
	var out []storedPart
	for rows.Next() {
		var p storedPart
		if err := rows.Scan(&p.partIdx, &p.start, &p.end, &p.tokens, &p.content); err != nil {
			t.Fatalf("scan residual part: %v", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("residual part rows: %v", err)
	}
	return out
}

// restampFilesForCeiling rewrites every file's chunk marker as if the chunk
// ceiling were chunkMax, so the next run re-splits the whole tree (D-078).
func restampFilesForCeiling(t *testing.T, pool *pgxpool.Pool, chunkMax int) int {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `SELECT id, sha FROM files`)
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
		marker := chunk.ParamsSHA(f.sha, chunkMax, chunk.GroupMaxTokens, chunk.ChunkerVersion)
		if _, err := pool.Exec(ctx,
			`UPDATE files SET chunked_sha = $2 WHERE id = $1`, f.id, marker); err != nil {
			t.Fatalf("restamp file: %v", err)
		}
	}
	return len(files)
}

// TestChunkResidualDeclarations proves a const-only, var-only and type-alias
// file each get exactly one residual chunk whose content equals the source span
// from start_line to end_line, whose search_tsv finds the declared name, and
// whose header is built; and that a file with a function plus a const gets no
// residual chunk (its declarations stay uncovered).
func TestChunkResidualDeclarations(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})
	ctx := context.Background()

	for _, tc := range []struct{ path, query string }{
		{"residual/constonly.go", "maxretries"},
		{"residual/varonly.go", "backoff"},
		{"residual/typealias.go", "alias"},
	} {
		parts := residualPartsOf(t, pool, tc.path)
		if len(parts) != 1 || parts[0].partIdx != 0 {
			t.Fatalf("%s residual parts = %+v, want exactly one part 0", tc.path, parts)
		}
		lines := fileLines(t, root, tc.path)
		p := parts[0]
		if want := strings.Join(lines[p.start-1:p.end], "\n"); p.content != want {
			t.Errorf("%s content mismatch\ngot  %q\nwant %q", tc.path, p.content, want)
		}

		var header string
		var matches bool
		if err := pool.QueryRow(ctx, `
			SELECT c.header, c.search_tsv @@ to_tsquery('simple', $2)
			FROM chunks c JOIN symbols s ON s.id = c.symbol_id
			WHERE s.qualified_name = $1`,
			declQualifiedName(tc.path), tc.query).Scan(&header, &matches); err != nil {
			t.Fatalf("read residual header of %s: %v", tc.path, err)
		}
		if header == "" {
			t.Errorf("%s residual chunk has an empty header", tc.path)
		}
		if !matches {
			t.Errorf("%s residual search_tsv does not match %q", tc.path, tc.query)
		}

		var kind string
		if err := pool.QueryRow(ctx, `
			SELECT s.kind FROM chunks c JOIN symbols s ON s.id = c.symbol_id
			WHERE s.qualified_name = $1`, declQualifiedName(tc.path)).Scan(&kind); err != nil {
			t.Fatalf("read residual owner kind of %s: %v", tc.path, err)
		}
		if kind != parse.KindDecl {
			t.Errorf("%s residual owner kind = %q, want %q", tc.path, kind, parse.KindDecl)
		}
	}

	// A file with a real chunk never gets a residual chunk.
	if parts := residualPartsOf(t, pool, "residual/mixed.go"); len(parts) != 0 {
		t.Errorf("mixed.go has %d residual parts, want 0", len(parts))
	}
	if n := scalarInt(t, pool, `
		SELECT count(*) FROM chunks c JOIN symbols s ON s.id = c.symbol_id
		JOIN files f ON f.id = c.file_id
		WHERE f.path = 'residual/mixed.go' AND s.name = 'Mixed'`); n != 1 {
		t.Errorf("mixed.go Mixed chunks = %d, want 1", n)
	}
}

// TestChunkResidualZeroChunkWithDeclsDrops proves every decl-only slice file
// has a chunk after the run, so the printed zero_chunk_files_with_decls drops to
// 0 while the doc-only figure is untouched (D-080).
func TestChunkResidualZeroChunkWithDeclsDrops(t *testing.T) {
	pool, root := chunkFixture(t)
	if _, err := pool.Exec(context.Background(), `
		UPDATE files SET in_slice = true WHERE path IN (
			'zerofiles/decls.go', 'residual/constonly.go', 'residual/varonly.go',
			'residual/typealias.go', 'residual/manyconst.go', 'residual/mixed.go')`); err != nil {
		t.Fatalf("mark slice files: %v", err)
	}
	out := runChunkStage(t, pool, root, chunkOptions{})

	if got := printedCount(t, out, "zero_chunk_files_with_decls"); got != 0 {
		t.Errorf("zero_chunk_files_with_decls = %d, want 0 after the residual run\n%s", got, out)
	}
	if got := printedCount(t, out, "zero_chunk_files_total"); got != 0 {
		t.Errorf("zero_chunk_files_total = %d, want 0 (every marked file now has a chunk)\n%s", got, out)
	}
}

// TestChunkResidualIdempotent proves a rerun over an unchanged tree reports the
// settled summary and leaves every chunk id, the residual chunk included,
// unchanged.
func TestChunkResidualIdempotent(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})
	before := chunkSnapshot(t, pool)

	var residualBefore int64
	if err := pool.QueryRow(context.Background(), `
		SELECT c.id FROM chunks c JOIN symbols s ON s.id = c.symbol_id
		WHERE s.qualified_name = $1`, declQualifiedName("residual/constonly.go")).Scan(&residualBefore); err != nil {
		t.Fatalf("read residual chunk id: %v", err)
	}

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
	var residualAfter int64
	if err := pool.QueryRow(context.Background(), `
		SELECT c.id FROM chunks c JOIN symbols s ON s.id = c.symbol_id
		WHERE s.qualified_name = $1`, declQualifiedName("residual/constonly.go")).Scan(&residualAfter); err != nil {
		t.Fatalf("read residual chunk id after rerun: %v", err)
	}
	if residualAfter != residualBefore {
		t.Errorf("residual chunk id changed on rerun: before %d, after %d", residualBefore, residualAfter)
	}
}

// TestChunkResidualCeilingChangeResplitsIdempotently proves a ceiling change
// re-splits every file, re-derives the residual chunks and leaves every id
// unchanged, on the re-split run and on a further one.
func TestChunkResidualCeilingChangeResplitsIdempotently(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})
	before := chunkSnapshot(t, pool)

	// First restamp: every file is re-split, but the derived chunks are
	// identical, so nothing changes.
	n := restampFilesForCeiling(t, pool, 2000)
	out := runChunkStage(t, pool, root, chunkOptions{})
	if want := fmt.Sprintf("%d files re-split, 0 chunks changed", n); !strings.Contains(out, want) {
		t.Errorf("output missing %q:\n%s", want, out)
	}
	if after := chunkSnapshot(t, pool); after != before {
		t.Errorf("chunk ids changed when only the ceiling changed:\nbefore %s\nafter  %s", before, after)
	}

	// A second restamp and run is the same no-op.
	restampFilesForCeiling(t, pool, 2000)
	runChunkStage(t, pool, root, chunkOptions{})
	if after := chunkSnapshot(t, pool); after != before {
		t.Errorf("chunk ids changed on the second ceiling change:\nbefore %s\nafter  %s", before, after)
	}
}

// TestChunkResidualSplitParts proves a residual file over the ceiling is split
// at declaration boundaries: more than one part, every part marked and within
// the ceiling, every part starting at a whole declaration, and no declaration
// split.
func TestChunkResidualSplitParts(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})

	const path = "residual/manyconst.go"
	parts := residualPartsOf(t, pool, path)
	if len(parts) < 2 {
		t.Fatalf("%s residual parts = %d, want at least 2", path, len(parts))
	}
	lines := fileLines(t, root, path)
	decls := parse.TopLevelDecls([]byte(strings.Join(lines, "\n")))
	if len(decls) < 2 {
		t.Fatalf("%s top-level declarations = %d, want at least 2", path, len(decls))
	}
	starts := map[int]bool{}
	for _, d := range decls {
		starts[d.StartLine] = true
	}
	for i, p := range parts {
		if p.partIdx != i {
			t.Errorf("part_idx = %d, want %d", p.partIdx, i)
		}
		marker := fmt.Sprintf("// ... part %d/%d", i+1, len(parts))
		if !strings.HasPrefix(p.content, marker+"\n") {
			t.Errorf("part %d lost its marker:\n%q", i, p.content)
		}
		if p.tokens > chunk.ChunkMaxTokens {
			t.Errorf("part %d token_count = %d, want <= %d", i, p.tokens, chunk.ChunkMaxTokens)
		}
		if p.start < 1 || p.end > len(lines) || p.end < p.start {
			t.Errorf("part %d span %d..%d outside %d lines", i, p.start, p.end, len(lines))
			continue
		}
		if !strings.HasSuffix(p.content, strings.Join(lines[p.start-1:p.end], "\n")) {
			t.Errorf("part %d does not end with its covered source lines", i)
		}
		if !starts[p.start] {
			t.Errorf("part %d starts at line %d, not a declaration boundary", i, p.start)
		}
	}
}

// TestChunkResidualGainedFuncLosesResidual proves the "zero-chunk file only"
// rule holds across a change: a file that gains a function loses its residual
// chunk and its synthetic owner symbol, and the function chunks normally.
func TestChunkResidualGainedFuncLosesResidual(t *testing.T) {
	pool := testdb.New(t)
	root := t.TempDir()
	writeSrc(t, root, "pkg/a/a.go", "package a\n\nconst A = 1\n")

	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)
	runGraphStage(t, pool, root, graphOptions{})
	runChunkStage(t, pool, root, chunkOptions{})
	if parts := residualPartsOf(t, pool, "pkg/a/a.go"); len(parts) != 1 {
		t.Fatalf("before: residual parts = %d, want 1", len(parts))
	}

	// The file gains a function, forcing it stale through the pipeline.
	writeSrc(t, root, "pkg/a/a.go", "package a\n\nconst A = 1\n\nfunc F() int { return A }\n")
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)
	runGraphStage(t, pool, root, graphOptions{})
	runChunkStage(t, pool, root, chunkOptions{})

	if parts := residualPartsOf(t, pool, "pkg/a/a.go"); len(parts) != 0 {
		t.Errorf("after gaining a function: residual parts = %d, want 0", len(parts))
	}
	if n := scalarInt(t, pool, `
		SELECT count(*) FROM chunks c JOIN symbols s ON s.id = c.symbol_id
		WHERE s.name = 'F'`); n != 1 {
		t.Errorf("function F chunks = %d, want 1", n)
	}
	if n := scalarInt(t, pool, `
		SELECT count(*) FROM symbols s JOIN files f ON f.id = s.file_id
		WHERE f.path = 'pkg/a/a.go' AND s.kind = $1`, parse.KindDecl); n != 0 {
		t.Errorf("synthetic owner symbols left on the file = %d, want 0", n)
	}
}

// TestChunkResidualGolden pins the residual split shape of the const-only,
// var-only, type-alias and many-const fixtures. Regenerate manually with
// -update only.
func TestChunkResidualGolden(t *testing.T) {
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})

	got := map[string][]partGolden{}
	for _, path := range []string{
		"residual/constonly.go", "residual/manyconst.go", "residual/typealias.go", "residual/varonly.go",
	} {
		for _, p := range residualPartsOf(t, pool, path) {
			got[path] = append(got[path], partGolden{
				PartIdx: p.partIdx, StartLine: p.start, EndLine: p.end, TokenCount: p.tokens,
			})
		}
	}
	data, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}
	data = append(data, '\n')

	golden := filepath.Join("testdata", "chunk", "golden", "residual.json")
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
		t.Errorf("residual golden mismatch (regenerate with -update if intended):\n--- got ---\n%s\n--- want ---\n%s", data, want)
	}
}

// TestChunkResidualSurvivesReparse proves the parse stage's vanished-symbol
// sweep never deletes the synthetic parse.KindDecl owner the chunk stage
// created (D-080): a package re-parse (triggered by any sibling edit, which
// re-parses the whole package) leaves the decl symbol and its residual chunk in
// place, with an unchanged chunk id. Without the kind filter in deleteVanished
// the owner is swept, its chunk cascades away, and the chunk stage never
// restores it because the file's own sha and chunked_sha are unchanged.
func TestChunkResidualSurvivesReparse(t *testing.T) {
	pool := testdb.New(t)
	root := t.TempDir()
	ctx := context.Background()
	writeSrc(t, root, "pkg/a/a.go", "package a\n\nconst A = 1\n")
	writeSrc(t, root, "pkg/a/b.go", "package a\n\nfunc B() int { return 1 }\n")

	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)
	runGraphStage(t, pool, root, graphOptions{})
	runChunkStage(t, pool, root, chunkOptions{})

	var declID, chunkID int64
	if err := pool.QueryRow(ctx, `
		SELECT s.id FROM symbols s JOIN files f ON f.id = s.file_id
		WHERE f.path = 'pkg/a/a.go' AND s.kind = $1`, parse.KindDecl).Scan(&declID); err != nil {
		t.Fatalf("before: no residual owner: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT c.id FROM chunks c JOIN symbols s ON s.id = c.symbol_id
		WHERE s.qualified_name = $1`, declQualifiedName("pkg/a/a.go")).Scan(&chunkID); err != nil {
		t.Fatalf("before: no residual chunk: %v", err)
	}

	// Force a re-parse of the package, as an edit to any sibling file would.
	if _, err := pool.Exec(ctx,
		`UPDATE files SET parsed_sha = NULL WHERE path = 'pkg/a/a.go'`); err != nil {
		t.Fatalf("force re-parse: %v", err)
	}
	runParseCorpus(t, pool, root, 0)

	var declAfter, chunkAfter int64
	if err := pool.QueryRow(ctx, `
		SELECT s.id FROM symbols s JOIN files f ON f.id = s.file_id
		WHERE f.path = 'pkg/a/a.go' AND s.kind = $1`, parse.KindDecl).Scan(&declAfter); err != nil {
		t.Fatalf("after re-parse: residual owner was deleted: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT c.id FROM chunks c JOIN symbols s ON s.id = c.symbol_id
		WHERE s.qualified_name = $1`, declQualifiedName("pkg/a/a.go")).Scan(&chunkAfter); err != nil {
		t.Fatalf("after re-parse: residual chunk was deleted: %v", err)
	}
	if declAfter != declID || chunkAfter != chunkID {
		t.Errorf("ids changed across re-parse: decl %d->%d, chunk %d->%d",
			declID, declAfter, chunkID, chunkAfter)
	}
}

// TestGraphFreshnessIgnoresDeclSymbols proves the synthetic parse.KindDecl owner
// never enters the graph freshness fingerprint (D-076, D-080): inserting one
// does not change what graph.LoadSymbols returns or graph.Fingerprint computes,
// so a residual chunk cannot mark every file stale and re-graph the whole repo.
func TestGraphFreshnessIgnoresDeclSymbols(t *testing.T) {
	pool := testdb.New(t)
	root := t.TempDir()
	ctx := context.Background()
	writeSrc(t, root, "pkg/a/a.go", "package a\n\nconst A = 1\n")
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)

	names, err := graph.BuildNameIndex(ctx, pool, root)
	if err != nil {
		t.Fatalf("BuildNameIndex: %v", err)
	}
	before, err := graph.LoadSymbols(ctx, pool)
	if err != nil {
		t.Fatalf("LoadSymbols: %v", err)
	}
	beforeFP := graph.Fingerprint(before, names, graph.ResolverVersion)

	var fileID int64
	if err := pool.QueryRow(ctx,
		`SELECT id FROM files WHERE path = 'pkg/a/a.go'`).Scan(&fileID); err != nil {
		t.Fatalf("read file id: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO symbols (file_id, kind, name, qualified_name, signature, start_line, end_line)
		VALUES ($1, $2, 'a.go', '@decls:pkg/a/a.go', 'const A = 1', 3, 3)`,
		fileID, parse.KindDecl); err != nil {
		t.Fatalf("insert decl symbol: %v", err)
	}

	after, err := graph.LoadSymbols(ctx, pool)
	if err != nil {
		t.Fatalf("LoadSymbols after: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("LoadSymbols count %d -> %d, want unchanged (decl excluded)", len(before), len(after))
	}
	if afterFP := graph.Fingerprint(after, names, graph.ResolverVersion); afterFP != beforeFP {
		t.Errorf("fingerprint changed by a decl symbol:\nbefore %s\nafter  %s", beforeFP, afterFP)
	}
}
