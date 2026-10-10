package walk_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

// TestSyncIdempotentAndDeletesVanished walks a small fixture into a throwaway
// database twice (the second run must change nothing) and proves a file that
// disappears from disk is deleted.
func TestSyncIdempotentAndDeletesVanished(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	root := t.TempDir()
	writeFixture(t, root, "pkg/scheduler/a.go", "package scheduler\n\nfunc A() {}\n")
	writeFixture(t, root, "pkg/api/b.go", "package api\n\nfunc B() {}\n")
	writeFixture(t, root, "pkg/scheduler/zz_generated.deepcopy.go", "package scheduler\n")

	files, _, err := walk.Walk(root)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	changed, deleted, err := walk.Sync(ctx, pool, files)
	if err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	if changed != 2 || deleted != 0 {
		t.Fatalf("first Sync changed=%d deleted=%d, want 2/0", changed, deleted)
	}

	var count, inSlice int
	if err := pool.QueryRow(ctx, "SELECT count(*), count(*) FILTER (WHERE in_slice) FROM files").Scan(&count, &inSlice); err != nil {
		t.Fatal(err)
	}
	if count != 2 || inSlice != 1 {
		t.Fatalf("rows=%d in_slice=%d, want 2/1", count, inSlice)
	}

	files2, _, err := walk.Walk(root)
	if err != nil {
		t.Fatalf("Walk 2: %v", err)
	}
	changed, deleted, err = walk.Sync(ctx, pool, files2)
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if changed != 0 || deleted != 0 {
		t.Fatalf("second Sync changed=%d deleted=%d, want 0/0 (idempotent)", changed, deleted)
	}

	if err := os.Remove(filepath.Join(root, "pkg/api/b.go")); err != nil {
		t.Fatal(err)
	}
	files3, _, err := walk.Walk(root)
	if err != nil {
		t.Fatalf("Walk 3: %v", err)
	}
	changed, deleted, err = walk.Sync(ctx, pool, files3)
	if err != nil {
		t.Fatalf("third Sync: %v", err)
	}
	if changed != 0 || deleted != 1 {
		t.Fatalf("third Sync changed=%d deleted=%d, want 0/1", changed, deleted)
	}

	var vanished int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM files WHERE path = 'pkg/api/b.go'").Scan(&vanished); err != nil {
		t.Fatal(err)
	}
	if vanished != 0 {
		t.Fatalf("vanished file still present: %d rows", vanished)
	}
}
