package main

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
)

// TestVindexSlug pins the model-to-index-name slug rule: every character
// outside [a-z0-9] becomes '_' and letters are lowercased.
func TestVindexSlug(t *testing.T) {
	for model, want := range map[string]string{
		"qwen3-embedding:0.6b": "qwen3_embedding_0_6b",
		"nomic-embed-text":     "nomic_embed_text",
		"a.B/C:D":              "a_b_c_d",
		"UPPER":                "upper",
	} {
		if got := modelSlug(model); got != want {
			t.Errorf("modelSlug(%q) = %q, want %q", model, got, want)
		}
	}
}

// TestQuoteLiteral pins single-quote escaping, the only meeting point between
// a validated string and the emitted DDL.
func TestQuoteLiteral(t *testing.T) {
	if got, want := quoteLiteral("qwen3-embedding:0.6b"), "'qwen3-embedding:0.6b'"; got != want {
		t.Errorf("quoteLiteral = %q, want %q", got, want)
	}
	if got, want := quoteLiteral("o'brien"), "'o''brien'"; got != want {
		t.Errorf("quoteLiteral with a quote = %q, want %q", got, want)
	}
}

// TestPlanVindexValidAndInvalid pins the validated DDL: the default dimension,
// the index name and the inlined literals, plus refusal of an unknown model,
// unknown kind or non-positive dimension.
func TestPlanVindexValidAndInvalid(t *testing.T) {
	plan, err := planVindex("qwen3-embedding:0.6b", kindChunkHeader, 0)
	if err != nil {
		t.Fatalf("planVindex: %v", err)
	}
	if plan.dim != 1024 {
		t.Errorf("default dim = %d, want 1024", plan.dim)
	}
	if want := "emb_hnsw_qwen3_embedding_0_6b_chunk_header_1024"; plan.index != want {
		t.Errorf("index name = %q, want %q", plan.index, want)
	}
	for _, want := range []string{
		"CREATE INDEX IF NOT EXISTS " + plan.index,
		"(vec::vector(1024)) vector_cosine_ops",
		"WHERE model = 'qwen3-embedding:0.6b' AND target_kind = 'chunk_header'",
		"WITH (m=16, ef_construction=128)",
	} {
		if !strings.Contains(plan.sql, want) {
			t.Errorf("DDL missing %q:\n%s", want, plan.sql)
		}
	}

	for _, tc := range []struct {
		model, kind string
		dim         int
	}{
		{"not-a-model", kindChunkRaw, 0},
		{"", kindChunkRaw, 0},
		{"qwen3-embedding:0.6b", "not_a_kind", 0},
		{"qwen3-embedding:0.6b", "", 0},
		{"qwen3-embedding:0.6b", kindChunkRaw, -1},
	} {
		if _, err := planVindex(tc.model, tc.kind, tc.dim); err == nil {
			t.Errorf("planVindex(%q, %q, %d) = nil error, want refusal", tc.model, tc.kind, tc.dim)
		}
	}
}

// TestVindexRefusesInvalidModelOrKind points the command at the test pool's
// database, so a regression that moved validation after config.Load or
// store.NewPool would contact the database and create an index instead of
// refusing. Each refusal must name the offending value, create no index, and a
// valid model/kind afterwards must still succeed.
func TestVindexRefusesInvalidModelOrKind(t *testing.T) {
	pool := testdb.New(t)
	t.Setenv("DATABASE_URL", pool.Config().ConnConfig.ConnString())

	const badModel = "not-a-model"
	if err := runVindex(context.Background(), []string{"--model", badModel, "--kind", kindChunkRaw}); err == nil {
		t.Fatal("runVindex with an unknown model = nil error, want refusal")
	} else if !strings.Contains(err.Error(), badModel) {
		t.Errorf("unknown-model error = %v, want it to name %q", err, badModel)
	}

	const badKind = "not_a_kind"
	if err := runVindex(context.Background(), []string{"--model", "qwen3-embedding:0.6b", "--kind", badKind}); err == nil {
		t.Fatal("runVindex with an unknown kind = nil error, want refusal")
	} else if !strings.Contains(err.Error(), badKind) {
		t.Errorf("unknown-kind error = %v, want it to name %q", err, badKind)
	}

	if n := scalarInt(t, pool, `SELECT count(*) FROM pg_indexes WHERE indexname LIKE 'emb_hnsw%'`); n != 0 {
		t.Errorf("indexes created after refusals = %d, want 0", n)
	}

	if err := runVindex(context.Background(), []string{"--model", "qwen3-embedding:0.6b", "--kind", kindChunkRaw}); err != nil {
		t.Fatalf("runVindex with a valid model and kind: %v", err)
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM pg_indexes WHERE indexname LIKE 'emb_hnsw%'`); n != 1 {
		t.Errorf("indexes created after the valid run = %d, want 1", n)
	}
}

// TestVindexCommandBuildsIndexPlannerUsesIt runs the command on a 200-row
// fixture, then forces the planner to use the partial HNSW index: the retrieval
// query with the inlined model and kind predicates must keep an Index Scan on
// ten consecutive runs and sort nothing above it, so the partial predicate
// matches the query.
func TestVindexCommandBuildsIndexPlannerUsesIt(t *testing.T) {
	pool := testdb.New(t)
	const model = "qwen3-embedding:0.6b"
	seedEmbeddingRows(t, pool, model, kindChunkHeader, 200)

	plan, err := planVindex(model, kindChunkHeader, 0)
	if err != nil {
		t.Fatalf("planVindex: %v", err)
	}

	t.Setenv("DATABASE_URL", pool.Config().ConnConfig.ConnString())
	out, code := captureRun(t, "vindex", "--model", model, "--kind", kindChunkHeader)
	if code != 0 {
		t.Fatalf("vindex exit code = %d\n%s", code, out)
	}
	for _, want := range []string{
		"index: " + plan.index,
		"size_bytes: ",
		"maintenance_work_mem: " + vindexMaintenanceWorkMem,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("vindex output missing %q:\n%s", want, out)
		}
	}
	if size, ok := printedInt(out, "size_bytes: "); !ok {
		t.Errorf("vindex output has no parseable size_bytes line:\n%s", out)
	} else if size <= 0 {
		t.Errorf("size_bytes = %d, want > 0 on the 200-row fixture", size)
	}

	query := syntheticVector(plan.dim, 7)
	for i := 0; i < 10; i++ {
		explain := explainForcedIndex(t, pool, plan, query)
		if !strings.Contains(explain, "Index Scan using "+plan.index) {
			t.Fatalf("run %d: plan does not use %s:\n%s", i, plan.index, explain)
		}
		if strings.Contains(explain, "Sort") {
			t.Fatalf("run %d: plan sorts above the index instead of using its order:\n%s", i, explain)
		}
	}
}

// printedInt returns the integer after key on its own line of out, e.g.
// printedInt(out, "size_bytes: ") for a "size_bytes: 123\n" line.
func printedInt(out, key string) (int64, bool) {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, key); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// TestVindexIdempotent runs the command twice: both succeed and exactly one
// index exists.
func TestVindexIdempotent(t *testing.T) {
	pool := testdb.New(t)
	const model = "nomic-embed-text"
	seedEmbeddingRows(t, pool, model, kindChunkRaw, 10)

	plan, err := planVindex(model, kindChunkRaw, 0)
	if err != nil {
		t.Fatalf("planVindex: %v", err)
	}
	t.Setenv("DATABASE_URL", pool.Config().ConnConfig.ConnString())
	for i := 0; i < 2; i++ {
		out, code := captureRun(t, "vindex", "--model", model, "--kind", kindChunkRaw)
		if code != 0 {
			t.Fatalf("run %d exit code = %d\n%s", i, code, out)
		}
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM pg_class WHERE relname = $1 AND relkind = 'i'`, plan.index); n != 1 {
		t.Errorf("pg_class index count = %d, want 1", n)
	}
	if n := scalarInt(t, pool, `SELECT count(*) FROM pg_indexes WHERE indexname = $1`, plan.index); n != 1 {
		t.Errorf("pg_indexes count = %d, want 1", n)
	}
}

// seedEmbeddingRows inserts n unit vectors of the model's dimension, one per
// target id, so the planner has enough rows to prefer a forced index.
func seedEmbeddingRows(t *testing.T, pool *pgxpool.Pool, model, kind string, n int) {
	t.Helper()
	dim, err := embed.Dim(model)
	if err != nil {
		t.Fatalf("dim: %v", err)
	}
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin seed: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for i := 1; i <= n; i++ {
		vec := pgvector.NewVector(syntheticVector(dim, int64(i)))
		if _, err := tx.Exec(ctx,
			`INSERT INTO embeddings (target_kind, target_id, model, vec) VALUES ($1, $2, $3, $4)`,
			kind, int64(i), model, vec); err != nil {
			t.Fatalf("insert embedding %d: %v", i, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
	if _, err := pool.Exec(ctx, "ANALYZE embeddings"); err != nil {
		t.Fatalf("analyze embeddings: %v", err)
	}
}

// explainForcedIndex returns the EXPLAIN plan of the retrieval query with
// sequential scans and sorting disabled. On a 200-row fixture Postgres would
// otherwise sort the rows (cheaper than the HNSW walk) and never consider the
// ordered index; disabling the sort shows that the partial HNSW index does
// match the model and kind predicate and is chosen for the ordering.
func explainForcedIndex(t *testing.T, pool *pgxpool.Pool, plan vindexPlan, query []float32) string {
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
	sql := fmt.Sprintf(
		"EXPLAIN SELECT id FROM embeddings WHERE model = %s AND target_kind = %s "+
			"ORDER BY vec::vector(%d) <=> %s::vector(%d) LIMIT 10",
		quoteLiteral(plan.model), quoteLiteral(plan.kind), plan.dim,
		quoteLiteral(vectorLiteral(query)), plan.dim)
	rows, err := tx.Query(ctx, sql)
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

// syntheticVector returns a deterministic unit vector of the given dimension.
func syntheticVector(dim int, seed int64) []float32 {
	v := make([]float32, dim)
	var sum float64
	for i := range v {
		x := math.Sin(float64(seed)*0.1 + float64(i)*0.01)
		v[i] = float32(x)
		sum += x * x
	}
	norm := math.Sqrt(sum)
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
	return v
}

// vectorLiteral renders a vector as a pgvector text literal, e.g. [0.1,0.2].
func vectorLiteral(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
