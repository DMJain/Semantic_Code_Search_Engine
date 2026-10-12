package summarize

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

// funcSpan is one function's 1-based inclusive source range.
type funcSpan struct {
	name       string
	start, end int
}

// funcBlock is a four-line function, so end-start == 3 (the eligible minimum).
func funcBlock(name string) string {
	return fmt.Sprintf("func %s() {\n\tv := %q\n\t_ = v\n}\n", name, name)
}

// funcFile builds a fixture file of eligible functions and their spans.
func funcFile(names ...string) (string, []funcSpan) {
	var b strings.Builder
	b.WriteString("package fixture\n\n")
	line := 3
	spans := make([]funcSpan, 0, len(names))
	for _, name := range names {
		block := funcBlock(name)
		start := line
		end := line + strings.Count(block, "\n") - 1
		spans = append(spans, funcSpan{name: name, start: start, end: end})
		b.WriteString(block)
		line = end + 1
	}
	return b.String(), spans
}

// largeFile builds a single function with a large body, so its prompt estimate
// exceeds a small num_ctx and trips the too_large guard.
func largeFile(name string, body int) (string, []funcSpan) {
	var b strings.Builder
	b.WriteString("package fixture\n\nfunc " + name + "() {\n")
	start := 3
	for i := 0; i < body; i++ {
		fmt.Fprintf(&b, "\tv%d := %d\n", i, i)
	}
	b.WriteString("}\n")
	return b.String(), []funcSpan{{name: name, start: start, end: start + body + 1}}
}

// seedSource writes source under dir and inserts one in-slice files row plus
// one func symbol per span. Tests use it in place of walk+parse to stay focused
// on the summarize runner.
func seedSource(t *testing.T, pool *pgxpool.Pool, dir, rel, source string, spans []funcSpan) {
	t.Helper()
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pkg := strings.TrimSuffix(rel, ".go")
	var fileID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO files (path, package, sha, loc, in_slice)
		VALUES ($1, $2, 'sha', $3, true) RETURNING id`,
		rel, pkg, strings.Count(source, "\n")).Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	for _, s := range spans {
		if _, err := pool.Exec(ctx, `
			INSERT INTO symbols (file_id, kind, name, qualified_name, signature, start_line, end_line, raw_calls)
			VALUES ($1, 'func', $2, $3, $4, $5, $6, '{}')`,
			fileID, s.name, pkg+"."+s.name, "func "+s.name+"()", s.start, s.end); err != nil {
			t.Fatal(err)
		}
	}
}

// fakeOllama is a stand-in Ollama server that counts requests and runs onCall
// with the request ordinal.
type fakeOllama struct {
	*httptest.Server
	requests atomic.Int64
}

func newFakeOllama(t *testing.T, onCall func(n int64)) *fakeOllama {
	t.Helper()
	f := &fakeOllama{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if onCall != nil {
			onCall(f.requests.Add(1))
		} else {
			f.requests.Add(1)
		}
		fmt.Fprint(w, `{"message":{"role":"assistant","content":"Does the thing."},"prompt_eval_count":10}`)
	}))
	t.Cleanup(f.Close)
	return f
}

func newRunner(pool *pgxpool.Pool, url, dir string, numCtx int, out io.Writer) *Runner {
	return &Runner{
		Pool:    pool,
		Client:  NewClient(url, "test-model", numCtx),
		KubeDir: dir,
		Model:   "test-model",
		Out:     out,
	}
}

func statusCounts(t *testing.T, pool *pgxpool.Pool) map[string]int {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT status, count(*) FROM summaries WHERE level = 'function' GROUP BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			t.Fatal(err)
		}
		counts[status] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return counts
}

func duplicateSummaries(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var n int64
	err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM (
			SELECT 1 FROM summaries GROUP BY level, target_id, prompt_version HAVING count(*) > 1) d`).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRunLimitThenRerunNoHTTP(t *testing.T) {
	pool := testdb.New(t)
	dir := t.TempDir()
	names := make([]string, 20)
	for i := range names {
		names[i] = fmt.Sprintf("F%02d", i)
	}
	source, spans := funcFile(names...)
	seedSource(t, pool, dir, "pkg/scheduler/a.go", source, spans)

	fake := newFakeOllama(t, nil)
	r := newRunner(pool, fake.URL, dir, 4096, io.Discard)

	st, err := r.Run(context.Background(), RunOptions{Limit: 20})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if st.Done != 20 {
		t.Fatalf("done = %d, want 20", st.Done)
	}
	if got := fake.requests.Load(); got != 20 {
		t.Fatalf("first run HTTP calls = %d, want 20", got)
	}
	if counts := statusCounts(t, pool); counts["done"] != 20 {
		t.Fatalf("status counts = %v, want done 20", counts)
	}

	st2, err := r.Run(context.Background(), RunOptions{Limit: 20})
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if st2.Done != 0 || st2.Skipped != 20 {
		t.Fatalf("rerun done = %d skipped = %d, want 0/20", st2.Done, st2.Skipped)
	}
	if got := fake.requests.Load(); got != 20 {
		t.Fatalf("rerun made %d extra HTTP calls, want 0", got-20)
	}
	if n := duplicateSummaries(t, pool); n != 0 {
		t.Fatalf("duplicate (level,target,prompt) rows = %d, want 0", n)
	}
}

func TestRunInterruptResume(t *testing.T) {
	pool := testdb.New(t)
	dir := t.TempDir()
	names := make([]string, 10)
	for i := range names {
		names[i] = fmt.Sprintf("G%02d", i)
	}
	source, spans := funcFile(names...)
	seedSource(t, pool, dir, "pkg/kubelet/a.go", source, spans)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// S1-15 pause/resume by counts: cancel once at least five items are done
	// (request 6), mid-run. The in-flight item still finishes because the
	// write/request context is not the cancelled one.
	const pauseAt = 6
	fake := newFakeOllama(t, func(n int64) {
		if n == pauseAt {
			cancel()
		}
	})
	r := newRunner(pool, fake.URL, dir, 4096, io.Discard)

	st, err := r.Run(ctx, RunOptions{})
	if err != nil {
		t.Fatalf("interrupted run: %v", err)
	}
	if st.Done != pauseAt {
		t.Fatalf("interrupted run done = %d, want exactly %d (cancel at request %d, concurrency 1)", st.Done, pauseAt, pauseAt)
	}
	// No partial row survives an interrupt: a done row carries text, a failed
	// row carries a reason and no text, a pending row carries no text.
	var partial int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM summaries
		WHERE (status = 'done' AND text IS NULL)
		   OR (status = 'failed' AND (reason IS NULL OR text IS NOT NULL))
		   OR (status = 'pending' AND text IS NOT NULL)`).Scan(&partial); err != nil {
		t.Fatal(err)
	}
	if partial != 0 {
		t.Fatalf("partial rows after interrupt = %d, want 0", partial)
	}
	if n := duplicateSummaries(t, pool); n != 0 {
		t.Fatalf("duplicate rows after interrupt = %d, want 0", n)
	}

	st2, err := r.Run(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if st2.Done+st2.Skipped != 10 {
		t.Fatalf("resume done+skipped = %d, want 10", st2.Done+st2.Skipped)
	}
	if counts := statusCounts(t, pool); counts["done"] != 10 || counts["pending"] != 0 {
		t.Fatalf("after resume status counts = %v, want done 10 pending 0", counts)
	}
	if n := duplicateSummaries(t, pool); n != 0 {
		t.Fatalf("duplicate rows = %d, want 0", n)
	}
}

func TestRunTooLargeNoHTTP(t *testing.T) {
	pool := testdb.New(t)
	dir := t.TempDir()
	source, spans := largeFile("Big", 60)
	seedSource(t, pool, dir, "pkg/controller/big.go", source, spans)

	fake := newFakeOllama(t, nil)
	// A tiny context makes the request estimate exceed 0.8 x num_ctx.
	r := newRunner(pool, fake.URL, dir, 64, io.Discard)

	st, err := r.Run(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if st.Failed != 1 {
		t.Fatalf("failed = %d, want 1", st.Failed)
	}
	if got := fake.requests.Load(); got != 0 {
		t.Fatalf("too_large made %d HTTP calls, want 0", got)
	}
	var status, reason string
	var text *string
	if err := pool.QueryRow(context.Background(),
		`SELECT status, reason, text FROM summaries WHERE level = 'function'`).Scan(&status, &reason, &text); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("status = %q, want failed", status)
	}
	if reason != ReasonTooLarge {
		t.Fatalf("reason = %q, want %q", reason, ReasonTooLarge)
	}
	if text != nil {
		t.Fatalf("text = %q, want NULL", *text)
	}
}

func TestRunRetryFailed(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dir := t.TempDir()
	source, spans := funcFile("Retry")
	seedSource(t, pool, dir, "pkg/scheduler/retry.go", source, spans)

	var id int64
	if err := pool.QueryRow(ctx,
		`SELECT id FROM symbols WHERE qualified_name = 'pkg/scheduler/retry.Retry'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	fake := newFakeOllama(t, nil)
	r := newRunner(pool, fake.URL, dir, 4096, io.Discard)

	// The failed row carries the symbol's current hash: a failure on unchanged
	// input is what --retry-failed exists for (a changed hash is stale; S1C-10).
	rows, err := r.eligible(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("eligible rows = %d err = %v, want 1", len(rows), err)
	}
	in, err := BuildInput(dir, rows[0].path, rows[0].sym)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO summaries (level, target_id, text, model, prompt_version, input_hash, status, reason)
		 VALUES ('function', $1, NULL, 'test-model', $2, $4, 'failed', $3)`,
		id, PromptVersion, ReasonHTTPError, in.Hash()); err != nil {
		t.Fatal(err)
	}

	// Without --retry-failed the failed row is skipped and the model is not called.
	st, err := r.Run(ctx, RunOptions{})
	if err != nil {
		t.Fatalf("run without retry: %v", err)
	}
	if st.Done != 0 || st.Skipped != 1 {
		t.Fatalf("without retry done = %d skipped = %d, want 0/1", st.Done, st.Skipped)
	}
	if got := fake.requests.Load(); got != 0 {
		t.Fatalf("without retry made %d HTTP calls, want 0", got)
	}

	// With --retry-failed the same row is re-picked and re-sent.
	st2, err := r.Run(ctx, RunOptions{RetryFailed: true})
	if err != nil {
		t.Fatalf("run with retry: %v", err)
	}
	if st2.Done != 1 {
		t.Fatalf("with retry done = %d, want 1", st2.Done)
	}
	if got := fake.requests.Load(); got != 1 {
		t.Fatalf("with retry HTTP calls = %d, want 1", got)
	}
	var status string
	var text *string
	if err := pool.QueryRow(ctx,
		`SELECT status, text FROM summaries WHERE level = 'function' AND target_id = $1`, id).
		Scan(&status, &text); err != nil {
		t.Fatal(err)
	}
	if status != "done" || text == nil {
		t.Fatalf("after retry status = %q text = %v, want done with text", status, text)
	}
}

func TestRunFreshnessRecomputesHash(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	dir := t.TempDir()
	source, spans := funcFile("Fresh")
	seedSource(t, pool, dir, "pkg/scheduler/fresh.go", source, spans)

	const qn = "pkg/scheduler/fresh.Fresh"
	// A per-call body proves the text is rewritten, not merely kept.
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"message":{"role":"assistant","content":"summary v%d"},"prompt_eval_count":10}`, calls.Add(1))
	}))
	t.Cleanup(srv.Close)
	r := newRunner(pool, srv.URL, dir, 4096, io.Discard)

	if st, err := r.Run(ctx, RunOptions{}); err != nil || st.Done != 1 {
		t.Fatalf("first run done = %d err = %v, want done 1", st.Done, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("first run HTTP calls = %d, want 1", got)
	}
	var beforeHash, beforeText string
	if err := pool.QueryRow(ctx,
		`SELECT sm.input_hash, sm.text FROM summaries sm JOIN symbols s ON s.id = sm.target_id
		 WHERE sm.level = 'function' AND s.qualified_name = $1`, qn).Scan(&beforeHash, &beforeText); err != nil {
		t.Fatal(err)
	}

	// An unchanged row is fresh: skipped, no new request.
	if st, err := r.Run(ctx, RunOptions{}); err != nil || st.Done != 0 || st.Skipped != 1 {
		t.Fatalf("unchanged rerun done = %d skipped = %d err = %v, want 0/1", st.Done, st.Skipped, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("unchanged rerun made %d extra HTTP calls, want 0", got-1)
	}

	// The stored symbol changes, so the recomputed input_hash differs and the
	// done row is stale: it is re-summarized and the stored hash/text update.
	if _, err := pool.Exec(ctx, `UPDATE symbols SET raw_calls = '{Changed.Call}' WHERE qualified_name = $1`, qn); err != nil {
		t.Fatal(err)
	}
	if st, err := r.Run(ctx, RunOptions{}); err != nil || st.Done != 1 || st.Skipped != 0 {
		t.Fatalf("stale rerun done = %d skipped = %d err = %v, want 1/0", st.Done, st.Skipped, err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("stale rerun HTTP calls = %d, want 2", got)
	}
	var afterHash, afterText string
	if err := pool.QueryRow(ctx,
		`SELECT sm.input_hash, sm.text FROM summaries sm JOIN symbols s ON s.id = sm.target_id
		 WHERE sm.level = 'function' AND s.qualified_name = $1`, qn).Scan(&afterHash, &afterText); err != nil {
		t.Fatal(err)
	}
	if afterHash == beforeHash {
		t.Fatalf("input_hash unchanged (%s); the changed symbol should be stale", afterHash)
	}
	if afterText == beforeText {
		t.Fatalf("text unchanged (%q); the row should have been re-summarized", afterText)
	}
}

func TestRunConcurrency2(t *testing.T) {
	pool := testdb.New(t)
	dir := t.TempDir()
	names := make([]string, 12)
	for i := range names {
		names[i] = fmt.Sprintf("H%02d", i)
	}
	source, spans := funcFile(names...)
	seedSource(t, pool, dir, "staging/src/k8s.io/client-go/cache/a.go", source, spans)

	fake := newFakeOllama(t, nil)
	r := newRunner(pool, fake.URL, dir, 4096, io.Discard)

	st, err := r.Run(context.Background(), RunOptions{Concurrency: 2})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if st.Done != 12 {
		t.Fatalf("done = %d, want 12", st.Done)
	}
	if n := duplicateSummaries(t, pool); n != 0 {
		t.Fatalf("duplicate rows = %d, want 0", n)
	}
}

func TestRunPrintSampleByteIdentical(t *testing.T) {
	pool := testdb.New(t)
	dir := t.TempDir()
	for i, root := range walk.SlicePrefixes {
		names := make([]string, 5)
		for j := range names {
			names[j] = fmt.Sprintf("S%d_%d", i, j)
		}
		source, spans := funcFile(names...)
		seedSource(t, pool, dir, root+"/sample.go", source, spans)
	}

	var first, second bytes.Buffer
	r := newRunner(pool, "http://127.0.0.1:0", dir, 4096, &first)
	if _, err := r.Run(context.Background(), RunOptions{Sample: 8, Seed: 3, PrintSample: true}); err != nil {
		t.Fatalf("first sample: %v", err)
	}
	r.Out = &second
	if _, err := r.Run(context.Background(), RunOptions{Sample: 8, Seed: 3, PrintSample: true}); err != nil {
		t.Fatalf("second sample: %v", err)
	}
	if first.String() != second.String() {
		t.Fatalf("sample output differs:\n%s\n---\n%s", first.String(), second.String())
	}
	// 1 header line plus one id per line.
	if lines := strings.Count(strings.TrimSpace(first.String()), "\n"); lines != 8 {
		t.Fatalf("sample output has %d id lines, want 8:\n%s", lines, first.String())
	}
}

func TestSampleStratification(t *testing.T) {
	cands, rootOf := stratifiedCandidates(t, 40, 30, 20, 10)
	got := Sample(cands, 37, 7)
	if len(got) != 37 {
		t.Fatalf("sample size = %d, want 37", len(got))
	}
	perRoot := map[string]int{}
	for _, id := range got {
		perRoot[rootOf[id]]++
	}
	// Largest remainder: floor(37*40/100)=14, floor(37*30/100)=11,
	// floor(37*20/100)=7, floor(37*10/100)=3; two leftovers go to the largest
	// remainders, roots 0 (80) and 3 (70).
	want := map[string]int{
		"pkg/scheduler":                15,
		"pkg/kubelet":                  11,
		"pkg/controller":               7,
		"staging/src/k8s.io/client-go": 4,
	}
	for root, n := range want {
		if perRoot[root] != n {
			t.Errorf("root %s got %d, want %d", root, perRoot[root], n)
		}
	}
}

func TestSampleDeterministic(t *testing.T) {
	cands, _ := stratifiedCandidates(t, 40, 30, 20, 10)
	first := Sample(cands, 37, 7)

	// Same candidates in the reverse order must give the same result: the
	// sampler sorts by qualified name before the seeded shuffle.
	reversed := make([]Candidate, len(cands))
	for i, c := range cands {
		reversed[len(cands)-1-i] = c
	}
	second := Sample(reversed, 37, 7)
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("sample not deterministic:\n%v\n%v", first, second)
	}
	if third := Sample(cands, 37, 7); fmt.Sprint(third) != fmt.Sprint(first) {
		t.Fatalf("sample not repeatable:\n%v\n%v", first, third)
	}
}

func TestSampleFewerCandidatesThanN(t *testing.T) {
	cands, _ := stratifiedCandidates(t, 2, 2, 2, 2)
	got := Sample(cands, 100, 1)
	if len(got) != len(cands) {
		t.Fatalf("sample size = %d, want all %d", len(got), len(cands))
	}
	seen := map[int64]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("id %d returned twice", id)
		}
		seen[id] = true
	}
}

func TestSample200DeterministicProportional(t *testing.T) {
	cands, rootOf := stratifiedCandidates(t, 101, 73, 51, 33)
	first := Sample(cands, 200, 3)
	second := Sample(cands, 200, 3)
	if !slices.Equal(first, second) {
		t.Fatalf("Sample twice differs:\n%v\n%v", first, second)
	}
	if len(first) != 200 {
		t.Fatalf("sample size = %d, want 200", len(first))
	}
	perRoot := map[string]int{}
	for _, id := range first {
		perRoot[rootOf[id]]++
	}
	// Largest remainder over 101/73/51/33 (total 258): the two leftover slots
	// go to the largest remainders, kubelet (152) and client-go (150).
	want := map[string]int{
		"pkg/scheduler":                78,
		"pkg/kubelet":                  57,
		"pkg/controller":               39,
		"staging/src/k8s.io/client-go": 26,
	}
	for root, n := range want {
		if perRoot[root] != n {
			t.Errorf("root %s got %d, want %d", root, perRoot[root], n)
		}
	}
}

// stratifiedCandidates builds deterministically named candidates with the given
// per-root counts and returns them with an id->root map.
func stratifiedCandidates(t *testing.T, sched, kubelet, controller, clientgo int) ([]Candidate, map[int64]string) {
	t.Helper()
	counts := map[string]int{
		"pkg/scheduler":                sched,
		"pkg/kubelet":                  kubelet,
		"pkg/controller":               controller,
		"staging/src/k8s.io/client-go": clientgo,
	}
	var cands []Candidate
	rootOf := map[int64]string{}
	var id int64
	for _, root := range walk.SlicePrefixes {
		for i := 0; i < counts[root]; i++ {
			id++
			cands = append(cands, Candidate{
				ID:            id,
				QualifiedName: fmt.Sprintf("%s.fn%04d", root, i),
				Root:          root,
			})
			rootOf[id] = root
		}
	}
	return cands, rootOf
}
