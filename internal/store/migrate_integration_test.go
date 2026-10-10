package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
)

var schemaTables = []string{"files", "symbols", "chunks", "edges", "summaries", "embeddings", "llm_cache", "traces"}

// These tests need a real Postgres with pgvector, such as the docker-compose
// one. Each runs in its own throwaway database (internal/testdb); `make test`
// sets REQUIRE_TEST_DB=1 so a missing configuration fails instead of skipping.

// TestMigrateCreatesTables checks a fresh database has every table and extension.
func TestMigrateCreatesTables(t *testing.T) {
	dbURL := testdb.Create(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := store.Migrate(ctx, dbURL); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	assertTables(ctx, t, conn)
	assertExtensions(ctx, t, conn)
}

// TestSearchTSVIsPlainColumn proves the second migration replaced the
// generated full-text column on chunks with a plain tsvector column: it is not
// generated, and a value inserted explicitly is matched by a text query. The
// inserts run in a transaction that is rolled back, so no rows are left behind.
func TestSearchTSVIsPlainColumn(t *testing.T) {
	dbURL := testdb.Create(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := store.Migrate(ctx, dbURL); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	var generated string
	if err := conn.QueryRow(ctx, `SELECT is_generated FROM information_schema.columns
		WHERE table_name = 'chunks' AND column_name = 'search_tsv'`).Scan(&generated); err != nil {
		t.Fatalf("read is_generated: %v", err)
	}
	if generated != "NEVER" {
		t.Errorf("chunks.search_tsv is_generated = %q, want NEVER (plain column)", generated)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var fileID int64
	if err := tx.QueryRow(ctx, `INSERT INTO files (path, package, sha, loc)
		VALUES ('a.go', 'pkg', 'deadbeef', 10) RETURNING id`).Scan(&fileID); err != nil {
		t.Fatalf("insert file: %v", err)
	}
	var symbolID int64
	if err := tx.QueryRow(ctx, `INSERT INTO symbols (file_id, kind, name, qualified_name, start_line, end_line)
		VALUES ($1, 'func', 'Foo', 'pkg.Foo', 1, 3) RETURNING id`, fileID).Scan(&symbolID); err != nil {
		t.Fatalf("insert symbol: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO chunks (symbol_id, file_id, part_idx, content, content_hash, token_count, search_tsv)
		VALUES ($1, $2, 0, 'unrelated text', 'hash', 2, to_tsvector('simple', 'foo bar'))`,
		symbolID, fileID); err != nil {
		t.Fatalf("insert chunk: %v", err)
	}

	var matched int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM chunks WHERE search_tsv @@ to_tsquery('simple', 'foo')`).Scan(&matched); err != nil {
		t.Fatalf("query: %v", err)
	}
	if matched != 1 {
		t.Errorf("chunks matching search_tsv @@ to_tsquery('simple','foo') = %d, want 1", matched)
	}
}

// TestSymbolsRawCallsAndSummaryUniqueness proves migration 00003 adds
// symbols.raw_calls, files.parsed_sha and summaries.reason, and that the
// unique index rejects a second summary for the same
// (level, target_id, prompt_version). It runs against a throwaway database and
// rolls back, so no rows are left behind.
func TestSymbolsRawCallsAndSummaryUniqueness(t *testing.T) {
	dbURL := testdb.Create(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := store.Migrate(ctx, dbURL); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var fileID int64
	if err := tx.QueryRow(ctx, `INSERT INTO files (path, package, sha, loc, parsed_sha)
		VALUES ('pkg/a.go', 'pkg', 'sha-unparsed', 10, 'sha-parsed') RETURNING id`).Scan(&fileID); err != nil {
		t.Fatalf("insert file: %v", err)
	}

	rawCalls := []string{"sched.cache.AssumePod", "klog.V(2).InfoS"}
	var symbolID int64
	if err := tx.QueryRow(ctx, `INSERT INTO symbols (file_id, kind, name, qualified_name, start_line, end_line, raw_calls)
		VALUES ($1, 'func', 'Foo', 'pkg.Foo', 1, 5, $2) RETURNING id`, fileID, rawCalls).Scan(&symbolID); err != nil {
		t.Fatalf("insert symbol: %v", err)
	}

	var gotParsed string
	if err := tx.QueryRow(ctx, `SELECT parsed_sha FROM files WHERE id = $1`, fileID).Scan(&gotParsed); err != nil {
		t.Fatalf("read parsed_sha: %v", err)
	}
	if gotParsed != "sha-parsed" {
		t.Errorf("files.parsed_sha = %q, want %q", gotParsed, "sha-parsed")
	}

	var gotRaw []string
	if err := tx.QueryRow(ctx, `SELECT raw_calls FROM symbols WHERE id = $1`, symbolID).Scan(&gotRaw); err != nil {
		t.Fatalf("read raw_calls: %v", err)
	}
	if !slices.Equal(gotRaw, rawCalls) {
		t.Errorf("symbols.raw_calls = %v, want %v", gotRaw, rawCalls)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO summaries (level, target_id, text, model, prompt_version, input_hash, status, reason)
		VALUES ('function', $1, NULL, 'qwen2.5-coder:7b', 'fn-v1', 'hash-1', 'failed', 'too_large')`,
		symbolID); err != nil {
		t.Fatalf("insert summary with reason: %v", err)
	}

	var gotReason string
	if err := tx.QueryRow(ctx, `SELECT reason FROM summaries WHERE target_id = $1`, symbolID).Scan(&gotReason); err != nil {
		t.Fatalf("read reason: %v", err)
	}
	if gotReason != "too_large" {
		t.Errorf("summaries.reason = %q, want %q", gotReason, "too_large")
	}

	_, err = tx.Exec(ctx, `INSERT INTO summaries (level, target_id, text, model, prompt_version, input_hash, status)
		VALUES ('function', $1, 'duplicate', 'qwen2.5-coder:7b', 'fn-v1', 'hash-2', 'done')`, symbolID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate summary error = %v, want unique violation (23505)", err)
	}
}

// TestMigrateUpDownUpInThrowawayDatabase exercises goose Up, Down (one step),
// then Up again in a throwaway database, so a rollback never touches dev data.
func TestMigrateUpDownUpInThrowawayDatabase(t *testing.T) {
	throwaway := testdb.Create(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if _, err := store.Migrate(ctx, throwaway); err != nil {
		t.Fatalf("up: %v", err)
	}
	assertSchema(ctx, t, throwaway)
	upVersion := gooseVersion(ctx, t, throwaway)
	if upVersion == 0 {
		t.Fatal("no goose version applied after up")
	}

	if _, err := store.MigrateDown(ctx, throwaway); err != nil {
		t.Fatalf("down: %v", err)
	}
	downVersion := gooseVersion(ctx, t, throwaway)
	if downVersion >= upVersion {
		t.Errorf("goose version after one down = %d, want < %d", downVersion, upVersion)
	}
	if upVersion == 1 {
		// Only 00001 exists, so a single Down rolled it back and dropped the schema.
		if hasTable(ctx, t, throwaway, "files") {
			t.Error("files table still present after rolling back 00001")
		}
	} else if !hasTable(ctx, t, throwaway, "files") {
		t.Error("files table missing after rolling back a later migration; 00001 should stay applied")
	}

	if _, err := store.Migrate(ctx, throwaway); err != nil {
		t.Fatalf("up again: %v", err)
	}
	assertSchema(ctx, t, throwaway)
	if got := gooseVersion(ctx, t, throwaway); got != upVersion {
		t.Errorf("goose version after re-up = %d, want %d", got, upVersion)
	}
}

func assertSchema(ctx context.Context, t *testing.T, dbURL string) {
	t.Helper()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	assertTables(ctx, t, conn)
	assertExtensions(ctx, t, conn)
}

func assertTables(ctx context.Context, t *testing.T, conn *pgx.Conn) {
	t.Helper()
	for _, table := range schemaTables {
		if !tableExists(ctx, t, conn, table) {
			t.Errorf("table %s does not exist after migration", table)
		}
	}
}

func assertExtensions(ctx context.Context, t *testing.T, conn *pgx.Conn) {
	t.Helper()
	for _, ext := range []string{"vector", "pg_trgm"} {
		var exists bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = $1)", ext).Scan(&exists); err != nil {
			t.Fatalf("check extension %s: %v", ext, err)
		}
		if !exists {
			t.Errorf("extension %s is not installed after migration", ext)
		}
	}
}

func tableExists(ctx context.Context, t *testing.T, conn *pgx.Conn, table string) bool {
	t.Helper()
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+table).Scan(&exists); err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	return exists
}

func hasTable(ctx context.Context, t *testing.T, dbURL, table string) bool {
	t.Helper()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	return tableExists(ctx, t, conn, table)
}

func gooseVersion(ctx context.Context, t *testing.T, dbURL string) int64 {
	t.Helper()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	var version int64
	if err := conn.QueryRow(ctx, "SELECT COALESCE(max(version_id), 0) FROM goose_db_version WHERE is_applied").Scan(&version); err != nil {
		t.Fatalf("read goose version: %v", err)
	}
	return version
}
