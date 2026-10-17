package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/cli"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/retrieve"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
)

// fakeSearcher is a retrieve.Retriever that returns a fixed ResultSet, so the
// output formatting can be tested without a database.
type fakeSearcher struct {
	rs  retrieve.ResultSet
	err error
}

func (f fakeSearcher) Retrieve(context.Context, retrieve.Query) (retrieve.ResultSet, error) {
	return f.rs, f.err
}

var _ retrieve.Retriever = fakeSearcher{}

// TestSearchFormatsResults pins the tab-separated line: rank from one, the
// score to four decimals, the path, the best evidence chunk's start-end span
// and the owning symbol's signature.
func TestSearchFormatsResults(t *testing.T) {
	rs, err := fakeSearcher{rs: retrieve.ResultSet{Results: []retrieve.Result{
		{File: "pkg/a.go", Score: 0.1234, Source: retrieve.FlatRaw, Evidence: []retrieve.Evidence{
			{ChunkID: 11, File: "pkg/a.go", StartLine: 3, EndLine: 9, Score: 0.1234},
			{ChunkID: 12, File: "pkg/a.go", StartLine: 20, EndLine: 22, Score: 0.01},
		}},
		{File: "pkg/b.go", Score: 0.5, Source: retrieve.FlatRaw, Evidence: []retrieve.Evidence{
			{ChunkID: 22, File: "pkg/b.go", StartLine: 4, EndLine: 8, Score: 0.5},
		}},
	}}}.Retrieve(context.Background(), retrieve.Query{Text: "q"})
	if err != nil {
		t.Fatalf("fake Retrieve: %v", err)
	}

	sigs := map[int64]string{11: "func Alpha()", 22: "type Beta struct{}"}
	lookup := func(_ context.Context, id int64) (string, error) {
		s, ok := sigs[id]
		if !ok {
			return "", fmt.Errorf("no signature for chunk %d", id)
		}
		return s, nil
	}

	var buf bytes.Buffer
	if err := printSearchResults(context.Background(), &buf, rs, lookup); err != nil {
		t.Fatalf("printSearchResults: %v", err)
	}
	want := "1\t0.1234\tpkg/a.go\t3-9\tfunc Alpha()\n" +
		"2\t0.5000\tpkg/b.go\t4-8\ttype Beta struct{}\n"
	if got := buf.String(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

// TestSearchFormatRejectsNoEvidence proves a result without an evidence chunk is
// an error rather than a blank line.
func TestSearchFormatRejectsNoEvidence(t *testing.T) {
	rs := retrieve.ResultSet{Results: []retrieve.Result{{File: "pkg/a.go", Score: 1}}}
	var buf bytes.Buffer
	err := printSearchResults(context.Background(), &buf, rs,
		func(context.Context, int64) (string, error) { return "", nil })
	if err == nil || !strings.Contains(err.Error(), "pkg/a.go") {
		t.Fatalf("err = %v, want a no-evidence error naming the file", err)
	}
	if buf.Len() != 0 {
		t.Errorf("printed %q, want nothing", buf.String())
	}
}

// TestRunSearchRejectsUnknownConfigAndArity proves the config and the single
// query argument are validated before any database or Ollama contact.
func TestRunSearchRejectsUnknownConfigAndArity(t *testing.T) {
	if err := runSearch(context.Background(), []string{"--config", "A2", "x"}); err == nil || !strings.Contains(err.Error(), "A2") {
		t.Errorf("unknown config err = %v, want it to name A2", err)
	}
	if err := runSearch(context.Background(), []string{"--config", "A0"}); err == nil || !strings.Contains(err.Error(), "query") {
		t.Errorf("missing query err = %v, want a query-argument error", err)
	}
}

const searchTestModel = "qwen3-embedding:0.6b"

// searchVec is the fixed unit vector the fake embedder answers with, seeded
// verbatim, so every stored chunk scores about 1.0 against the query.
func searchVec(dim int) []float32 {
	v := make([]float32, dim)
	f := float32(1 / math.Sqrt(float64(dim)))
	for i := range v {
		v[i] = f
	}
	return v
}

// seedSearchChunk inserts one in- or out-of-slice file, its symbol and one
// chunk, and returns the chunk id.
func seedSearchChunk(t *testing.T, pool *pgxpool.Pool, path, name string, inSlice bool) int64 {
	t.Helper()
	ctx := context.Background()
	var fileID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO files (path, package, sha, loc, in_slice) VALUES ($1, 'pkg', 'sha', 1, $2) RETURNING id`,
		path, inSlice).Scan(&fileID); err != nil {
		t.Fatalf("insert file %s: %v", path, err)
	}
	var symID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO symbols (file_id, kind, name, qualified_name, signature, start_line, end_line)
		 VALUES ($1, 'func', $2, $3, $4, 1, 2) RETURNING id`,
		fileID, name, "q:"+name, "func "+name+"()").Scan(&symID); err != nil {
		t.Fatalf("insert symbol %s: %v", name, err)
	}
	var chunkID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO chunks (symbol_id, file_id, part_idx, content, header, content_hash, token_count, start_line, end_line)
		 VALUES ($1, $2, 0, $3, $4, 'h', 1, 3, 9) RETURNING id`,
		symID, fileID, "func "+name+"() {}", "// header\nfunc "+name+"()").Scan(&chunkID); err != nil {
		t.Fatalf("insert chunk %s: %v", name, err)
	}
	return chunkID
}

// seedSearchEmbedding stores one vector for a chunk and the test model.
func seedSearchEmbedding(t *testing.T, pool *pgxpool.Pool, chunkID int64, kind string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO embeddings (target_kind, target_id, model, vec) VALUES ($1, $2, $3, $4)`,
		kind, chunkID, searchTestModel, pgvector.NewVector(searchVec(1024))); err != nil {
		t.Fatalf("insert embedding for chunk %d: %v", chunkID, err)
	}
}

// seedSearchFailure records an embed_failures row for a chunk and kind, hashing
// parts with the real embedder so a current-hash row genuinely matches what
// loadCandidates recomputes.
func seedSearchFailure(t *testing.T, pool *pgxpool.Pool, hasher inputHasher, chunkID int64, kind string, parts ...string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO embed_failures (target_kind, target_id, model, reason, input_hash) VALUES ($1, $2, $3, 'truncated', $4)`,
		kind, chunkID, searchTestModel, hasher.InputHash(parts...)); err != nil {
		t.Fatalf("insert failure for chunk %d: %v", chunkID, err)
	}
}

// deleteSearchEmbedding removes a chunk's stored vector for a kind.
func deleteSearchEmbedding(t *testing.T, pool *pgxpool.Pool, chunkID int64, kind string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM embeddings WHERE target_kind = $1 AND target_id = $2 AND model = $3`,
		kind, chunkID, searchTestModel); err != nil {
		t.Fatalf("delete embedding for chunk %d: %v", chunkID, err)
	}
}

// setSearchEnv points runSearch at the throwaway database and the fake embedder.
func setSearchEnv(t *testing.T, pool *pgxpool.Pool, f *embedFake) {
	t.Helper()
	t.Setenv("DATABASE_URL", pool.Config().ConnConfig.ConnString())
	t.Setenv("OLLAMA_URL", f.server.URL)
	t.Setenv("OLLAMA_EMBED_MODEL", searchTestModel)
}

// runSearchStdout drives the search command and returns stdout plus its error.
func runSearchStdout(t *testing.T, args ...string) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	runErr := runSearch(context.Background(), args)
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out), runErr
}

// TestSearchShortfallReportsHaveWant proves the guard: with two in-slice chunks
// and one stored vector for the config's kind, search exits 2 with
// "have 1, want 2" and runs no query.
func TestSearchShortfallReportsHaveWant(t *testing.T) {
	pool := testdb.New(t)
	first := seedSearchChunk(t, pool, "pkg/a.go", "Alpha", true)
	second := seedSearchChunk(t, pool, "pkg/b.go", "Beta", true)
	seedSearchEmbedding(t, pool, first, kindChunkHeader)
	seedSearchEmbedding(t, pool, second, kindChunkHeader)
	deleteSearchEmbedding(t, pool, second, kindChunkHeader)
	f := newEmbedFake(t, searchTestModel)
	setSearchEnv(t, pool, f)

	err := runSearch(context.Background(), []string{"--config", "A1", "x"})
	var exit *cli.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("err = %v, want *cli.ExitError", err)
	}
	if exit.Code != 2 {
		t.Errorf("exit code = %d, want 2", exit.Code)
	}
	if got, want := exit.Err.Error(), "have 1, want 2"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	if len(f.requests) != 0 {
		t.Errorf("the guard let %d embed requests through", len(f.requests))
	}
}

// TestSearchCoverageExcludesCurrentHashFailures proves a chunk whose
// embed_failures row still matches the chunk's current input hash is left out
// of want, so the guard passes (have == want) instead of exiting 2 (D-081).
func TestSearchCoverageExcludesCurrentHashFailures(t *testing.T) {
	pool := testdb.New(t)
	failed := seedSearchChunk(t, pool, "pkg/a.go", "Alpha", true)
	embedded := seedSearchChunk(t, pool, "pkg/b.go", "Beta", true)
	seedSearchEmbedding(t, pool, embedded, kindChunkHeader)
	f := newEmbedFake(t, searchTestModel)
	client := clientFor(t, f)
	setSearchEnv(t, pool, f)

	_, content, header := chunkRow(t, pool, "Alpha")
	seedSearchFailure(t, pool, client, failed, kindChunkHeader, header, content)

	out, err := runSearchStdout(t, "--config", "A1", "x")
	if err != nil {
		t.Fatalf("runSearch: %v\n%s", err, out)
	}
}

// TestSearchCoverageKeepsStaleFailures proves a chunk whose embed_failures row
// records a stale input hash is still wanted, so the guard sees want > have and
// exits 2 with "have N, want M" (D-081).
func TestSearchCoverageKeepsStaleFailures(t *testing.T) {
	pool := testdb.New(t)
	stale := seedSearchChunk(t, pool, "pkg/a.go", "Alpha", true)
	embedded := seedSearchChunk(t, pool, "pkg/b.go", "Beta", true)
	seedSearchEmbedding(t, pool, embedded, kindChunkHeader)
	f := newEmbedFake(t, searchTestModel)
	client := clientFor(t, f)
	setSearchEnv(t, pool, f)

	seedSearchFailure(t, pool, client, stale, kindChunkHeader, "stale-header", "stale-content")

	err := runSearch(context.Background(), []string{"--config", "A1", "x"})
	var exit *cli.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("err = %v, want *cli.ExitError", err)
	}
	if exit.Code != 2 {
		t.Errorf("exit code = %d, want 2", exit.Code)
	}
	if got, want := exit.Err.Error(), "have 1, want 2"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

// TestSearchStageDrivesRetrieverSeam proves searchStage routes through the
// injectable retriever: the fake answers with a fixed ResultSet, so the printed
// line comes from the seam and no query reaches the embed server.
func TestSearchStageDrivesRetrieverSeam(t *testing.T) {
	pool := testdb.New(t)
	a := seedSearchChunk(t, pool, "pkg/a.go", "Alpha", true)
	b := seedSearchChunk(t, pool, "pkg/b.go", "Beta", true)
	seedSearchEmbedding(t, pool, a, kindChunkHeader)
	seedSearchEmbedding(t, pool, b, kindChunkHeader)
	f := newEmbedFake(t, searchTestModel)
	client := clientFor(t, f)

	r := fakeSearcher{rs: retrieve.ResultSet{Results: []retrieve.Result{
		{File: "pkg/b.go", Score: 0.75, Source: retrieve.FlatHeader, Evidence: []retrieve.Evidence{
			{ChunkID: b, File: "pkg/b.go", StartLine: 3, EndLine: 9, Score: 0.75},
		}},
	}}}
	var buf bytes.Buffer
	err := searchStage(context.Background(), pool, client, r,
		searchOptions{Query: "q", Kind: kindChunkHeader, Limit: 5}, &buf)
	if err != nil {
		t.Fatalf("searchStage: %v", err)
	}
	want := "1\t0.7500\tpkg/b.go\t3-9\tfunc Beta()\n"
	if got := buf.String(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
	if len(f.requests) != 0 {
		t.Errorf("the fake retriever run still hit the embed server %d times", len(f.requests))
	}
}

// TestSearchNoVectorsForConfigExitsTwo proves a config with no stored vectors
// for its kind exits 2 with a "have 0, want N" message.
func TestSearchNoVectorsForConfigExitsTwo(t *testing.T) {
	pool := testdb.New(t)
	seedSearchChunk(t, pool, "pkg/a.go", "Alpha", true)
	seedSearchChunk(t, pool, "pkg/b.go", "Beta", true)
	f := newEmbedFake(t, searchTestModel)
	setSearchEnv(t, pool, f)

	err := runSearch(context.Background(), []string{"--config", "A0", "x"})
	var exit *cli.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("err = %v, want *cli.ExitError", err)
	}
	if exit.Code != 2 {
		t.Errorf("exit code = %d, want 2", exit.Code)
	}
	if got, want := exit.Err.Error(), "have 0, want 2"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

// TestSearchPrintsRankedRows proves a complete database prints one tab-separated
// line per ranked file: rank from one, a four-decimal score, the path, the
// start-end span and the owning symbol's signature.
func TestSearchPrintsRankedRows(t *testing.T) {
	pool := testdb.New(t)
	a := seedSearchChunk(t, pool, "pkg/a.go", "Alpha", true)
	b := seedSearchChunk(t, pool, "pkg/b.go", "Beta", true)
	seedSearchEmbedding(t, pool, a, kindChunkHeader)
	seedSearchEmbedding(t, pool, b, kindChunkHeader)
	f := newEmbedFake(t, searchTestModel)
	setSearchEnv(t, pool, f)

	out, err := runSearchStdout(t, "--config", "A1", "--limit", "2", "find alpha")
	if err != nil {
		t.Fatalf("runSearch: %v\n%s", err, out)
	}
	lines := nonBlankLines(out)
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2:\n%s", len(lines), out)
	}
	scoreRE := regexp.MustCompile(`^\d+\.\d{4}$`)
	spanRE := regexp.MustCompile(`^\d+-\d+$`)
	sigs := map[string]string{"pkg/a.go": "func Alpha()", "pkg/b.go": "func Beta()"}
	for i, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 5 {
			t.Fatalf("line %d = %q, want 5 tab-separated fields", i+1, line)
		}
		if fields[0] != strconv.Itoa(i+1) {
			t.Errorf("line %d rank = %q, want %d", i+1, fields[0], i+1)
		}
		if !scoreRE.MatchString(fields[1]) {
			t.Errorf("line %d score = %q, want four decimals", i+1, fields[1])
		}
		wantSig, ok := sigs[fields[2]]
		if !ok {
			t.Fatalf("line %d path = %q, not a seeded in-slice file", i+1, fields[2])
		}
		if !spanRE.MatchString(fields[3]) {
			t.Errorf("line %d span = %q, want start-end", i+1, fields[3])
		}
		if fields[4] != wantSig {
			t.Errorf("line %d signature = %q, want %q", i+1, fields[4], wantSig)
		}
	}
}

// nonBlankLines splits s into its non-empty lines.
func nonBlankLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}
