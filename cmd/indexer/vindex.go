package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
)

// vindexMaintenanceWorkMem is the session memory requested for the HNSW build
// and printed so the build setting is recorded.
const vindexMaintenanceWorkMem = "256MB"

// knownTargetKinds lists the target_kind enum in a stable order for messages.
// It mirrors validTargetKinds in cmd/indexer/embed.go (the same schema enum);
// keep the two in step so the accepted kinds and the error text do not drift.
var knownTargetKinds = []string{
	kindChunkRaw, kindChunkHeader, "summary_function", "summary_file", "summary_package",
}

// vindexPlan is one index build: the validated model, kind and dimension, the
// index name and the exact DDL to run.
type vindexPlan struct {
	model string
	kind  string
	dim   int
	index string
	sql   string
}

func runVindex(ctx context.Context, args []string) error {
	fs := newFlagSet("vindex")
	model := fs.String("model", "", "embedding model (required)")
	kind := fs.String("kind", "", "target kind (required)")
	dim := fs.Int("dim", 0, "vector dimension (default the model's registry dimension)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Validate the model, kind and dimension before opening the pool, so an
	// invalid argument runs no SQL at all.
	plan, err := planVindex(*model, *kind, *dim)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return vindexStage(ctx, pool, plan, os.Stdout)
}

// planVindex validates a model, kind and dimension against the registry and the
// target_kind enum and builds the partial HNSW index DDL. Model and kind are
// whitelisted and then single-quote escaped, so no unvalidated input is ever
// inlined.
func planVindex(model, kind string, dim int) (vindexPlan, error) {
	if model == "" {
		return vindexPlan{}, fmt.Errorf("--model is required; known models: %s",
			strings.Join(embed.KnownModels(), ", "))
	}
	if kind == "" {
		return vindexPlan{}, fmt.Errorf("--kind is required; kinds: %s", strings.Join(knownTargetKinds, ", "))
	}
	specDim, err := embed.Dim(model)
	if err != nil {
		return vindexPlan{}, err
	}
	if !validTargetKinds[kind] {
		return vindexPlan{}, fmt.Errorf("unknown kind %q; want one of: %s", kind, strings.Join(knownTargetKinds, ", "))
	}
	if dim == 0 {
		dim = specDim
	}
	if dim <= 0 {
		return vindexPlan{}, fmt.Errorf("--dim must be positive, got %d", dim)
	}
	name := fmt.Sprintf("emb_hnsw_%s_%s_%d", modelSlug(model), modelSlug(kind), dim)
	ddl := fmt.Sprintf(
		"CREATE INDEX IF NOT EXISTS %s ON embeddings USING hnsw ((vec::vector(%d)) vector_cosine_ops) "+
			"WITH (m=16, ef_construction=128) WHERE model = %s AND target_kind = %s",
		name, dim, quoteLiteral(model), quoteLiteral(kind))
	return vindexPlan{model: model, kind: kind, dim: dim, index: name, sql: ddl}, nil
}

// vindexStage sets the build memory for the session, creates the index
// idempotently and prints the index name, its size and the memory setting. The
// DDL, the SET LOCAL and the size lookup share one transaction, so the memory
// setting applies to the build and never leaks back to the pool.
func vindexStage(ctx context.Context, pool *pgxpool.Pool, plan vindexPlan, out io.Writer) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin vindex: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL maintenance_work_mem = "+quoteLiteral(vindexMaintenanceWorkMem)); err != nil {
		return fmt.Errorf("set maintenance_work_mem: %w", err)
	}
	var mem string
	if err := tx.QueryRow(ctx, "SHOW maintenance_work_mem").Scan(&mem); err != nil {
		return fmt.Errorf("read maintenance_work_mem: %w", err)
	}
	if _, err := tx.Exec(ctx, plan.sql); err != nil {
		return fmt.Errorf("create index %s: %w", plan.index, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit vindex: %w", err)
	}

	var size int64
	if err := pool.QueryRow(ctx, "SELECT pg_relation_size($1::regclass)", plan.index).Scan(&size); err != nil {
		return fmt.Errorf("index size %s: %w", plan.index, err)
	}
	fmt.Fprintf(out, "index: %s\n", plan.index)
	fmt.Fprintf(out, "size_bytes: %d\n", size)
	fmt.Fprintf(out, "maintenance_work_mem: %s\n", mem)
	return nil
}

// modelSlug lowercases the model and replaces every character outside [a-z0-9]
// with '_', so the result is safe to use in an index name.
func modelSlug(model string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(model) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// quoteLiteral wraps s in single quotes, doubling any single quote, so a
// validated value can be inlined into DDL without changing its meaning.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
