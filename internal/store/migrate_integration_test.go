package store_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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

// TestSummariesCompletedAtMigrationKeepsRows proves the completion-timestamp
// migration is reversible and non-destructive: summaries rows present before
// Down survive Down and the following Up, the column exists only while the
// migration is applied, and rows written before it have a NULL timestamp.
func TestSummariesCompletedAtMigrationKeepsRows(t *testing.T) {
	dbURL := testdb.Create(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if _, err := store.Migrate(ctx, dbURL); err != nil {
		t.Fatalf("up: %v", err)
	}
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	if !summariesHasCompletedAt(ctx, t, conn) {
		t.Fatal("summaries.completed_at missing after up")
	}
	if _, err := conn.Exec(ctx, `INSERT INTO summaries (level, target_id, text, model, prompt_version, input_hash, status)
		VALUES ('function', 1, 'x', 'm', 'fn-v1', 'h', 'done')`); err != nil {
		t.Fatalf("insert summary: %v", err)
	}
	var nullTS bool
	if err := conn.QueryRow(ctx, `SELECT completed_at IS NULL FROM summaries WHERE target_id = 1`).Scan(&nullTS); err != nil || !nullTS {
		t.Fatalf("completed_at default null = %v (err %v), want true", nullTS, err)
	}

	// Roll back every migration newer than the completed_at one (00004); the
	// loop keeps the test valid as later migrations are added.
	for i := 0; i < 10 && summariesHasCompletedAt(ctx, t, conn); i++ {
		if _, err := store.MigrateDown(ctx, dbURL); err != nil {
			t.Fatalf("down: %v", err)
		}
	}
	if summariesHasCompletedAt(ctx, t, conn) {
		t.Error("summaries.completed_at still present after down")
	}
	assertSummaryRowCount(ctx, t, conn, 1)

	if _, err := store.Migrate(ctx, dbURL); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if !summariesHasCompletedAt(ctx, t, conn) {
		t.Error("summaries.completed_at missing after re-up")
	}
	assertSummaryRowCount(ctx, t, conn, 1)
}

func summariesHasCompletedAt(ctx context.Context, t *testing.T, conn *pgx.Conn) bool {
	t.Helper()
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = 'summaries' AND column_name = 'completed_at' AND is_nullable = 'YES'
		AND data_type = 'timestamp with time zone')`).Scan(&ok); err != nil {
		t.Fatalf("check completed_at: %v", err)
	}
	return ok
}

func assertSummaryRowCount(ctx context.Context, t *testing.T, conn *pgx.Conn, want int) {
	t.Helper()
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM summaries`).Scan(&n); err != nil {
		t.Fatalf("count summaries: %v", err)
	}
	if n != want {
		t.Errorf("summaries rows = %d, want %d", n, want)
	}
}

// TestEdgesMigrationShape proves migration 00005 adds call_line, call_kind and
// unresolved_reason, allows the unresolved resolution, enforces every shape and
// vocabulary CHECK, dedupes identical edges and cascades on callee delete.
func TestEdgesMigrationShape(t *testing.T) {
	dbURL := testdb.Create(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if _, err := store.Migrate(ctx, dbURL); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	var fileID, callerID, calleeID int64
	if err := conn.QueryRow(ctx, `INSERT INTO files (path, package, sha, loc)
		VALUES ('pkg/a.go', 'pkg', 'sha', 3) RETURNING id`).Scan(&fileID); err != nil {
		t.Fatalf("insert file: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO symbols (file_id, kind, name, qualified_name, start_line, end_line)
		VALUES ($1, 'func', 'A', 'pkg.A', 1, 3) RETURNING id`, fileID).Scan(&callerID); err != nil {
		t.Fatalf("insert caller: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO symbols (file_id, kind, name, qualified_name, start_line, end_line)
		VALUES ($1, 'func', 'B', 'pkg.B', 5, 7) RETURNING id`, fileID).Scan(&calleeID); err != nil {
		t.Fatalf("insert callee: %v", err)
	}

	// A valid exact and a valid unresolved row are accepted.
	if _, err := conn.Exec(ctx, `INSERT INTO edges (caller_id, callee_id, callee_raw, resolution, source, call_line, call_kind)
		VALUES ($1, $2, 'B', 'exact', 'treesitter', 2, 'func')`, callerID, calleeID); err != nil {
		t.Fatalf("insert exact edge: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO edges (caller_id, callee_id, callee_raw, resolution, source, call_line, call_kind, unresolved_reason)
		VALUES ($1, NULL, 'Missing', 'unresolved', 'treesitter', 3, 'unknown', 'no_match')`, callerID); err != nil {
		t.Fatalf("insert unresolved edge: %v", err)
	}

	rejects := []struct {
		name string
		sql  string
		args []any
	}{
		{"unresolved with a callee", `INSERT INTO edges (caller_id, callee_id, callee_raw, resolution, source, call_line, call_kind, unresolved_reason)
			VALUES ($1, $2, 'B', 'unresolved', 'treesitter', 4, 'unknown', 'no_match')`, []any{callerID, calleeID}},
		{"exact with no callee", `INSERT INTO edges (caller_id, callee_id, callee_raw, resolution, source, call_line, call_kind)
			VALUES ($1, NULL, 'B', 'exact', 'treesitter', 4, 'func')`, []any{callerID}},
		{"unresolved with no reason", `INSERT INTO edges (caller_id, callee_id, callee_raw, resolution, source, call_line, call_kind)
			VALUES ($1, NULL, 'B', 'unresolved', 'treesitter', 4, 'unknown')`, []any{callerID}},
		{"exact with a reason", `INSERT INTO edges (caller_id, callee_id, callee_raw, resolution, source, call_line, call_kind, unresolved_reason)
			VALUES ($1, $2, 'B', 'exact', 'treesitter', 4, 'func', 'no_match')`, []any{callerID, calleeID}},
		{"name_only with no callee", `INSERT INTO edges (caller_id, callee_id, callee_raw, resolution, source, call_line, call_kind)
			VALUES ($1, NULL, 'B', 'name_only', 'treesitter', 4, 'func')`, []any{callerID}},
		{"unknown call kind", `INSERT INTO edges (caller_id, callee_id, callee_raw, resolution, source, call_line, call_kind)
			VALUES ($1, $2, 'B', 'exact', 'treesitter', 4, 'bogus')`, []any{callerID, calleeID}},
		{"unknown reason", `INSERT INTO edges (caller_id, callee_id, callee_raw, resolution, source, call_line, call_kind, unresolved_reason)
			VALUES ($1, NULL, 'B', 'unresolved', 'treesitter', 4, 'unknown', 'bogus')`, []any{callerID}},
		{"unknown resolution", `INSERT INTO edges (caller_id, callee_id, callee_raw, resolution, source, call_line, call_kind)
			VALUES ($1, $2, 'B', 'bogus', 'treesitter', 4, 'func')`, []any{callerID, calleeID}},
	}
	for _, tc := range rejects {
		t.Run(tc.name, func(t *testing.T) {
			_, err := conn.Exec(ctx, tc.sql, tc.args...)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
				t.Errorf("error = %v, want check violation (23514)", err)
			}
		})
	}

	// A duplicate edge is rejected by the unique index.
	_, err = conn.Exec(ctx, `INSERT INTO edges (caller_id, callee_id, callee_raw, resolution, source, call_line, call_kind)
		VALUES ($1, $2, 'B', 'exact', 'treesitter', 2, 'func')`, callerID, calleeID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate edge error = %v, want unique violation (23505)", err)
	}

	// A duplicate unresolved edge (same caller/raw/line, NULL callee) is also
	// rejected: coalesce(callee_id, 0) makes two NULL callees collide.
	_, err = conn.Exec(ctx, `INSERT INTO edges (caller_id, callee_id, callee_raw, resolution, source, call_line, call_kind, unresolved_reason)
		VALUES ($1, NULL, 'Missing', 'unresolved', 'treesitter', 3, 'unknown', 'no_match')`, callerID)
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate unresolved edge error = %v, want unique violation (23505)", err)
	}

	// Deleting the callee removes the edge and raises no error.
	if _, err := conn.Exec(ctx, `DELETE FROM symbols WHERE id = $1`, calleeID); err != nil {
		t.Fatalf("delete callee: %v", err)
	}
	if n := scalarCount(ctx, t, conn, `SELECT count(*) FROM edges WHERE resolution = 'exact'`); n != 0 {
		t.Errorf("exact edges after callee delete = %d, want 0", n)
	}
}

// TestEdgesMigrationDownUpDescriptorUnchanged proves 00005's Down reverses
// exactly: after Up, one Down and Up again the edges table description
// (columns, constraints, indexes) is unchanged. `make migrate-down migrate-up`
// on an empty database is the same cycle.
func TestEdgesMigrationDownUpDescriptorUnchanged(t *testing.T) {
	dbURL := testdb.Create(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if _, err := store.Migrate(ctx, dbURL); err != nil {
		t.Fatalf("up: %v", err)
	}
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	before := edgesDescriptor(ctx, t, conn)
	if _, err := store.MigrateDown(ctx, dbURL); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := store.Migrate(ctx, dbURL); err != nil {
		t.Fatalf("up again: %v", err)
	}
	after := edgesDescriptor(ctx, t, conn)
	if before != after {
		t.Errorf("edges description changed across down/up:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// edgesDescriptor is a canonical description of the edges table: its columns,
// constraints and indexes.
func edgesDescriptor(ctx context.Context, t *testing.T, conn *pgx.Conn) string {
	t.Helper()
	var b strings.Builder
	sections := []struct{ header, sql string }{
		{"columns", `SELECT column_name || ' ' || data_type || ' ' || is_nullable || ' ' || coalesce(column_default, '')
			FROM information_schema.columns WHERE table_name = 'edges' ORDER BY ordinal_position`},
		{"constraints", `SELECT conname || ' ' || pg_get_constraintdef(oid)
			FROM pg_constraint WHERE conrelid = 'edges'::regclass ORDER BY conname`},
		{"indexes", `SELECT indexname || ' ' || indexdef FROM pg_indexes WHERE tablename = 'edges' ORDER BY indexname`},
	}
	for _, section := range sections {
		fmt.Fprintf(&b, "[%s]\n", section.header)
		rows, err := conn.Query(ctx, section.sql)
		if err != nil {
			t.Fatalf("descriptor %s: %v", section.header, err)
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				rows.Close()
				t.Fatalf("scan descriptor %s: %v", section.header, err)
			}
			b.WriteString(line)
			b.WriteByte('\n')
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("descriptor %s rows: %v", section.header, err)
		}
	}
	return b.String()
}

// scalarCount runs a query returning one integer count.
func scalarCount(ctx context.Context, t *testing.T, conn *pgx.Conn, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := conn.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return n
}

// TestEdgesMigrationConvertsNullCalleeRows proves the widened Up conversion:
// a pre-00005 row with a NULL callee and any non-unresolved resolution (the old
// schema allowed exact/name_only/ambiguous there) becomes unresolved/no_match,
// so the new shape CHECKs hold even when data predates the migration.
func TestEdgesMigrationConvertsNullCalleeRows(t *testing.T) {
	dbURL := testdb.Create(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if _, err := store.Migrate(ctx, dbURL); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := store.MigrateDown(ctx, dbURL); err != nil {
		t.Fatalf("down to the pre-00005 shape: %v", err)
	}
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	var fileID, callerID int64
	if err := conn.QueryRow(ctx, `INSERT INTO files (path, package, sha, loc)
		VALUES ('pkg/a.go', 'pkg', 'sha', 3) RETURNING id`).Scan(&fileID); err != nil {
		t.Fatalf("insert file: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO symbols (file_id, kind, name, qualified_name, start_line, end_line)
		VALUES ($1, 'func', 'A', 'pkg.A', 1, 3) RETURNING id`, fileID).Scan(&callerID); err != nil {
		t.Fatalf("insert caller: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO edges (caller_id, callee_id, callee_raw, resolution, source)
		VALUES ($1, NULL, 'Missing', 'exact', 'treesitter')`, callerID); err != nil {
		t.Fatalf("insert pre-00005 NULL-callee row: %v", err)
	}

	if _, err := store.Migrate(ctx, dbURL); err != nil {
		t.Fatalf("up again: %v", err)
	}
	var resolution, reason string
	var calleeNull bool
	if err := conn.QueryRow(ctx,
		`SELECT resolution, unresolved_reason, callee_id IS NULL FROM edges`).Scan(&resolution, &reason, &calleeNull); err != nil {
		t.Fatalf("read converted row: %v", err)
	}
	if resolution != "unresolved" || reason != "no_match" || !calleeNull {
		t.Errorf("converted row = (%q, %q, callee NULL %v), want (unresolved, no_match, true)", resolution, reason, calleeNull)
	}
}
