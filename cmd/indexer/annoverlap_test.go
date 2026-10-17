package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/cli"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
)

// annTestDim is the registry dimension of qwen3-embedding:0.6b, the model the
// fixture vectors and the fake embedder are built at so the dimension
// cross-check in annOverlapStage passes.
const annTestDim = 1024

// annUnitVec returns a unit vector whose cosine similarity to [1,0,0,...] is s.
func annUnitVec(s float64) []float32 {
	v := make([]float32, annTestDim)
	v[0] = float32(s)
	if s < 1 {
		v[1] = float32(math.Sqrt(1 - s*s))
	}
	return v
}

// annFakeEmbedder returns one fixed vector for every query and counts the
// calls, so a test can prove the queries are embedded once.
type annFakeEmbedder struct {
	model string
	dim   int
	vec   []float32
	calls int
}

func (f *annFakeEmbedder) EmbedQueries(_ context.Context, queries []string) ([][]float32, error) {
	f.calls++
	out := make([][]float32, len(queries))
	for i := range out {
		out[i] = f.vec
	}
	return out, nil
}

func (f *annFakeEmbedder) EmbedDocuments(context.Context, []embed.Document) ([][]float32, error) {
	return nil, nil
}

func (f *annFakeEmbedder) Dim() int      { return f.dim }
func (f *annFakeEmbedder) Model() string { return f.model }

// TestOverlapAt pins the pure overlap computation: identical sets are a full
// overlap, disjoint sets are zero and a partial overlap is the shared count
// over k, for both the chunk (id) and file (path) levels.
func TestOverlapAt(t *testing.T) {
	chunks := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := overlapAt(chunks, chunks, 10); got != 1 {
		t.Errorf("identical chunks = %v, want 1", got)
	}
	disjoint := []int64{11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	if got := overlapAt(chunks, disjoint, 10); got != 0 {
		t.Errorf("disjoint chunks = %v, want 0", got)
	}
	partial := []int64{1, 2, 3, 4, 5, 6, 11, 12, 13, 14}
	if got := overlapAt(chunks, partial, 10); got != 0.6 {
		t.Errorf("partial chunks = %v, want 0.6", got)
	}

	files := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	if got := overlapAt(files, files, 10); got != 1 {
		t.Errorf("identical files = %v, want 1", got)
	}
	other := []string{"a", "b", "c", "d", "e", "f", "x", "y", "z", "w"}
	if got := overlapAt(files, other, 10); got != 0.6 {
		t.Errorf("partial files = %v, want 0.6", got)
	}
	if got := overlapAt(files, []string{"x", "y"}, 10); got != 0 {
		t.Errorf("disjoint files = %v, want 0", got)
	}
}

// annSentence builds an n-word sentence with a distinctive marker and a final
// period, so a test can control the word count of a doc's first sentence.
func annSentence(n int, marker string) string {
	words := make([]string, 0, n)
	words = append(words, marker)
	for i := 1; i < n; i++ {
		words = append(words, fmt.Sprintf("w%d", i))
	}
	return strings.Join(words, " ") + "."
}

// TestProbeQueriesSeededAndFiltered proves the probe builder draws in-slice
// function and method docs whose first sentence has at least 12 words, that it
// is deterministic under seed 5, and that it keeps short and out-of-slice docs
// out. Only 15 good function docs are seeded, so a probe of 30 that excluded
// methods could return at most 15; the draw must therefore include methods.
// TestFirstSentence pins the doc-comment first-sentence cut: it ends at the
// earliest of ". ", "! " and "? ", folds multiple // lines into one sentence,
// and skips a leading //go: directive.
func TestFirstSentence(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"// a. b", "a."},
		{"// a! b", "a!"},
		{"// a? b", "a?"},
		{"// go before. more", "go before."},
		{"//go:build x\n// doc. more", "doc."},
	} {
		if got := firstSentence(tc.in); got != tc.want {
			t.Errorf("firstSentence(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestProbeQueriesSeededAndFiltered(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	inFile := annInsertFile(t, pool, "in.go", true)
	outFile := annInsertFile(t, pool, "out.go", false)

	good := map[string]bool{}
	method := map[string]bool{}
	for i := 0; i < 15; i++ {
		s := annSentence(12+i%5, fmt.Sprintf("func%02d", i))
		annInsertDocSymbol(t, pool, inFile, parse.KindFunc, fmt.Sprintf("func%02d", i), s)
		good[s] = true
	}
	for i := 0; i < 40; i++ {
		s := annSentence(12+i%5, fmt.Sprintf("meth%02d", i))
		annInsertDocSymbol(t, pool, inFile, parse.KindMethod, fmt.Sprintf("meth%02d", i), s)
		good[s] = true
		method[s] = true
	}
	for i := 0; i < 10; i++ {
		annInsertDocSymbol(t, pool, inFile, parse.KindFunc, fmt.Sprintf("short%02d", i),
			annSentence(6, fmt.Sprintf("short%02d", i)))
	}
	for i := 0; i < 10; i++ {
		annInsertDocSymbol(t, pool, outFile, parse.KindMethod, fmt.Sprintf("outside%02d", i),
			annSentence(13, fmt.Sprintf("outside%02d", i)))
	}

	first, err := probeQueries(ctx, pool)
	if err != nil {
		t.Fatalf("probeQueries: %v", err)
	}
	second, err := probeQueries(ctx, pool)
	if err != nil {
		t.Fatalf("probeQueries second call: %v", err)
	}
	if !slices.Equal(first, second) {
		t.Fatalf("probe under seed 5 is not deterministic:\n%v\n%v", first, second)
	}
	if len(first) != 30 {
		t.Fatalf("probe size = %d, want 30 (the pool must include methods, or it is too small)", len(first))
	}
	sawMethod := false
	for _, q := range first {
		if !good[q] {
			t.Errorf("probe query %q is not an in-slice function or method doc with a >= 12-word first sentence", q)
		}
		if len(strings.Fields(q)) < 12 {
			t.Errorf("probe query %q has %d words, want at least 12", q, len(strings.Fields(q)))
		}
		if method[q] {
			sawMethod = true
		}
	}
	if !sawMethod {
		t.Errorf("probe drew no method docs: %v", first)
	}
}

// TestGateFailed pins the gate threshold: a chunk-level mean below 0.95 fails,
// 0.95 and above clears.
func TestGateFailed(t *testing.T) {
	for _, tc := range []struct {
		mean float64
		want bool
	}{
		{0.0, true},
		{0.9499, true},
		{0.95, false},
		{0.951, false},
		{1.0, false},
	} {
		if got := gateFailed(tc.mean); got != tc.want {
			t.Errorf("gateFailed(%v) = %v, want %v", tc.mean, got, tc.want)
		}
	}
}

// TestAnnOverlapStageSweepsEF seeds a 200-row corpus of well-separated in-slice
// vectors so the HNSW and exact top-10 agree, builds the real partial HNSW
// index, ANALYZEs and asserts (with the same disabling settings the tool uses)
// that the HNSW query plan is an Index Scan on that index - so the comparison
// cannot pass vacuously. It then runs the stage over two widths: it embeds the
// queries once, prints one row per width and returns nil (the gate passes).
func TestAnnOverlapStageSweepsEF(t *testing.T) {
	pool := testdb.New(t)
	const model = "qwen3-embedding:0.6b"
	const kind = "chunk_raw"
	ctx := context.Background()

	idx, err := planVindex(model, kind, 0)
	if err != nil {
		t.Fatalf("planVindex: %v", err)
	}

	const fileCount, chunkCount = 12, 200
	fileIDs := make([]int64, fileCount)
	for j := range fileIDs {
		fileIDs[j] = annInsertFile(t, pool, fmt.Sprintf("f%02d.go", j), true)
	}
	for i := 0; i < chunkCount; i++ {
		// Distinct cosine similarities to [1,0,...] keep the ranking total,
		// so HNSW and the brute-force sort must agree.
		vec := annUnitVec(0.99 - float64(i)*0.005)
		annInsertChunkVector(t, pool, fileIDs[i%fileCount], fmt.Sprintf("sym%03d", i), vec, model, kind)
	}
	if _, err := pool.Exec(ctx, idx.sql); err != nil {
		t.Fatalf("create index: %v", err)
	}
	if _, err := pool.Exec(ctx, "ANALYZE embeddings"); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if explain := annExplainForced(t, pool, annHitSQL(annTestDim, model, kind), annUnitVec(1)); !strings.Contains(explain, "Index Scan using "+idx.index) {
		t.Fatalf("HNSW query plan does not use %s (the comparison would be vacuous):\n%s", idx.index, explain)
	}

	emb := &annFakeEmbedder{model: model, dim: annTestDim, vec: annUnitVec(1)}
	plan := overlapPlan{model: model, kind: kind, dim: annTestDim, ef: []int{200, 400},
		queries: []string{"first query", "second query"}}

	var buf bytes.Buffer
	if err := annOverlapStage(ctx, pool, emb, plan, &buf); err != nil {
		t.Fatalf("annOverlapStage: %v", err)
	}
	if emb.calls != 1 {
		t.Errorf("EmbedQueries calls = %d, want 1", emb.calls)
	}

	rows := 0
	for _, line := range strings.Split(buf.String(), "\n") {
		if !strings.HasPrefix(line, "ef=") {
			continue
		}
		rows++
		for _, want := range []string{"chunk_mean=", "chunk_min=", "file_mean=", "file_min="} {
			if !strings.Contains(line, want) {
				t.Errorf("row %q missing %q", line, want)
			}
		}
	}
	if rows != 2 {
		t.Fatalf("rows = %d, want one per ef width (2):\n%s", rows, buf.String())
	}
	if !strings.Contains(buf.String(), "ef=200 chunk_mean=1.00 chunk_min=1.00") {
		t.Errorf("expected a full chunk-level overlap on the fixture:\n%s", buf.String())
	}
}

// TestAnnOverlapStageGatesBelowThreshold proves the exit-3 gate is wired: a
// corpus where the HNSW and exact top-10 do not overlap (here no in-slice
// embeddings match, so both sides return nothing and the chunk overlap is 0)
// must return an ExitError with code 3 and the message "tune ef_search", both
// for a single width and for a sweep where no width clears the gate.
func TestAnnOverlapStageGatesBelowThreshold(t *testing.T) {
	const model = "qwen3-embedding:0.6b"
	const kind = "chunk_raw"
	for _, tc := range []struct {
		name string
		ef   []int
	}{
		{"single", []int{200}},
		{"sweep", []int{200, 400}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := testdb.New(t)
			emb := &annFakeEmbedder{model: model, dim: annTestDim, vec: annUnitVec(1)}
			plan := overlapPlan{model: model, kind: kind, dim: annTestDim, ef: tc.ef,
				queries: []string{"q"}}

			var buf bytes.Buffer
			err := annOverlapStage(context.Background(), pool, emb, plan, &buf)
			var exit *cli.ExitError
			if !errors.As(err, &exit) {
				t.Fatalf("annOverlapStage = %v, want *cli.ExitError", err)
			}
			if exit.Code != 3 {
				t.Errorf("exit code = %d, want 3", exit.Code)
			}
			if exit.Err.Error() != "tune ef_search" {
				t.Errorf("exit message = %q, want %q", exit.Err.Error(), "tune ef_search")
			}
			if !strings.Contains(buf.String(), "chunk_mean=0.00") {
				t.Errorf("gate-failing row missing:\n%s", buf.String())
			}
		})
	}
}

// annExplainForced returns the EXPLAIN plan of sql with sequential scans and
// sorting disabled, the same settings the HNSW side of annHits applies, so a
// test can prove the plan walks the HNSW index rather than seq-scanning.
func annExplainForced(t *testing.T, pool *pgxpool.Pool, sql string, vec []float32) string {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin explain: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, setting := range []string{"SET LOCAL enable_seqscan = off", "SET LOCAL enable_sort = off"} {
		if _, err := tx.Exec(ctx, setting); err != nil {
			t.Fatalf("%s: %v", setting, err)
		}
	}
	rows, err := tx.Query(ctx, "EXPLAIN "+sql, pgvector.NewVector(vec))
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan explain: %v", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("explain rows: %v", err)
	}
	return strings.Join(lines, "\n")
}

// TestAnnOverlapRefusesInvalidModelOrKind proves an unknown model, an unknown
// kind or an out-of-range width is refused before any SQL or embedder call:
// the pool's database is reachable, so a regression that validated after
// opening it (or after reaching Ollama) would fail differently.
func TestAnnOverlapRefusesInvalidModelOrKind(t *testing.T) {
	pool := testdb.New(t)
	t.Setenv("DATABASE_URL", pool.Config().ConnConfig.ConnString())

	if err := runAnnOverlap(context.Background(), []string{"--model", "not-a-model", "--kind", kindChunkRaw}); err == nil {
		t.Fatal("unknown model = nil error, want refusal")
	} else if !strings.Contains(err.Error(), "not-a-model") {
		t.Errorf("unknown-model error = %v, want it to name the value", err)
	}

	if err := runAnnOverlap(context.Background(), []string{"--model", "qwen3-embedding:0.6b", "--kind", "not_a_kind"}); err == nil {
		t.Fatal("unknown kind = nil error, want refusal")
	} else if !strings.Contains(err.Error(), "not_a_kind") {
		t.Errorf("unknown-kind error = %v, want it to name the value", err)
	}

	if err := runAnnOverlap(context.Background(), []string{"--model", "qwen3-embedding:0.6b", "--kind", kindChunkRaw, "--ef", "1001"}); err == nil {
		t.Fatal("out-of-range ef = nil error, want refusal")
	} else if !strings.Contains(err.Error(), "1001") {
		t.Errorf("out-of-range ef error = %v, want it to name the value", err)
	}
}

// annInsertFile inserts one file and returns its id.
func annInsertFile(t *testing.T, pool *pgxpool.Pool, path string, inSlice bool) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO files (path, package, sha, loc, in_slice) VALUES ($1, 'pkg', 'sha', 1, $2) RETURNING id`,
		path, inSlice).Scan(&id); err != nil {
		t.Fatalf("insert file %s: %v", path, err)
	}
	return id
}

// annInsertDocSymbol inserts a symbol of the given kind carrying doc.
func annInsertDocSymbol(t *testing.T, pool *pgxpool.Pool, fileID int64, kind, name, sentence string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO symbols (file_id, kind, name, qualified_name, start_line, end_line, doc)
		 VALUES ($1, $2, $3, $4, 1, 2, $5)`,
		fileID, kind, name, "q:"+name, "// "+sentence); err != nil {
		t.Fatalf("insert doc symbol %s: %v", name, err)
	}
}

// annInsertChunkVector inserts a symbol, its single chunk and the chunk's
// embedding.
func annInsertChunkVector(t *testing.T, pool *pgxpool.Pool, fileID int64, name string, vec []float32, model, kind string) {
	t.Helper()
	ctx := context.Background()
	var symID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO symbols (file_id, kind, name, qualified_name, start_line, end_line)
		 VALUES ($1, 'func', $2, $3, 1, 2) RETURNING id`,
		fileID, name, "q:"+name).Scan(&symID); err != nil {
		t.Fatalf("insert symbol %s: %v", name, err)
	}
	var chunkID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO chunks (symbol_id, file_id, part_idx, content, header, content_hash, token_count, start_line, end_line)
		 VALUES ($1, $2, 0, $3, '', 'h', 1, 1, 2) RETURNING id`,
		symID, fileID, "content "+name).Scan(&chunkID); err != nil {
		t.Fatalf("insert chunk %s: %v", name, err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO embeddings (target_kind, target_id, model, vec) VALUES ($1, $2, $3, $4)`,
		kind, chunkID, model, pgvector.NewVector(vec)); err != nil {
		t.Fatalf("insert embedding %s: %v", name, err)
	}
}
