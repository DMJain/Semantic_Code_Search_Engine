package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/chunk"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"
)

// embedFake is a /api/embed server that records every request's inputs and can
// be told to answer a chosen input with an input-too-long error, a wrong-
// dimension vector or a generic server error. It never touches a real Ollama.
type embedFake struct {
	t            *testing.T
	model        string
	dim          int
	server       *httptest.Server
	mu           sync.Mutex
	requests     [][]string
	failTooLong  map[string]bool
	failWrongDim map[string]bool
	failGeneric  map[string]bool
	after        func(total int)
}

func newEmbedFake(t *testing.T, model string) *embedFake {
	t.Helper()
	dim := 1024
	if model == "nomic-embed-text" {
		dim = 768
	}
	f := &embedFake{
		t: t, model: model, dim: dim,
		failTooLong:  map[string]bool{},
		failWrongDim: map[string]bool{},
		failGeneric:  map[string]bool{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

func (f *embedFake) handle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model    string   `json:"model"`
		Input    []string `json:"input"`
		Truncate *bool    `json:"truncate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.t.Errorf("decode request: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.requests = append(f.requests, append([]string(nil), req.Input...))
	total := len(f.requests)
	after := f.after
	tooLong, wrongAt, generic := false, -1, false
	for i, in := range req.Input {
		if f.failTooLong[in] {
			tooLong = true
		}
		if f.failWrongDim[in] {
			wrongAt = i
		}
		if f.failGeneric[in] {
			generic = true
		}
	}
	f.mu.Unlock()
	if after != nil {
		after(total)
	}

	if generic {
		http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
		return
	}
	if tooLong {
		http.Error(w, `{"error":"input length exceeds context window"}`, http.StatusBadRequest)
		return
	}
	out := make([][]float64, len(req.Input))
	for i := range req.Input {
		if i == wrongAt {
			out[i] = unitVec(f.dim-1, 1)
			continue
		}
		out[i] = unitVec(f.dim, 1)
	}
	if err := json.NewEncoder(w).Encode(map[string]any{"embeddings": out}); err != nil {
		f.t.Errorf("encode response: %v", err)
	}
}

// failInputs marks inputs to answer with the given failure mode.
func (f *embedFake) failInputs(mode string, inputs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, in := range inputs {
		switch mode {
		case "too_long":
			f.failTooLong[in] = true
		case "wrong_dim":
			f.failWrongDim[in] = true
		default:
			f.failGeneric[in] = true
		}
	}
}

func (f *embedFake) clearFailures(mode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch mode {
	case "too_long":
		f.failTooLong = map[string]bool{}
	case "wrong_dim":
		f.failWrongDim = map[string]bool{}
	default:
		f.failGeneric = map[string]bool{}
	}
}

// setAfterRequest installs a hook called with the running request count after
// each request is recorded, so a test can cancel mid-run.
func (f *embedFake) setAfterRequest(fn func(total int)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.after = fn
}

func (f *embedFake) countInput(s string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, req := range f.requests {
		for _, in := range req {
			if in == s {
				n++
			}
		}
	}
	return n
}

func (f *embedFake) hasInput(s string) bool { return f.countInput(s) > 0 }

func (f *embedFake) requestSizes() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int, len(f.requests))
	for i, r := range f.requests {
		out[i] = len(r)
	}
	return out
}

func clientFor(t *testing.T, f *embedFake) *embed.Ollama {
	t.Helper()
	c, err := embed.NewOllama(f.server.URL, f.model, 0)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// embedFixture returns a throwaway database with the chunk fixture split and
// every requested file marked in-slice.
func embedFixture(t *testing.T, inSlice ...string) (*pgxpool.Pool, string) {
	t.Helper()
	pool, root := chunkFixture(t)
	runChunkStage(t, pool, root, chunkOptions{})
	for _, path := range inSlice {
		if _, err := pool.Exec(context.Background(),
			`UPDATE files SET in_slice = true WHERE path = $1`, path); err != nil {
			t.Fatalf("mark %s in slice: %v", path, err)
		}
	}
	return pool, root
}

func runEmbedStage(t *testing.T, pool *pgxpool.Pool, client embedClient, opts embedOptions) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	err := embedStage(context.Background(), pool, client, opts, &buf)
	return buf.String(), err
}

func defaultEmbedOptions(kinds ...string) embedOptions {
	if len(kinds) == 0 {
		kinds = defaultEmbedKinds
	}
	return embedOptions{Kinds: kinds}
}

// parseKindCounts reads one kind's "<kind>: embedded=.. skipped=.. too_large=..
// truncated=.." line.
func parseKindCounts(t *testing.T, out, kind string) kindCounts {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, kind+":") {
			var c kindCounts
			if _, err := fmt.Sscanf(line, kind+": embedded=%d skipped=%d too_large=%d truncated=%d",
				&c.embedded, &c.skipped, &c.tooLarge, &c.truncated); err != nil {
				t.Fatalf("parse %q: %v", line, err)
			}
			return c
		}
	}
	t.Fatalf("output missing a %q line:\n%s", kind, out)
	return kindCounts{}
}

// chunkRow reads one chunk's id, content and header by symbol name.
func chunkRow(t *testing.T, pool *pgxpool.Pool, symbol string) (id int64, content, header string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `
		SELECT c.id, c.content, c.header
		FROM chunks c JOIN symbols s ON s.id = c.symbol_id
		WHERE s.name = $1 AND c.part_idx = 0`, symbol).Scan(&id, &content, &header); err != nil {
		t.Fatalf("read chunk %s: %v", symbol, err)
	}
	return id, content, header
}

// embeddingsFor returns the stored input hashes for one chunk and kind.
func embeddingsFor(t *testing.T, pool *pgxpool.Pool, kind string, id int64, model string) (hash string, found bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
		SELECT input_hash FROM embeddings
		WHERE target_kind = $1 AND target_id = $2 AND model = $3`, kind, id, model).Scan(&hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false
		}
		t.Fatalf("read %s embedding for %d: %v", kind, id, err)
	}
	return hash, true
}

// TestParseEmbedKinds pins flag validation against the target_kind enum.
func TestParseEmbedKinds(t *testing.T) {
	tests := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{in: "chunk_raw,chunk_header", want: []string{"chunk_raw", "chunk_header"}},
		{in: " chunk_header ", want: []string{"chunk_header"}},
		{in: "chunk_raw,chunk_raw", want: []string{"chunk_raw"}},
		{in: "bogus", wantErr: true},
		{in: "summary_file", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tt := range tests {
		got, err := parseEmbedKinds(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseEmbedKinds(%q) = %v, want error", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseEmbedKinds(%q): %v", tt.in, err)
			continue
		}
		if strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("parseEmbedKinds(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// TestEmbedStageStoresBothKindsWithInputHashes embeds every in-slice chunk of
// one file in both kinds and asserts the stored hashes are H(model, template,
// content) and H(model, template, header, content) (D-079, D-081).
func TestEmbedStageStoresBothKindsWithInputHashes(t *testing.T) {
	pool, _ := embedFixture(t, "basic/basic.go")
	f := newEmbedFake(t, "qwen3-embedding:0.6b")
	client := clientFor(t, f)

	out, err := runEmbedStage(t, pool, client, defaultEmbedOptions())
	if err != nil {
		t.Fatalf("embedStage: %v\n%s", err, out)
	}
	inSlice := int(scalarInt(t, pool, `
		SELECT count(*) FROM chunks c JOIN files f ON f.id = c.file_id WHERE f.in_slice`))
	for _, kind := range defaultEmbedKinds {
		if got := parseKindCounts(t, out, kind); got.embedded != inSlice || got.skipped != 0 {
			t.Errorf("%s counts = %+v, want embedded=%d skipped=0", kind, got, inSlice)
		}
	}

	rows, err := pool.Query(context.Background(), `
		SELECT c.id, c.content, c.header FROM chunks c JOIN files f ON f.id = c.file_id WHERE f.in_slice`)
	if err != nil {
		t.Fatalf("query chunks: %v", err)
	}
	defer rows.Close()
	checked := 0
	for rows.Next() {
		var id int64
		var content, header string
		if err := rows.Scan(&id, &content, &header); err != nil {
			t.Fatalf("scan chunk: %v", err)
		}
		if got, ok := embeddingsFor(t, pool, kindChunkRaw, id, client.Model()); !ok || got != client.InputHash(content) {
			t.Errorf("chunk %d raw hash = %q (found %v), want %q", id, got, ok, client.InputHash(content))
		}
		if got, ok := embeddingsFor(t, pool, kindChunkHeader, id, client.Model()); !ok || got != client.InputHash(header, content) {
			t.Errorf("chunk %d header hash = %q (found %v), want %q", id, got, ok, client.InputHash(header, content))
		}
		checked++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("chunks rows: %v", err)
	}
	if checked != inSlice {
		t.Errorf("checked %d chunks, want %d", checked, inSlice)
	}
}

// TestEmbedStageNoTitleAndPinnedRender proves both kinds render with no title:
// the nomic no-title render of "func evictPod()" still hashes to the pinned
// D-003 value, and a header render is the model's registered document prefix
// followed by the header (D-079).
func TestEmbedStageNoTitleAndPinnedRender(t *testing.T) {
	pool, _ := embedFixture(t, "basic/basic.go")
	f := newEmbedFake(t, "nomic-embed-text")
	client := clientFor(t, f)

	if _, err := runEmbedStage(t, pool, client, defaultEmbedOptions()); err != nil {
		t.Fatalf("embedStage: %v", err)
	}

	const pinned = "7de036e71c1bb500bad38d85ad0bfd141cd3fa5b284430cd5e6ab5840aca29d0"
	if _, err := client.EmbedDocuments(context.Background(), []embed.Document{{Text: "func evictPod()"}}); err != nil {
		t.Fatalf("EmbedDocuments: %v", err)
	}
	rendered := "search_document: func evictPod()"
	if !f.hasInput(rendered) {
		t.Fatalf("fake never saw the nomic no-title render %q", rendered)
	}
	sum := sha256.Sum256([]byte(rendered))
	if got := hex.EncodeToString(sum[:]); got != pinned {
		t.Fatalf("nomic no-title render of func evictPod() = %s, want pinned %s", got, pinned)
	}
	if got := client.DocPrefix(); got != "search_document: " {
		t.Errorf("DocPrefix() = %q, want %q", got, "search_document: ")
	}

	id, content, header := chunkRow(t, pool, "Documented")
	rawDoc := client.DocPrefix() + content
	headerDoc := client.DocPrefix() + header + "\n" + content
	if !f.hasInput(rawDoc) {
		t.Errorf("raw document not rendered as prefix+content:\nwant %q", rawDoc)
	}
	if !f.hasInput(headerDoc) {
		t.Errorf("header document not rendered as prefix+header+content:\nwant %q", headerDoc)
	}
	if !strings.HasPrefix(headerDoc, "search_document: // package: ") {
		t.Errorf("header render %q does not start with the document prefix then the header", headerDoc)
	}
	if _, ok := embeddingsFor(t, pool, kindChunkRaw, id, client.Model()); !ok {
		t.Errorf("no raw embedding stored for Documented")
	}
}

// TestEmbedStageBatchesOf32 proves each request carries at most 32 documents and
// a request of exactly 32 happens once the work list is larger.
func TestEmbedStageBatchesOf32(t *testing.T) {
	pool, _ := embedFixture(t, "basic/basic.go")
	addSyntheticInSliceChunks(t, pool, 40)
	f := newEmbedFake(t, "qwen3-embedding:0.6b")
	client := clientFor(t, f)

	if _, err := runEmbedStage(t, pool, client, defaultEmbedOptions()); err != nil {
		t.Fatalf("embedStage: %v", err)
	}
	sizes := f.requestSizes()
	full := 0
	for _, n := range sizes {
		if n > embedBatchSize {
			t.Fatalf("request of %d inputs exceeds the batch size %d", n, embedBatchSize)
		}
		if n == embedBatchSize {
			full++
		}
	}
	if full < 2 {
		t.Errorf("requests of exactly %d inputs = %d, want at least 2 (one per kind): sizes=%v",
			embedBatchSize, full, sizes)
	}
}

// TestEmbedStageStopsOnCancel proves a signal (ctx cancellation) finishes the
// in-flight batch, commits it, then stops cleanly: the run returns nil, the
// committed batch is intact, and no later batch or kind reaches the server
// (architecture "SIGINT/SIGTERM finishes the in-flight item, commits, exits 0").
func TestEmbedStageStopsOnCancel(t *testing.T) {
	pool, _ := embedFixture(t, "basic/basic.go")
	addSyntheticInSliceChunks(t, pool, 40)
	f := newEmbedFake(t, "qwen3-embedding:0.6b")
	client := clientFor(t, f)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Request 1 is the probe; cancel after the first embedding batch, so the
	// in-flight batch completes but the kind and the run stop.
	var once sync.Once
	f.setAfterRequest(func(total int) {
		if total == 2 {
			once.Do(cancel)
		}
	})

	var buf bytes.Buffer
	err := embedStage(ctx, pool, client, defaultEmbedOptions(), &buf)
	if err != nil {
		t.Fatalf("embedStage after a signal = %v, want nil (clean stop)\n%s", err, buf.String())
	}
	out := buf.String()

	if got := len(f.requests); got != 2 {
		t.Fatalf("server requests = %d, want 2 (probe + the one committed batch); sizes=%v", got, f.requestSizes())
	}
	raw := parseKindCounts(t, out, kindChunkRaw)
	if raw.embedded == 0 {
		t.Fatalf("no chunk_raw batch committed before the signal:\n%s", out)
	}
	if stored := scalarInt(t, pool, `
		SELECT count(*) FROM embeddings WHERE target_kind = $1`, kindChunkRaw); stored != int64(raw.embedded) {
		t.Errorf("stored chunk_raw vectors = %d, want the committed batch %d", stored, raw.embedded)
	}
	if strings.Contains(out, kindChunkHeader+":") {
		t.Errorf("the stopped run still printed a %s summary:\n%s", kindChunkHeader, out)
	}
	if stored := scalarInt(t, pool, `
		SELECT count(*) FROM embeddings WHERE target_kind = $1`, kindChunkHeader); stored != 0 {
		t.Errorf("stopped run stored %d %s vectors, want 0", stored, kindChunkHeader)
	}
}

// TestEmbedStageStopSkipsNextKind proves a first kind whose failures exceed 1%
// of in-slice chunks stops the run before the next kind starts: the error is
// returned, the next kind writes no rows and the server never sees its
// documents (D-082).
func TestEmbedStageStopSkipsNextKind(t *testing.T) {
	pool, _ := embedFixture(t, "extra/extra.go")
	f := newEmbedFake(t, "qwen3-embedding:0.6b")
	client := clientFor(t, f)

	_, content, header := chunkRow(t, pool, "Entry")
	if content == "" || header == "" {
		t.Fatal("Entry chunk missing content/header in the extra fixture")
	}
	if _, failContent, _ := chunkRow(t, pool, "overBudget"); failContent == "" {
		t.Fatal("overBudget chunk missing in the extra fixture")
	}

	out, err := runEmbedStage(t, pool, client, defaultEmbedOptions())
	if err == nil || !strings.Contains(err.Error(), "1%") {
		t.Fatalf("err = %v, want the 1%% STOP\n%s", err, out)
	}
	if strings.Contains(out, kindChunkHeader+":") {
		t.Errorf("the stopped run still printed a %s summary:\n%s", kindChunkHeader, out)
	}
	if n := scalarInt(t, pool, `
		SELECT count(*) FROM embeddings WHERE target_kind = $1`, kindChunkHeader); n != 0 {
		t.Errorf("stopped run stored %d %s vectors, want 0", n, kindChunkHeader)
	}
	if n := scalarInt(t, pool, `
		SELECT count(*) FROM embeddings WHERE target_kind = $1`, kindChunkRaw); n == 0 {
		t.Errorf("stopped run stored no %s vectors, want the first kind's batch", kindChunkRaw)
	}
	// The server saw the first kind's documents but no header document.
	if !f.hasInput(content) {
		t.Errorf("the server never saw the first kind's document %q", content)
	}
	if f.hasInput(header + "\n" + content) {
		t.Errorf("the stopped run sent a %s document to the server", kindChunkHeader)
	}
}

// TestEmbedStageLimitAndOrdering proves --limit embeds exactly N items per kind,
// drawn from the work list ordered by chunk id (so the lowest in-slice ids are
// the ones embedded), and a rerun is a no-op.
func TestEmbedStageLimitAndOrdering(t *testing.T) {
	pool, _ := embedFixture(t, "basic/basic.go")
	addSyntheticInSliceChunks(t, pool, 40)
	f := newEmbedFake(t, "qwen3-embedding:0.6b")
	client := clientFor(t, f)

	const limit = 5
	out, err := runEmbedStage(t, pool, client, embedOptions{Kinds: defaultEmbedKinds, Limit: limit})
	if err != nil {
		t.Fatalf("embedStage: %v\n%s", err, out)
	}
	for _, kind := range defaultEmbedKinds {
		if got := parseKindCounts(t, out, kind).embedded; got != limit {
			t.Errorf("%s embedded = %d, want %d", kind, got, limit)
		}
	}
	if stored := scalarInt(t, pool, `SELECT count(*) FROM embeddings`); stored != 2*limit {
		t.Errorf("stored vectors = %d, want %d", stored, 2*limit)
	}

	// The work list is chunk-id ordered, so the limited set is exactly the
	// lowest in-slice ids.
	ids, err := pool.Query(context.Background(), `
		SELECT c.id FROM chunks c JOIN files f ON f.id = c.file_id
		WHERE f.in_slice ORDER BY c.id LIMIT $1`, limit)
	if err != nil {
		t.Fatalf("query lowest ids: %v", err)
	}
	var wantIDs []int64
	for ids.Next() {
		var id int64
		if err := ids.Scan(&id); err != nil {
			t.Fatalf("scan id: %v", err)
		}
		wantIDs = append(wantIDs, id)
	}
	if err := ids.Err(); err != nil {
		t.Fatalf("ids rows: %v", err)
	}
	for _, kind := range defaultEmbedKinds {
		for _, id := range wantIDs {
			if _, ok := embeddingsFor(t, pool, kind, id, client.Model()); !ok {
				t.Errorf("%s: lowest-id chunk %d was not embedded", kind, id)
			}
		}
	}

	out, err = runEmbedStage(t, pool, client, embedOptions{Kinds: defaultEmbedKinds, Limit: limit})
	if err != nil {
		t.Fatalf("rerun: %v\n%s", err, out)
	}
	for _, kind := range defaultEmbedKinds {
		if got := parseKindCounts(t, out, kind).embedded; got != 0 {
			t.Errorf("%s rerun embedded = %d, want 0", kind, got)
		}
	}
}

// TestRunEmbedModelFlag proves --model defaults to OLLAMA_EMBED_MODEL and an
// unknown model is rejected before any database work (D-079, D-081).
func TestRunEmbedModelFlag(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x:x@127.0.0.1:1/x?sslmode=disable")
	t.Setenv("OLLAMA_URL", "http://127.0.0.1:1")

	t.Run("default comes from config", func(t *testing.T) {
		t.Setenv("OLLAMA_EMBED_MODEL", "mystery-default")
		err := runEmbed(context.Background(), nil)
		if err == nil || !strings.Contains(err.Error(), "mystery-default") {
			t.Fatalf("err = %v, want the configured model named", err)
		}
	})
	t.Run("unknown model errors before any SQL", func(t *testing.T) {
		t.Setenv("OLLAMA_EMBED_MODEL", "qwen3-embedding:0.6b")
		err := runEmbed(context.Background(), []string{"--model", "mystery-flag"})
		if err == nil || !strings.Contains(err.Error(), "mystery-flag") {
			t.Fatalf("err = %v, want the unknown flag model named", err)
		}
		if strings.Contains(strings.ToLower(err.Error()), "connect") {
			t.Fatalf("err = %v, want the model error before any database connection", err)
		}
	})
}

// addSyntheticInSliceChunks inserts n extra chunks into an already in-slice
// file, so the batching and threshold tests have enough work without touching
// the fixture.
func addSyntheticInSliceChunks(t *testing.T, pool *pgxpool.Pool, n int) {
	t.Helper()
	ctx := context.Background()
	var fileID, symbolID int64
	if err := pool.QueryRow(ctx, `
		SELECT f.id, s.id FROM files f JOIN symbols s ON s.file_id = f.id
		WHERE f.path = 'basic/basic.go' ORDER BY s.id LIMIT 1`).Scan(&fileID, &symbolID); err != nil {
		t.Fatalf("pick synthetic symbol: %v", err)
	}
	for i := 0; i < n; i++ {
		content := fmt.Sprintf("func synthetic%d() {}", i)
		if _, err := pool.Exec(ctx, `
			INSERT INTO chunks (symbol_id, file_id, part_idx, content, header, content_hash, token_count, start_line, end_line)
			VALUES ($1, $2, $3, $4, '', 'x', $5, 1, 1)`,
			symbolID, fileID, 1000+i, content, chunk.EstimateTokens(content)); err != nil {
			t.Fatalf("insert synthetic chunk: %v", err)
		}
	}
}

// runEmbedEachKind drives one stage call per kind, so the 1% STOP at the end of
// a kind does not skip the following kind. The extra fixture has an over-budget
// chunk, so each call returns the STOP error.
func runEmbedEachKind(t *testing.T, pool *pgxpool.Pool, client embedClient, kinds ...string) []error {
	t.Helper()
	errs := make([]error, len(kinds))
	for i, kind := range kinds {
		if _, errs[i] = runEmbedStage(t, pool, client, embedOptions{Kinds: []string{kind}}); errs[i] == nil {
			t.Fatalf("%s: expected the 1%% STOP; the fixture has an unembeddable chunk", kind)
		}
		if !strings.Contains(errs[i].Error(), "1%") {
			t.Fatalf("%s: err = %v, want the 1%% STOP", kind, errs[i])
		}
	}
	return errs
}

// TestEmbedStageTooLargeNeverSent proves an item over EmbeddableContentMax gets
// zero server requests and a too_large failure row, in both kinds (D-082).
func TestEmbedStageTooLargeNeverSent(t *testing.T) {
	pool, _ := embedFixture(t, "extra/extra.go")
	f := newEmbedFake(t, "qwen3-embedding:0.6b")
	client := clientFor(t, f)

	id, content, header := chunkRow(t, pool, "overBudget")
	if content == "" {
		t.Fatal("overBudget content is empty")
	}
	if f.hasInput(content) || f.hasInput(header+"\n"+content) {
		t.Errorf("the too_large item reached the server (content=%q)", content)
	}
	for _, kind := range defaultEmbedKinds {
		out, err := runEmbedStage(t, pool, client, embedOptions{Kinds: []string{kind}})
		if err == nil || !strings.Contains(err.Error(), "1%") {
			t.Fatalf("%s: err = %v, want the 1%% STOP after recording the failure\n%s", kind, err, out)
		}
		if _, ok := embeddingsFor(t, pool, kind, id, client.Model()); ok {
			t.Errorf("%s stored a vector for the too_large chunk", kind)
		}
		var reason string
		if err := pool.QueryRow(context.Background(), `
			SELECT reason FROM embed_failures WHERE target_kind = $1 AND target_id = $2 AND model = $3`,
			kind, id, client.Model()).Scan(&reason); err != nil {
			t.Fatalf("read %s failure for overBudget: %v", kind, err)
		}
		if reason != "too_large" {
			t.Errorf("%s failure reason = %q, want too_large", kind, reason)
		}
		if got := parseKindCounts(t, out, kind).tooLarge; got != 1 {
			t.Errorf("%s too_large = %d, want 1", kind, got)
		}
	}
}

// TestEmbedStageInputTooLongRecordsTruncated proves an input-too-long batch
// error records truncated for the offending item and the other items of the
// batch are retried one by one and stored (D-015, D-082).
func TestEmbedStageInputTooLongRecordsTruncated(t *testing.T) {
	pool, _ := embedFixture(t, "basic/basic.go")
	f := newEmbedFake(t, "qwen3-embedding:0.6b")
	client := clientFor(t, f)
	id, content, _ := chunkRow(t, pool, "Plain")
	f.failInputs("too_long", content)

	out, err := runEmbedStage(t, pool, client, defaultEmbedOptions(kindChunkRaw))
	if err == nil || !strings.Contains(err.Error(), "1%") {
		t.Fatalf("err = %v, want the 1%% STOP\n%s", err, out)
	}
	if _, ok := embeddingsFor(t, pool, kindChunkRaw, id, client.Model()); ok {
		t.Errorf("the truncated chunk got a stored vector")
	}
	var reason string
	if err := pool.QueryRow(context.Background(), `
		SELECT reason FROM embed_failures WHERE target_kind = $1 AND target_id = $2 AND model = $3`,
		kindChunkRaw, id, client.Model()).Scan(&reason); err != nil {
		t.Fatalf("read failure for Plain: %v", err)
	}
	if reason != "truncated" {
		t.Errorf("failure reason = %q, want truncated", reason)
	}
	if got := parseKindCounts(t, out, kindChunkRaw).truncated; got != 1 {
		t.Errorf("chunk_raw truncated = %d, want 1", got)
	}
	// The rest of the batch was retried individually and stored.
	other := scalarInt(t, pool, `
		SELECT count(*) FROM embeddings e JOIN chunks c ON c.id = e.target_id JOIN files fl ON fl.id = c.file_id
		WHERE e.target_kind = $1 AND fl.in_slice AND e.model = $2`, kindChunkRaw, client.Model())
	inSlice := scalarInt(t, pool, `
		SELECT count(*) FROM chunks c JOIN files f ON f.id = c.file_id WHERE f.in_slice`)
	if other != inSlice-1 {
		t.Errorf("stored raw vectors = %d, want every in-slice chunk but the truncated one = %d", other, inSlice-1)
	}
	// A batch request and then per-item requests happened.
	singles := 0
	for _, n := range f.requestSizes() {
		if n == 1 {
			singles++
		}
	}
	if singles == 0 {
		t.Errorf("no per-item retry requests recorded: sizes=%v", f.requestSizes())
	}
}

// TestEmbedStageGenericErrorIsNotTruncated proves a per-item error that is not
// an input-too-long (a 500) is a hard error: nothing is stored, no failure row
// is written, and the error is not ErrInputTooLong (D-082).
func TestEmbedStageGenericErrorIsNotTruncated(t *testing.T) {
	pool, _ := embedFixture(t, "basic/basic.go")
	f := newEmbedFake(t, "qwen3-embedding:0.6b")
	client := clientFor(t, f)
	id, content, _ := chunkRow(t, pool, "Plain")
	f.failInputs("generic", content)

	out, err := runEmbedStage(t, pool, client, defaultEmbedOptions(kindChunkRaw))
	if err == nil {
		t.Fatalf("err = nil, want a hard error\n%s", out)
	}
	if errors.Is(err, embed.ErrInputTooLong) {
		t.Fatalf("err = %v, want NOT errors.Is ErrInputTooLong", err)
	}
	if strings.Contains(out, "truncated=") {
		t.Errorf("a hard error printed a kind summary:\n%s", out)
	}
	var n int64
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM embed_failures WHERE target_kind = $1 AND target_id = $2`,
		kindChunkRaw, id).Scan(&n); err != nil {
		t.Fatalf("count failures: %v", err)
	}
	if n != 0 {
		t.Errorf("a generic per-item error recorded %d failure rows, want 0", n)
	}
	if _, ok := embeddingsFor(t, pool, kindChunkRaw, id, client.Model()); ok {
		t.Errorf("a generic per-item error stored a vector")
	}
}

// TestEmbedStageWrongDimensionRejectedAsHardError proves a wrong-dimension
// response is never stored and surfaces as embed.ErrWrongDimension (not a
// truncation failure).
func TestEmbedStageWrongDimensionRejectedAsHardError(t *testing.T) {
	pool, _ := embedFixture(t, "basic/basic.go")
	f := newEmbedFake(t, "qwen3-embedding:0.6b")
	client := clientFor(t, f)
	id, content, _ := chunkRow(t, pool, "Plain")
	f.failInputs("wrong_dim", content)

	out, err := runEmbedStage(t, pool, client, defaultEmbedOptions(kindChunkRaw))
	if !errors.Is(err, embed.ErrWrongDimension) {
		t.Fatalf("err = %v, want errors.Is ErrWrongDimension\n%s", err, out)
	}
	if _, ok := embeddingsFor(t, pool, kindChunkRaw, id, client.Model()); ok {
		t.Errorf("a wrong-dimension vector was stored")
	}
	var n int64
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM embed_failures WHERE target_kind = $1 AND target_id = $2`,
		kindChunkRaw, id).Scan(&n); err != nil {
		t.Fatalf("count failures: %v", err)
	}
	if n != 0 {
		t.Errorf("wrong dimension recorded %d failure rows, want 0 (hard error, not truncated)", n)
	}
}

// TestEmbedStageIdempotent proves a rerun over an unchanged database inserts 0
// rows and reports every item skipped.
func TestEmbedStageIdempotent(t *testing.T) {
	pool, _ := embedFixture(t, "basic/basic.go")
	f := newEmbedFake(t, "qwen3-embedding:0.6b")
	client := clientFor(t, f)

	if _, err := runEmbedStage(t, pool, client, defaultEmbedOptions()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	before := scalarInt(t, pool, `SELECT count(*) FROM embeddings`)

	out, err := runEmbedStage(t, pool, client, defaultEmbedOptions())
	if err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	for _, kind := range defaultEmbedKinds {
		if got := parseKindCounts(t, out, kind); got.embedded != 0 {
			t.Errorf("%s second run embedded = %d, want 0", kind, got.embedded)
		}
	}
	if after := scalarInt(t, pool, `SELECT count(*) FROM embeddings`); after != before {
		t.Errorf("embeddings count changed on rerun: before %d, after %d", before, after)
	}
}

// TestEmbedStageOrphanCleanup proves deleting one chunk leaves two orphan chunk
// vectors, the next run deletes exactly those and reports orphans_deleted, and
// no chunk-kind vector is left without a chunk (D-081).
func TestEmbedStageOrphanCleanup(t *testing.T) {
	pool, _ := embedFixture(t, "basic/basic.go")
	f := newEmbedFake(t, "qwen3-embedding:0.6b")
	client := clientFor(t, f)

	if _, err := runEmbedStage(t, pool, client, defaultEmbedOptions()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	id, _, _ := chunkRow(t, pool, "Plain")
	if _, err := pool.Exec(context.Background(), `DELETE FROM chunks WHERE id = $1`, id); err != nil {
		t.Fatalf("delete chunk %d: %v", id, err)
	}
	if orphans := scalarInt(t, pool, `
		SELECT count(*) FROM embeddings e
		LEFT JOIN chunks c ON c.id = e.target_id
		WHERE e.target_kind IN ('chunk_raw','chunk_header') AND c.id IS NULL`); orphans != 2 {
		t.Fatalf("orphan vectors after delete = %d, want 2", orphans)
	}

	out, err := runEmbedStage(t, pool, client, defaultEmbedOptions())
	if err != nil {
		t.Fatalf("second run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "orphans_deleted: 2") {
		t.Errorf("output missing orphans_deleted: 2:\n%s", out)
	}
	if left := scalarInt(t, pool, `
		SELECT count(*) FROM embeddings e
		LEFT JOIN chunks c ON c.id = e.target_id
		WHERE e.target_kind IN ('chunk_raw','chunk_header') AND c.id IS NULL`); left != 0 {
		t.Errorf("orphan chunk vectors after cleanup = %d, want 0", left)
	}
}

// TestEmbedStageNoNonSliceChunkEmbedded proves only in-slice chunks are
// embedded (D-029): every non-slice file's chunks stay unembedded.
func TestEmbedStageNoNonSliceChunkEmbedded(t *testing.T) {
	pool, _ := embedFixture(t, "basic/basic.go")
	f := newEmbedFake(t, "qwen3-embedding:0.6b")
	client := clientFor(t, f)

	if _, err := runEmbedStage(t, pool, client, defaultEmbedOptions()); err != nil {
		t.Fatalf("embedStage: %v", err)
	}
	if n := scalarInt(t, pool, `
		SELECT count(*) FROM embeddings e
		JOIN chunks c ON c.id = e.target_id
		JOIN files f ON f.id = c.file_id
		WHERE NOT f.in_slice`); n != 0 {
		t.Errorf("non-slice chunk vectors = %d, want 0", n)
	}
	inSlice := scalarInt(t, pool, `
		SELECT count(*) FROM chunks c JOIN files f ON f.id = c.file_id WHERE f.in_slice`)
	if stored := scalarInt(t, pool, `SELECT count(*) FROM embeddings`); stored != 2*inSlice {
		t.Errorf("stored vectors = %d, want 2 per in-slice chunk = %d", stored, 2*inSlice)
	}
}

// TestEmbedStageFailureSetsIdentical proves chunk_raw and chunk_header fail on
// exactly the same chunks per model (D-082). Each kind runs on its own because
// the 1% STOP after chunk_raw would otherwise skip chunk_header.
func TestEmbedStageFailureSetsIdentical(t *testing.T) {
	pool, _ := embedFixture(t, "extra/extra.go")
	f := newEmbedFake(t, "qwen3-embedding:0.6b")
	client := clientFor(t, f)

	runEmbedEachKind(t, pool, client, defaultEmbedKinds...)
	for _, dir := range [][2]string{{kindChunkRaw, kindChunkHeader}, {kindChunkHeader, kindChunkRaw}} {
		var n int64
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*) FROM (
				SELECT target_id FROM embed_failures WHERE target_kind = $1 AND model = $2
				EXCEPT
				SELECT target_id FROM embed_failures WHERE target_kind = $3 AND model = $2
			) d`, dir[0], client.Model(), dir[1]).Scan(&n); err != nil {
			t.Fatalf("compare failure sets: %v", err)
		}
		if n != 0 {
			t.Errorf("%s failures not in %s = %d, want 0", dir[0], dir[1], n)
		}
	}
}

// TestEmbedStageTooLargeIsContentOnly proves the too_large pre-send predicate is
// content-only: the overBudget chunk's content token_count alone decides the
// failure, so both chunk_raw and chunk_header record a too_large row for it and
// the two failure sets are identical in both directions (D-082).
func TestEmbedStageTooLargeIsContentOnly(t *testing.T) {
	pool, _ := embedFixture(t, "extra/extra.go")
	f := newEmbedFake(t, "qwen3-embedding:0.6b")
	client := clientFor(t, f)

	id, content, _ := chunkRow(t, pool, "overBudget")
	if got := chunk.EstimateTokens(content); got <= chunk.EmbeddableContentMax {
		t.Fatalf("overBudget content tokens = %d, want > EmbeddableContentMax %d", got, chunk.EmbeddableContentMax)
	}
	runEmbedEachKind(t, pool, client, defaultEmbedKinds...)

	for _, kind := range defaultEmbedKinds {
		var reason string
		if err := pool.QueryRow(context.Background(), `
			SELECT reason FROM embed_failures WHERE target_kind = $1 AND target_id = $2 AND model = $3`,
			kind, id, client.Model()).Scan(&reason); err != nil {
			t.Fatalf("%s: read overBudget failure: %v", kind, err)
		}
		if reason != "too_large" {
			t.Errorf("%s overBudget reason = %q, want too_large", kind, reason)
		}
	}
	for _, dir := range [][2]string{{kindChunkRaw, kindChunkHeader}, {kindChunkHeader, kindChunkRaw}} {
		var n int64
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*) FROM (
				SELECT target_id FROM embed_failures WHERE target_kind = $1 AND model = $2
				EXCEPT
				SELECT target_id FROM embed_failures WHERE target_kind = $3 AND model = $2
			) d`, dir[0], client.Model(), dir[1]).Scan(&n); err != nil {
			t.Fatalf("compare failure sets: %v", err)
		}
		if n != 0 {
			t.Errorf("%s failures not in %s = %d, want 0", dir[0], dir[1], n)
		}
	}
}

// TestEmbedStageStaleHashRetriedMatchingSkipped proves a failure is skipped
// only while its stored input_hash matches: a matching failure is not
// re-attempted, and a changed chunk (stale hash) is retried (D-081).
func TestEmbedStageStaleHashRetriedMatchingSkipped(t *testing.T) {
	pool, _ := embedFixture(t, "basic/basic.go")
	f := newEmbedFake(t, "qwen3-embedding:0.6b")
	client := clientFor(t, f)
	id, content, _ := chunkRow(t, pool, "Plain")
	f.failInputs("too_long", content)

	if _, err := runEmbedStage(t, pool, client, defaultEmbedOptions(kindChunkRaw)); err == nil {
		t.Fatal("expected the 1% STOP on the first run")
	}
	sent := f.countInput(content)

	// A rerun with the same content skips the matching failure: no request.
	out, err := runEmbedStage(t, pool, client, defaultEmbedOptions(kindChunkRaw))
	if err != nil {
		t.Fatalf("matching-failure rerun: %v\n%s", err, out)
	}
	if got := parseKindCounts(t, out, kindChunkRaw); got.embedded != 0 {
		t.Errorf("matching-failure rerun counts = %+v, want embedded=0", got)
	}
	if f.countInput(content) != sent {
		t.Errorf("matching failure was re-sent: %d -> %d", sent, f.countInput(content))
	}

	// Changing the chunk changes its input hash, so the stale failure is
	// retried (and here succeeds).
	const changed = "func Plain() { return }"
	if _, err := pool.Exec(context.Background(), `UPDATE chunks SET content = $2 WHERE id = $1`, id, changed); err != nil {
		t.Fatalf("change chunk content: %v", err)
	}
	out, err = runEmbedStage(t, pool, client, defaultEmbedOptions(kindChunkRaw))
	if err != nil {
		t.Fatalf("stale-hash rerun: %v\n%s", err, out)
	}
	if got := parseKindCounts(t, out, kindChunkRaw); got.embedded != 1 {
		t.Errorf("stale-hash rerun embedded = %d, want 1", got.embedded)
	}
	hash, ok := embeddingsFor(t, pool, kindChunkRaw, id, client.Model())
	if !ok || hash != client.InputHash(changed) {
		t.Errorf("stale-hash rerun stored hash = %q (found %v), want %q", hash, ok, client.InputHash(changed))
	}
}

// TestEmbedStageRetryFailed proves --retry-failed re-attempts an item whose
// stored failure hash still matches.
func TestEmbedStageRetryFailed(t *testing.T) {
	pool, _ := embedFixture(t, "basic/basic.go")
	f := newEmbedFake(t, "qwen3-embedding:0.6b")
	client := clientFor(t, f)
	id, content, _ := chunkRow(t, pool, "Plain")
	f.failInputs("too_long", content)

	if _, err := runEmbedStage(t, pool, client, defaultEmbedOptions(kindChunkRaw)); err == nil {
		t.Fatal("expected the 1% STOP on the first run")
	}
	sent := f.countInput(content)

	if _, err := runEmbedStage(t, pool, client, embedOptions{Kinds: []string{kindChunkRaw}, RetryFailed: true}); err == nil {
		t.Fatal("expected the 1% STOP while the input still fails")
	}
	if f.countInput(content) <= sent {
		t.Errorf("--retry-failed did not re-attempt the item: %d -> %d", sent, f.countInput(content))
	}

	f.clearFailures("too_long")
	out, err := runEmbedStage(t, pool, client, embedOptions{Kinds: []string{kindChunkRaw}, RetryFailed: true})
	if err != nil {
		t.Fatalf("retry-failed run: %v\n%s", err, out)
	}
	if _, ok := embeddingsFor(t, pool, kindChunkRaw, id, client.Model()); !ok {
		t.Errorf("--retry-failed did not embed the previously failed chunk")
	}
}

// TestEmbedStageEdgeChangeReembedsHeaderOnly proves a graph edge change (after
// the chunk stage recomputes the stored header) re-embeds exactly the affected
// chunk_header rows and no chunk_raw row (D-081).
func TestEmbedStageEdgeChangeReembedsHeaderOnly(t *testing.T) {
	pool, root := embedFixture(t, "extra/extra.go")
	f := newEmbedFake(t, "qwen3-embedding:0.6b")
	client := clientFor(t, f)

	// The first run records the overBudget too_large failure and stops; the
	// vectors for the fitting chunks are stored. Run one kind at a time so the
	// STOP after chunk_raw does not skip chunk_header.
	runEmbedEachKind(t, pool, client, defaultEmbedKinds...)
	entryID, _, _ := chunkRow(t, pool, "Entry")
	rawBefore, _ := embeddingsFor(t, pool, kindChunkRaw, entryID, client.Model())

	if _, err := pool.Exec(context.Background(), `
		DELETE FROM edges e USING symbols caller, symbols callee
		WHERE e.caller_id = caller.id AND e.callee_id = callee.id
		  AND e.source = 'treesitter' AND caller.name = 'Entry' AND callee.name = 'FanOut'`); err != nil {
		t.Fatalf("delete edge: %v", err)
	}
	runChunkStage(t, pool, root, chunkOptions{})

	// The overBudget failure still matches, so neither kind stops and only the
	// affected headers re-embed.
	embeds := map[string]int{}
	for _, kind := range defaultEmbedKinds {
		out, err := runEmbedStage(t, pool, client, embedOptions{Kinds: []string{kind}})
		if err != nil {
			t.Fatalf("%s: second run: %v\n%s", kind, err, out)
		}
		embeds[kind] = parseKindCounts(t, out, kind).embedded
	}
	if embeds[kindChunkRaw] != 0 {
		t.Errorf("chunk_raw re-embedded %d rows, want 0 (a graph change must not touch raw)", embeds[kindChunkRaw])
	}
	if embeds[kindChunkHeader] != 2 {
		t.Errorf("chunk_header re-embedded %d rows, want the affected Entry and FanOut = 2", embeds[kindChunkHeader])
	}
	rawAfter, _ := embeddingsFor(t, pool, kindChunkRaw, entryID, client.Model())
	if rawAfter != rawBefore {
		t.Errorf("chunk_raw input_hash changed on a graph change: %q -> %q", rawBefore, rawAfter)
	}
}

// TestEmbedStageProbeFailureAborts proves a failing probe aborts before any
// vector or failure is written.
func TestEmbedStageProbeFailureAborts(t *testing.T) {
	pool, _ := embedFixture(t, "basic/basic.go")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	client, err := embed.NewOllama(srv.URL, "qwen3-embedding:0.6b", 0)
	if err != nil {
		t.Fatal(err)
	}

	out, err := runEmbedStage(t, pool, client, defaultEmbedOptions())
	if err == nil || !strings.Contains(err.Error(), "probe") {
		t.Fatalf("err = %v, want a probe error\n%s", err, out)
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM embeddings`); n != 0 {
		t.Errorf("vectors stored despite a failed probe: %d", n)
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM embed_failures`); n != 0 {
		t.Errorf("failures stored despite a failed probe: %d", n)
	}
}
