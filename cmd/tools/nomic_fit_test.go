package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/chunk"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/cli"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
)

// fitRequest is one request the fake server observed.
type fitRequest struct {
	Path      string
	Input     []string
	KeepAlive *int
}

// fitFake is a fake Ollama that serves /api/embed, /api/generate and
// /api/version. It records every request, can omit prompt_eval_count (the
// fallback case), can reject chosen inputs with a context-overflow 400, and can
// hard-fail chosen inputs with a 500, so nothing touches a real server.
type fitFake struct {
	t       *testing.T
	server  *httptest.Server
	mu      sync.Mutex
	reqs    []fitRequest
	countFn func(string) int
	omit    bool
	reject  map[string]bool
	fail    map[string]bool
	version string
}

func newFitFake(t *testing.T) *fitFake {
	t.Helper()
	f := &fitFake{
		t:       t,
		countFn: func(string) int { return 0 },
		reject:  map[string]bool{},
		fail:    map[string]bool{},
		version: "0.12.3-test",
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fitFake) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/version":
		_ = json.NewEncoder(w).Encode(map[string]string{"version": f.version})
		return
	case "/api/generate":
		var req struct {
			Model     string `json:"model"`
			KeepAlive *int   `json:"keep_alive"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			f.t.Errorf("decode generate request: %v", err)
		}
		f.record(fitRequest{Path: r.URL.Path, KeepAlive: req.KeepAlive})
		_ = json.NewEncoder(w).Encode(map[string]any{"done": true})
		return
	case "/api/embed":
		// handled below
	default:
		http.NotFound(w, r)
		return
	}

	var req struct {
		Model     string   `json:"model"`
		Input     []string `json:"input"`
		KeepAlive *int     `json:"keep_alive"`
		Truncate  *bool    `json:"truncate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.t.Errorf("decode embed request: %v", err)
	}
	f.record(fitRequest{Path: r.URL.Path, Input: append([]string(nil), req.Input...), KeepAlive: req.KeepAlive})

	for _, in := range req.Input {
		if f.fail[in] {
			http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			return
		}
	}
	rejected := false
	for _, in := range req.Input {
		if f.reject[in] {
			rejected = true
		}
	}
	if rejected {
		http.Error(w, `{"error":"input length exceeds the context length"}`, http.StatusBadRequest)
		return
	}
	out := make([][]float64, len(req.Input))
	for i := range req.Input {
		out[i] = make([]float64, 768)
	}
	resp := map[string]any{"embeddings": out}
	if !f.omit {
		total := 0
		for _, in := range req.Input {
			total += f.countFn(in)
		}
		resp["prompt_eval_count"] = total
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (f *fitFake) record(r fitRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, r)
}

// embedInputs returns every input sent to /api/embed, in order.
func (f *fitFake) embedInputs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.reqs {
		if r.Path == "/api/embed" {
			out = append(out, r.Input...)
		}
	}
	return out
}

// embedSizes returns the number of inputs on each /api/embed request.
func (f *fitFake) embedSizes() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []int
	for _, r := range f.reqs {
		if r.Path == "/api/embed" {
			out = append(out, len(r.Input))
		}
	}
	return out
}

// sawUnload reports whether a /api/generate request carried keep_alive 0.
func (f *fitFake) sawUnload() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.reqs {
		if r.Path == "/api/generate" && r.KeepAlive != nil && *r.KeepAlive == 0 {
			return true
		}
	}
	return false
}

func nomicClient(t *testing.T, f *fitFake) *embed.Ollama {
	t.Helper()
	c, err := embed.NewOllama(f.server.URL, nomicModel, 0)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// fitFixture writes n in-slice chunk_header documents into a throwaway database
// and returns their headers in chunk-id order. Each header embeds a tail of a
// controlled length so the estimates are known and varied.
func fitFixture(t *testing.T, n int) (*pgxpool.Pool, []string) {
	t.Helper()
	pool := testdb.New(t)
	fileID, symID := insertFitFile(t, pool, "fit/fit.go", true)
	var headers []string
	for i := 0; i < n; i++ {
		header := fmt.Sprintf("// package: fit\n// file: fit/fit.go\nfunc f%03d() {}\n%s",
			i, strings.Repeat("z", (i*37)%n))
		insertFitChunk(t, pool, fileID, symID, i, header)
		headers = append(headers, header)
	}
	return pool, headers
}

func insertFitFile(t *testing.T, pool *pgxpool.Pool, path string, inSlice bool) (fileID, symID int64) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `
		INSERT INTO files (path, package, sha, loc, in_slice)
		VALUES ($1, 'fit', 'sha', 1, $2) RETURNING id`, path, inSlice).Scan(&fileID); err != nil {
		t.Fatalf("insert file: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO symbols (file_id, kind, name, qualified_name, start_line, end_line)
		VALUES ($1, 'func', 'fit', $2, 1, 1) RETURNING id`, fileID, path).Scan(&symID); err != nil {
		t.Fatalf("insert symbol: %v", err)
	}
	return fileID, symID
}

func insertFitChunk(t *testing.T, pool *pgxpool.Pool, fileID, symID int64, part int, header string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO chunks (symbol_id, file_id, part_idx, content, header, content_hash, token_count, start_line, end_line)
		VALUES ($1, $2, $3, '', $4, 'h', 0, 1, 1)`, symID, fileID, part, header); err != nil {
		t.Fatalf("insert chunk: %v", err)
	}
}

func readFitResult(t *testing.T, path string) nomicFitResult {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	var res nomicFitResult
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return res
}

// synthCandidates builds n candidates whose estimates vary so the largest 200
// are a real subset.
func synthCandidates(n int) []fitCandidate {
	cands := make([]fitCandidate, n)
	for i := range cands {
		cands[i] = fitCandidate{ID: int64(i + 1), Estimate: 1000 + (i*37)%500, Rendered: fmt.Sprintf("d%d", i)}
	}
	return cands
}

// TestSelectFitSampleDeterministic proves the sample is the n largest by
// estimate plus n seeded-random documents, and is reproducible under the seed.
func TestSelectFitSampleDeterministic(t *testing.T) {
	cands := synthCandidates(500)

	first := selectFitSample(cands, 1, 200)
	if len(first) != 400 {
		t.Fatalf("sample size = %d, want 400", len(first))
	}

	// The largest half: sorted by estimate descending, ties by id ascending,
	// and equal to an independently computed top 200.
	want := append([]fitCandidate(nil), cands...)
	sort.SliceStable(want, func(i, j int) bool {
		if want[i].Estimate != want[j].Estimate {
			return want[i].Estimate > want[j].Estimate
		}
		return want[i].ID < want[j].ID
	})
	want = want[:200]
	for i := range want {
		if first[i].ID != want[i].ID {
			t.Fatalf("largest[%d] = %d, want %d", i, first[i].ID, want[i].ID)
		}
	}

	// Same seed => same ids.
	for i, c := range selectFitSample(cands, 1, 200) {
		if c.ID != first[i].ID {
			t.Fatalf("same seed differs at %d: %d vs %d", i, c.ID, first[i].ID)
		}
	}

	// Different seed: the largest half is unchanged, the random half differs.
	other := selectFitSample(cands, 2, 200)
	for i := 0; i < 200; i++ {
		if other[i].ID != first[i].ID {
			t.Fatalf("largest half changed with the seed at %d", i)
		}
	}
	same := true
	for i := 200; i < len(first); i++ {
		if other[i].ID != first[i].ID {
			same = false
			break
		}
	}
	if same {
		t.Fatal("different seed gave the same random half")
	}

	// n above the candidate count clamps both halves.
	small := selectFitSample(synthCandidates(70), 1, 200)
	if len(small) != 140 {
		t.Fatalf("small sample size = %d, want 140", len(small))
	}
}

// TestFitFailureRule pins the fixed failure rule: rejections fail, an
// over-limit ratio fails and computes the replacement budget, and a fitting
// ratio passes (D-078). Failure is the exact negation of the success condition.
func TestFitFailureRule(t *testing.T) {
	const budget, numCtx = 1638, 2048
	tests := []struct {
		name       string
		rejected   int
		ratioMax   float64
		wantFailed bool
		wantBudget int
	}{
		{name: "success", ratioMax: 1.0},
		{name: "at the boundary", ratioMax: 1.187},
		{name: "over limit", ratioMax: 2.0, wantFailed: true, wantBudget: 972},
		{name: "rejection", rejected: 1, ratioMax: 1.0, wantFailed: true, wantBudget: 1945},
		{name: "fallback rejection", rejected: 3, wantFailed: true, wantBudget: 0},
		{name: "fallback acceptance"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBudget, gotFailed := fitFailure(tt.rejected, tt.ratioMax, budget, numCtx)
			if gotFailed != tt.wantFailed || gotBudget != tt.wantBudget {
				t.Fatalf("fitFailure(%d,%v) = (%d,%v), want (%d,%v)",
					tt.rejected, tt.ratioMax, gotBudget, gotFailed, tt.wantBudget, tt.wantFailed)
			}
		})
	}
}

// TestLoadFitCandidates proves only in-slice documents are read, ordered by
// chunk id, with the estimate of the rendered request.
func TestLoadFitCandidates(t *testing.T) {
	pool, headers := fitFixture(t, 3)
	outFile, outSym := insertFitFile(t, pool, "fit/out.go", false)
	insertFitChunk(t, pool, outFile, outSym, 0, "// out of slice")

	cands, err := loadFitCandidates(context.Background(), pool, "search_document: ")
	if err != nil {
		t.Fatalf("loadFitCandidates: %v", err)
	}
	if len(cands) != len(headers) {
		t.Fatalf("candidates = %d, want %d (only in-slice)", len(cands), len(headers))
	}
	for i := range cands {
		want := "search_document: " + headers[i] + "\n"
		if cands[i].Rendered != want {
			t.Errorf("candidate %d rendered = %q, want %q", i, cands[i].Rendered, want)
		}
		if cands[i].Estimate != chunk.EstimateTokens(want) {
			t.Errorf("candidate %d estimate = %d, want %d", i, cands[i].Estimate, chunk.EstimateTokens(want))
		}
		if i > 0 && cands[i].ID <= cands[i-1].ID {
			t.Errorf("candidates not ordered by id: %d after %d", cands[i].ID, cands[i-1].ID)
		}
	}
}

// TestNomicFitRecordAndUnload runs a fitting sample and asserts the record
// fields, one document per request, that a pre-seeded embeddings row is left
// untouched, and the unload.
func TestNomicFitRecordAndUnload(t *testing.T) {
	ctx := context.Background()
	pool, headers := fitFixture(t, 5)

	// Pre-seed one valid embeddings row (a real chunk) so a run that wrote
	// nothing cannot pass by finding an empty table unchanged.
	var seededChunk int64
	if err := pool.QueryRow(ctx, `SELECT id FROM chunks ORDER BY id LIMIT 1`).Scan(&seededChunk); err != nil {
		t.Fatalf("seed chunk id: %v", err)
	}
	seedVec := make([]float32, 768)
	for i := range seedVec {
		seedVec[i] = 0.1
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO embeddings (target_kind, target_id, model, vec, input_hash)
		VALUES ('chunk', $1, $2, $3, 'seed')`,
		seededChunk, nomicModel, pgvector.NewVector(seedVec)); err != nil {
		t.Fatalf("seed embedding: %v", err)
	}
	var seededVec string
	if err := pool.QueryRow(ctx, `SELECT vec::text FROM embeddings`).Scan(&seededVec); err != nil {
		t.Fatalf("read seeded embedding: %v", err)
	}

	f := newFitFake(t)
	f.countFn = chunk.EstimateTokens
	client := nomicClient(t, f)
	path := filepath.Join(t.TempDir(), "nomic-fit.json")

	var buf bytes.Buffer
	if err := nomicFit(ctx, pool, client, fitOptions{Seed: 1, JSONPath: path}, &buf); err != nil {
		t.Fatalf("nomicFit: %v\n%s", err, buf.String())
	}

	res := readFitResult(t, path)
	if got := scalarCount(t, pool, `SELECT count(*) FROM embeddings`); got != 1 {
		t.Errorf("embeddings rows = %d, want 1 (the seeded row, unchanged)", got)
	}
	var afterVec string
	if err := pool.QueryRow(ctx, `SELECT vec::text FROM embeddings WHERE target_kind = 'chunk' AND target_id = $1 AND model = $2`,
		seededChunk, nomicModel).Scan(&afterVec); err != nil {
		t.Fatalf("read seeded embedding after run: %v", err)
	}
	if afterVec != seededVec {
		t.Errorf("seeded embedding changed by the run: %q -> %q", seededVec, afterVec)
	}
	if inputs := f.embedInputs(); len(inputs) != res.N+1 {
		t.Errorf("embed inputs = %d, want %d (one probe plus one document per request)", len(inputs), res.N+1)
	}
	for i, n := range f.embedSizes() {
		if n != 1 {
			t.Errorf("embed request %d carried %d documents, want 1", i, n)
		}
	}
	if !f.sawUnload() {
		t.Error("the model was not unloaded with keep_alive 0")
	}

	if res.Model != nomicModel {
		t.Errorf("model = %q, want %q", res.Model, nomicModel)
	}
	if res.OllamaVersion != f.version {
		t.Errorf("ollama_version = %q, want %q", res.OllamaVersion, f.version)
	}
	if res.Seed != 1 {
		t.Errorf("seed = %d, want 1", res.Seed)
	}
	if res.N != 10 || res.Accepted != 10 || res.Rejected != 0 {
		t.Errorf("n/accepted/rejected = %d/%d/%d, want 10/10/0", res.N, res.Accepted, res.Rejected)
	}
	if res.Fallback {
		t.Error("fallback = true, want false when a token count is returned")
	}
	if res.RatioP50 != 1.0 || res.RatioP99 != 1.0 || res.RatioMax != 1.0 {
		t.Errorf("ratios = %v/%v/%v, want 1/1/1", res.RatioP50, res.RatioP99, res.RatioMax)
	}
	wantEst := maxEstimate(headers)
	if res.EstMax != wantEst || res.RealMax != wantEst {
		t.Errorf("est_max/real_max = %d/%d, want %d", res.EstMax, res.RealMax, wantEst)
	}
}

// TestNomicFitFallbackWithoutTokenCount proves the fallback when the server
// returns no prompt_eval_count: accept/reject only, zero ratios, exit 0.
func TestNomicFitFallbackWithoutTokenCount(t *testing.T) {
	pool, _ := fitFixture(t, 4)
	f := newFitFake(t)
	f.omit = true
	client := nomicClient(t, f)
	path := filepath.Join(t.TempDir(), "nomic-fit.json")

	var buf bytes.Buffer
	if err := nomicFit(context.Background(), pool, client, fitOptions{Seed: 1, JSONPath: path}, &buf); err != nil {
		t.Fatalf("nomicFit: %v\n%s", err, buf.String())
	}
	res := readFitResult(t, path)
	if !res.Fallback {
		t.Error("fallback = false, want true when no token count is returned")
	}
	if res.Accepted != res.N || res.Rejected != 0 {
		t.Errorf("accepted/rejected = %d/%d, want %d/0", res.Accepted, res.Rejected, res.N)
	}
	if res.RatioP50 != 0 || res.RatioP99 != 0 || res.RatioMax != 0 || res.RealMax != 0 {
		t.Errorf("fallback ratios/real_max = %v/%v/%v/%d, want all 0",
			res.RatioP50, res.RatioP99, res.RatioMax, res.RealMax)
	}
}

// TestNomicFitFallbackFailureNoNumericBudget proves a fallback-mode rejection
// exits non-zero with a distinct message and no numeric replacement budget:
// with no measurable ratio there is no budget to propose.
func TestNomicFitFallbackFailureNoNumericBudget(t *testing.T) {
	pool, headers := fitFixture(t, 4)
	f := newFitFake(t)
	f.omit = true
	f.reject["search_document: "+headers[0]+"\n"] = true
	client := nomicClient(t, f)
	path := filepath.Join(t.TempDir(), "nomic-fit.json")

	var buf bytes.Buffer
	err := nomicFit(context.Background(), pool, client, fitOptions{Seed: 1, JSONPath: path}, &buf)
	if err == nil {
		t.Fatalf("nomicFit = nil, want a failure\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "-> 0") {
		t.Errorf("fallback failure proposed a numeric budget:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "ratio unavailable") {
		t.Errorf("want the distinct fallback message, got:\n%s", buf.String())
	}
	res := readFitResult(t, path)
	if !res.Fallback || res.Rejected == 0 {
		t.Errorf("fallback/rejected = %v/%d, want true/>0", res.Fallback, res.Rejected)
	}
}

// TestNomicFitFailureOnRejection proves a rejection fails the run with the
// replacement budget on the exit path, and the record still lands.
func TestNomicFitFailureOnRejection(t *testing.T) {
	pool, headers := fitFixture(t, 4)
	f := newFitFake(t)
	f.countFn = chunk.EstimateTokens
	f.reject["search_document: "+headers[0]+"\n"] = true
	client := nomicClient(t, f)
	path := filepath.Join(t.TempDir(), "nomic-fit.json")

	var buf bytes.Buffer
	err := nomicFit(context.Background(), pool, client, fitOptions{Seed: 1, JSONPath: path}, &buf)
	if err == nil {
		t.Fatalf("nomicFit = nil, want a failure\n%s", buf.String())
	}
	if !strings.Contains(err.Error(), "1945") {
		t.Errorf("err = %v, want the replacement budget 1945", err)
	}
	res := readFitResult(t, path)
	if res.Rejected == 0 {
		t.Error("rejected = 0, want the rejected document counted")
	}
	if res.Accepted+res.Rejected != res.N {
		t.Errorf("accepted+rejected = %d+%d, want n = %d", res.Accepted, res.Rejected, res.N)
	}
}

// TestNomicFitFailureOnOverLimitRatio proves an over-limit ratio fails with the
// computed replacement budget floor(0.95*2048/ratio_max).
func TestNomicFitFailureOnOverLimitRatio(t *testing.T) {
	pool, _ := fitFixture(t, 4)
	f := newFitFake(t)
	f.countFn = func(in string) int { return 2 * chunk.EstimateTokens(in) }
	client := nomicClient(t, f)
	path := filepath.Join(t.TempDir(), "nomic-fit.json")

	var buf bytes.Buffer
	err := nomicFit(context.Background(), pool, client, fitOptions{Seed: 1, JSONPath: path}, &buf)
	if err == nil {
		t.Fatalf("nomicFit = nil, want a failure\n%s", buf.String())
	}
	if !strings.Contains(err.Error(), "972") {
		t.Errorf("err = %v, want the replacement budget 972", err)
	}
	res := readFitResult(t, path)
	if res.RatioMax != 2.0 {
		t.Errorf("ratio_max = %v, want 2", res.RatioMax)
	}
	if res.Rejected != 0 {
		t.Errorf("rejected = %d, want 0 (the failure is the ratio)", res.Rejected)
	}
}

// TestNomicFitAbortsOnServerError proves a 5xx from the embed endpoint aborts
// the run with an error instead of being counted as a rejected document, so an
// outage is never reported as a fit failure.
func TestNomicFitAbortsOnServerError(t *testing.T) {
	pool, headers := fitFixture(t, 4)
	f := newFitFake(t)
	f.fail["search_document: "+headers[0]+"\n"] = true
	client := nomicClient(t, f)
	path := filepath.Join(t.TempDir(), "nomic-fit.json")

	var buf bytes.Buffer
	err := nomicFit(context.Background(), pool, client, fitOptions{Seed: 1, JSONPath: path}, &buf)
	if err == nil {
		t.Fatalf("nomicFit = nil, want the server error to abort\n%s", buf.String())
	}
	if !strings.Contains(err.Error(), "embed") {
		t.Errorf("err = %v, want the embed server error", err)
	}
	if strings.Contains(buf.String(), "FAILED") {
		t.Errorf("an outage was reported as a fit failure:\n%s", buf.String())
	}
}

// TestNomicFitSecondFailureIsIdentical documents the STOP semantics: the tool
// cannot know a run is the second, so it never auto-replaces or auto-retries.
// A failing run returns non-zero and prints the computed budget; running again
// on the same failing data fails identically with the same budget.
func TestNomicFitSecondFailureIsIdentical(t *testing.T) {
	pool, _ := fitFixture(t, 4)
	f := newFitFake(t)
	f.countFn = func(in string) int { return 2 * chunk.EstimateTokens(in) }
	client := nomicClient(t, f)
	path := filepath.Join(t.TempDir(), "nomic-fit.json")

	run := func(name string) (string, string) {
		var buf bytes.Buffer
		err := nomicFit(context.Background(), pool, client, fitOptions{Seed: 1, JSONPath: path}, &buf)
		if err == nil {
			t.Fatalf("run %s: nomicFit = nil, want a failure\n%s", name, buf.String())
		}
		if res := readFitResult(t, path); res.RatioMax != 2.0 {
			t.Errorf("run %s: ratio_max = %v, want 2", name, res.RatioMax)
		}
		return err.Error(), buf.String()
	}

	firstErr, firstOut := run("first")
	secondErr, secondOut := run("second")
	if firstErr != secondErr {
		t.Errorf("second failure differs:\n first: %s\nsecond: %s", firstErr, secondErr)
	}
	if firstOut != secondOut {
		t.Errorf("second output differs:\n first: %s\nsecond: %s", firstOut, secondOut)
	}
	if !strings.Contains(firstErr, "972") {
		t.Errorf("err = %v, want the computed replacement budget 972", firstErr)
	}
}

// TestToolsRegistersNomicFit proves the command is listed in the tools help.
func TestToolsRegistersNomicFit(t *testing.T) {
	var errOut bytes.Buffer
	code := cli.App{Name: "tools", Commands: commands, Stderr: &errOut}.Run([]string{"help"})
	if code != 2 {
		t.Fatalf("help exit = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "nomic-fit") {
		t.Fatalf("tools usage does not list nomic-fit:\n%s", errOut.String())
	}
}

// scalarCount runs a query returning one integer.
func scalarCount(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return n
}

// maxEstimate is the largest estimate among the rendered documents.
func maxEstimate(headers []string) int {
	hi := 0
	for _, h := range headers {
		if n := chunk.EstimateTokens("search_document: " + h + "\n"); n > hi {
			hi = n
		}
	}
	return hi
}
