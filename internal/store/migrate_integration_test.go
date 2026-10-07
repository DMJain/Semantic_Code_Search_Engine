package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestMigrateCreatesTables needs a real Postgres with pgvector, such as the
// docker-compose one. It is skipped unless TEST_DATABASE_URL is set, so CI
// never runs it.
func TestMigrateCreatesTables(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := Migrate(ctx, url); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)

	tables := []string{"files", "symbols", "chunks", "edges", "summaries", "embeddings", "llm_cache", "traces"}
	for _, table := range tables {
		var exists bool
		if err := conn.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+table).Scan(&exists); err != nil {
			t.Fatalf("check %s: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s does not exist after migration", table)
		}
	}
}
