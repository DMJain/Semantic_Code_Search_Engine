package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

// writeSrc writes a fixture file under root at a slash-separated path.
func writeSrc(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedFiles walks root and syncs the files table, as `indexer walk` would.
func seedFiles(t *testing.T, pool *pgxpool.Pool, root string) {
	t.Helper()
	files, _, err := walk.Walk(root)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if _, _, err := walk.Sync(context.Background(), pool, files); err != nil {
		t.Fatalf("walk sync: %v", err)
	}
}

// runParseCorpus drives the parse stage against a throwaway pool and returns
// its stdout.
func runParseCorpus(t *testing.T, pool *pgxpool.Pool, root string, limit int) string {
	t.Helper()
	var buf bytes.Buffer
	if err := parseCorpus(context.Background(), pool, root, limit, true, &buf); err != nil {
		t.Fatalf("parseCorpus: %v", err)
	}
	return buf.String()
}

// scalarInt runs a query returning one integer.
func scalarInt(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return n
}

// symbolHash recomputes the summary input hash from the current symbol, the
// way the summarize stage will (code is represented by its line range here).
func symbolHash(t *testing.T, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	var signature, doc *string
	var start, end int
	err := pool.QueryRow(context.Background(),
		`SELECT signature, start_line, end_line, doc FROM symbols WHERE id = $1`, id).
		Scan(&signature, &start, &end, &doc)
	if err != nil {
		t.Fatalf("recompute input hash: %v", err)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%v|%d|%d|%v", deref(signature), start, end, deref(doc))))
	return hex.EncodeToString(sum[:])
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// TestParseCorpusStoresRawCalls proves raw_calls are written with the symbol,
// that a symbol with no calls stores an empty array rather than NULL, and that
// a re-parse leaves the stored list unchanged (deterministic upsert).
func TestParseCorpusStoresRawCalls(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	root := t.TempDir()
	writeSrc(t, root, "pkg/a/a.go",
		"package a\n\nfunc A() {\n\tsched.cache.AssumePod()\n\tlen(0)\n}\nfunc Clean() {}\n")
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)

	const qn = "k8s.io/kubernetes/pkg/a.A"
	var before []string
	if err := pool.QueryRow(ctx, `SELECT raw_calls FROM symbols WHERE qualified_name = $1`, qn).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if want := []string{"sched.cache.AssumePod"}; !slices.Equal(before, want) {
		t.Fatalf("raw_calls = %v, want %v", before, want)
	}

	var empty []string
	if err := pool.QueryRow(ctx,
		`SELECT raw_calls FROM symbols WHERE qualified_name = 'k8s.io/kubernetes/pkg/a.Clean'`).Scan(&empty); err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Errorf("Clean raw_calls = %v, want empty", empty)
	}

	// Pre-seed a different value so the re-parse must overwrite it through the
	// ON CONFLICT DO UPDATE path, not merely leave the first insert's value.
	if _, err := pool.Exec(ctx,
		`UPDATE symbols SET raw_calls = '{STALE}' WHERE qualified_name = $1`, qn); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE files SET parsed_sha = NULL`); err != nil {
		t.Fatal(err)
	}
	runParseCorpus(t, pool, root, 0)
	var after []string
	if err := pool.QueryRow(ctx, `SELECT raw_calls FROM symbols WHERE qualified_name = $1`, qn).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(after, before) {
		t.Errorf("re-parse did not overwrite raw_calls: got %v, want %v", after, before)
	}
}

// TestParseCorpusIdempotent proves a second run finds nothing stale and that no
// qualified name is duplicated across a package.
func TestParseCorpusIdempotent(t *testing.T) {
	pool := testdb.New(t)
	root := t.TempDir()
	writeSrc(t, root, "pkg/a/a.go", "package a\n\nfunc A() {}\nfunc init() {}\n")
	writeSrc(t, root, "pkg/b/b.go", "package b\n\nfunc B() {}\nfunc init() {}\n")
	seedFiles(t, pool, root)

	if out := runParseCorpus(t, pool, root, 0); !strings.Contains(out, "2 files to parse") {
		t.Fatalf("first run output = %q, want '2 files to parse'", out)
	}
	if out := runParseCorpus(t, pool, root, 0); !strings.Contains(out, "0 files to parse") {
		t.Fatalf("second run output = %q, want '0 files to parse'", out)
	}

	dupes := scalarInt(t, pool,
		`SELECT count(*) FROM (SELECT qualified_name FROM symbols GROUP BY 1 HAVING count(*) > 1) d`)
	if dupes != 0 {
		t.Errorf("%d duplicate qualified names, want 0", dupes)
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM symbols`); n != 4 {
		t.Errorf("symbol count = %d, want 4", n)
	}
}

// TestParseCorpusKeepsSymbolIDs proves a symbol's id survives a re-parse with
// the parsed marker cleared.
func TestParseCorpusKeepsSymbolIDs(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	root := t.TempDir()
	writeSrc(t, root, "pkg/a/a.go", "package a\n\nfunc A() {}\n")
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)

	const qn = "k8s.io/kubernetes/pkg/a.A"
	before := scalarInt(t, pool, `SELECT id FROM symbols WHERE qualified_name = $1`, qn)

	if _, err := pool.Exec(ctx, `UPDATE files SET parsed_sha = NULL`); err != nil {
		t.Fatal(err)
	}
	if out := runParseCorpus(t, pool, root, 0); !strings.Contains(out, "1 files to parse") {
		t.Fatalf("re-parse output = %q, want '1 files to parse'", out)
	}
	after := scalarInt(t, pool, `SELECT id FROM symbols WHERE qualified_name = $1`, qn)
	if before != after {
		t.Errorf("symbol id changed across re-parse: %d -> %d", before, after)
	}
}

// TestParseCorpusRemovesVanishedSymbolAndSummary proves a function removed from
// a file leaves no symbol and no function summary, so the orphan query is 0.
func TestParseCorpusRemovesVanishedSymbolAndSummary(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	root := t.TempDir()
	writeSrc(t, root, "pkg/a/a.go", "package a\n\nfunc A() {}\nfunc B() {}\n")
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)

	const qnB = "k8s.io/kubernetes/pkg/a.B"
	bID := scalarInt(t, pool, `SELECT id FROM symbols WHERE qualified_name = $1`, qnB)
	if _, err := pool.Exec(ctx,
		`INSERT INTO summaries (level, target_id, text, model, prompt_version, input_hash, status)
		 VALUES ('function', $1, 'summary of B', 'm', 'fn-v1', 'hash', 'done')`, bID); err != nil {
		t.Fatal(err)
	}

	writeSrc(t, root, "pkg/a/a.go", "package a\n\nfunc A() {}\n")
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)

	if n := scalarInt(t, pool, `SELECT count(*) FROM symbols WHERE qualified_name = $1`, qnB); n != 0 {
		t.Errorf("vanished symbol B still present: %d", n)
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM summaries WHERE target_id = $1`, bID); n != 0 {
		t.Errorf("summary for vanished symbol B still present: %d", n)
	}
	orphans := scalarInt(t, pool,
		`SELECT count(*) FROM summaries s
		 WHERE s.level = 'function'
		   AND NOT EXISTS (SELECT 1 FROM symbols y WHERE y.id = s.target_id)`)
	if orphans != 0 {
		t.Errorf("orphan function summaries = %d, want 0", orphans)
	}
}

// TestParseCorpusRemovesAllSymbolsFromFile proves that when a file is left
// with no symbols the per-file delete still removes every old symbol (the
// seen list is empty, which must delete rather than no-op).
func TestParseCorpusRemovesAllSymbolsFromFile(t *testing.T) {
	pool := testdb.New(t)
	root := t.TempDir()
	writeSrc(t, root, "pkg/a/a.go", "package a\n\nfunc A() {}\nfunc B() {}\n")
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)

	writeSrc(t, root, "pkg/a/a.go", "package a\n\nvar X = 1\n")
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)

	if n := scalarInt(t, pool, `SELECT count(*) FROM symbols WHERE file_id = (SELECT id FROM files WHERE path = 'pkg/a/a.go')`); n != 0 {
		t.Errorf("file still has %d symbols after all functions removed, want 0", n)
	}
}

// TestParseCorpusDuplicateShiftMakesSummaryStale proves that inserting an extra
// init shifts the #n numbering so a summary stored against init#1 now points at
// different code, which the recomputed input hash detects.
func TestParseCorpusDuplicateShiftMakesSummaryStale(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	root := t.TempDir()
	writeSrc(t, root, "pkg/p/a.go", "package pkg\n\nfunc init() {}\nfunc Keep() {}\n")
	writeSrc(t, root, "pkg/p/b.go", "package pkg\n\nfunc init() {}\n")
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)

	const qn = "k8s.io/kubernetes/pkg/p.init#1"
	shifted := scalarInt(t, pool, `SELECT id FROM symbols WHERE qualified_name = $1`, qn)
	before := symbolHash(t, pool, shifted)
	if _, err := pool.Exec(ctx,
		`INSERT INTO summaries (level, target_id, text, model, prompt_version, input_hash, status)
		 VALUES ('function', $1, 'summary of init#1', 'm', 'fn-v1', $2, 'done')`, shifted, before); err != nil {
		t.Fatal(err)
	}

	// A new init before the existing one pushes file b's init from #1 to #2.
	writeSrc(t, root, "pkg/p/a.go", "package pkg\n\nfunc init() {}\nfunc init() {}\nfunc Keep() {}\n")
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)

	after := symbolHash(t, pool, shifted)
	if before == after {
		t.Errorf("recomputed input hash unchanged (%s); the shifted summary should be stale", after)
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM summaries WHERE target_id = $1`, shifted); n != 1 {
		t.Errorf("summary row count = %d, want 1 (stale, not deleted)", n)
	}
}

// TestParseCorpusResumeMatchesUninterrupted proves that resuming after an
// interrupted run (simulated with a package limit) yields the same symbol count
// as an uninterrupted run.
func TestParseCorpusResumeMatchesUninterrupted(t *testing.T) {
	root := t.TempDir()
	writeSrc(t, root, "pkg/a/a.go", "package a\n\nfunc A1() {}\nfunc A2() {}\n")
	writeSrc(t, root, "pkg/b/b.go", "package b\n\nfunc B() {}\n")

	// Uninterrupted baseline.
	full := testdb.New(t)
	seedFiles(t, full, root)
	runParseCorpus(t, full, root, 0)
	want := scalarInt(t, full, `SELECT count(*) FROM symbols`)

	// Interrupted run: stop after the first package, then resume.
	resumed := testdb.New(t)
	seedFiles(t, resumed, root)
	if out := runParseCorpus(t, resumed, root, 1); !strings.Contains(out, "parsed 1 files") {
		t.Fatalf("limited run output = %q, want only the first package parsed", out)
	}
	if n := scalarInt(t, resumed,
		`SELECT count(*) FROM symbols WHERE qualified_name LIKE 'k8s.io/kubernetes/pkg/b.%'`); n != 0 {
		t.Fatalf("package b parsed before resume: %d symbols", n)
	}
	runParseCorpus(t, resumed, root, 0)
	got := scalarInt(t, resumed, `SELECT count(*) FROM symbols`)
	if got != want {
		t.Errorf("resumed symbol count = %d, want %d (uninterrupted)", got, want)
	}
}

// addDoneSummary stores a done function summary for a symbol.
func addDoneSummary(t *testing.T, pool *pgxpool.Pool, id int64, text string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO summaries (level, target_id, text, model, prompt_version, input_hash, status)
		 VALUES ('function', $1, $2, 'm', 'fn-v1', 'hash', 'done')`, id, text); err != nil {
		t.Fatal(err)
	}
}

// TestParseCorpusSymbolMoveKeepsSummary proves a symbol moved between files of
// one package keeps its id and summary whichever of the two files is parsed
// first (files are parsed in path order, so both directions are exercised).
func TestParseCorpusSymbolMoveKeepsSummary(t *testing.T) {
	const qn = "k8s.io/kubernetes/pkg/p.Moved"
	for _, tc := range []struct{ name, from, to string }{
		{"old file parsed first", "a.go", "b.go"},
		{"new file parsed first", "b.go", "a.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := testdb.New(t)
			root := t.TempDir()
			writeSrc(t, root, "pkg/p/"+tc.from, "package p\n\nfunc Moved() {}\n")
			writeSrc(t, root, "pkg/p/"+tc.to, "package p\n\nfunc Other() {}\n")
			seedFiles(t, pool, root)
			runParseCorpus(t, pool, root, 0)

			id := scalarInt(t, pool, `SELECT id FROM symbols WHERE qualified_name = $1`, qn)
			addDoneSummary(t, pool, id, "summary of Moved")

			writeSrc(t, root, "pkg/p/"+tc.from, "package p\n\nfunc Stay() {}\n")
			writeSrc(t, root, "pkg/p/"+tc.to, "package p\n\nfunc Other() {}\nfunc Moved() {}\n")
			seedFiles(t, pool, root)
			runParseCorpus(t, pool, root, 0)

			if got := scalarInt(t, pool, `SELECT id FROM symbols WHERE qualified_name = $1`, qn); got != id {
				t.Errorf("moved symbol id = %d, want %d", got, id)
			}
			if n := scalarInt(t, pool,
				`SELECT count(*) FROM summaries WHERE level = 'function' AND target_id = $1 AND text = 'summary of Moved'`, id); n != 1 {
				t.Errorf("summary rows for moved symbol = %d, want 1", n)
			}
			if n := scalarInt(t, pool,
				`SELECT count(*) FROM symbols s JOIN files f ON f.id = s.file_id WHERE s.qualified_name = $1 AND f.path = $2`,
				qn, "pkg/p/"+tc.to); n != 1 {
				t.Errorf("moved symbol not attached to %s", tc.to)
			}
		})
	}
}

// TestParseCorpusDeletedSymbolLosesSummaryAcrossFiles proves that in a
// multi-file package a symbol absent from the whole parse loses its symbol row
// and summary while a sibling symbol keeps both.
func TestParseCorpusDeletedSymbolLosesSummaryAcrossFiles(t *testing.T) {
	pool := testdb.New(t)
	root := t.TempDir()
	writeSrc(t, root, "pkg/p/a.go", "package p\n\nfunc Gone() {}\n")
	writeSrc(t, root, "pkg/p/b.go", "package p\n\nfunc Kept() {}\n")
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)

	gone := scalarInt(t, pool, `SELECT id FROM symbols WHERE qualified_name = 'k8s.io/kubernetes/pkg/p.Gone'`)
	kept := scalarInt(t, pool, `SELECT id FROM symbols WHERE qualified_name = 'k8s.io/kubernetes/pkg/p.Kept'`)
	addDoneSummary(t, pool, gone, "summary of Gone")
	addDoneSummary(t, pool, kept, "summary of Kept")

	writeSrc(t, root, "pkg/p/a.go", "package p\n\nvar X = 1\n")
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)

	if n := scalarInt(t, pool, `SELECT count(*) FROM symbols WHERE id = $1`, gone); n != 0 {
		t.Errorf("deleted symbol still present: %d", n)
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM summaries WHERE target_id = $1`, gone); n != 0 {
		t.Errorf("summary of deleted symbol still present: %d", n)
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM summaries WHERE target_id = $1`, kept); n != 1 {
		t.Errorf("summary of kept symbol = %d rows, want 1", n)
	}
}

// TestParseCorpusReparseChangesNoRows proves re-parsing an unchanged tree
// leaves every symbol and summary row exactly as it was.
func TestParseCorpusReparseChangesNoRows(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	root := t.TempDir()
	writeSrc(t, root, "pkg/p/a.go", "package p\n\nfunc A() { B() }\nfunc init() {}\n")
	writeSrc(t, root, "pkg/p/b.go", "package p\n\nfunc B() {}\nfunc init() {}\n")
	seedFiles(t, pool, root)
	runParseCorpus(t, pool, root, 0)
	addDoneSummary(t, pool, scalarInt(t, pool, `SELECT id FROM symbols WHERE name = 'A'`), "summary of A")

	snapshot := func() string {
		var s string
		err := pool.QueryRow(ctx, `
			SELECT coalesce((SELECT string_agg(y::text, ';' ORDER BY y.id) FROM symbols y), '') || '#' ||
			       coalesce((SELECT string_agg(m::text, ';' ORDER BY m.id) FROM summaries m), '')`).Scan(&s)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := snapshot()
	if _, err := pool.Exec(ctx, `UPDATE files SET parsed_sha = NULL`); err != nil {
		t.Fatal(err)
	}
	runParseCorpus(t, pool, root, 0)
	if after := snapshot(); after != before {
		t.Errorf("rows changed by re-parse:\nbefore %s\nafter  %s", before, after)
	}
}
