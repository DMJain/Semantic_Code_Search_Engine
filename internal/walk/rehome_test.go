package walk_test

import (
	"context"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

// TestSyncRehomesSymbolsOfVanishedFiles proves symbols of a vanished file move
// to the first surviving file (by path) of the same package, only that
// survivor's parsed marker is cleared, the survivor's own slice flag now
// governs the symbol, and a package with no survivor loses its symbols.
func TestSyncRehomesSymbolsOfVanishedFiles(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	f := func(path, pkg string, inSlice bool) walk.File {
		return walk.File{Path: path, Package: pkg, SHA: "s-" + path, LOC: 5, InSlice: inSlice}
	}
	all := []walk.File{
		f("p/a.go", "p", false), f("p/b.go", "p", true), f("p/c.go", "p", true), f("p/d.go", "p", true),
		f("q/q.go", "q", true),
	}
	if _, _, err := walk.Sync(ctx, pool, all); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE files SET parsed_sha = sha`)
	exec(`INSERT INTO symbols (file_id, kind, name, qualified_name, signature, start_line, end_line)
	      SELECT id, 'func', 'F', 'p.F', 'func F()', 1, 9 FROM files WHERE path = 'p/d.go'`)
	exec(`INSERT INTO symbols (file_id, kind, name, qualified_name, signature, start_line, end_line)
	      SELECT id, 'func', 'Q', 'q.Q', 'func Q()', 1, 9 FROM files WHERE path = 'q/q.go'`)

	// d.go and q.go vanish; a.go, b.go, c.go survive.
	if _, deleted, err := walk.Sync(ctx, pool, all[:3]); err != nil || deleted != 2 {
		t.Fatalf("Sync deleted %d, err %v, want 2", deleted, err)
	}

	var path string
	var inSlice bool
	if err := pool.QueryRow(ctx,
		`SELECT f.path, f.in_slice FROM symbols s JOIN files f ON f.id = s.file_id WHERE s.qualified_name = 'p.F'`).
		Scan(&path, &inSlice); err != nil {
		t.Fatalf("p.F not kept: %v", err)
	}
	if path != "p/a.go" || inSlice {
		t.Errorf("p.F re-homed to %s (in_slice %v), want p/a.go (in_slice false)", path, inSlice)
	}
	var stale []string
	rows, err := pool.Query(ctx, `SELECT path FROM files WHERE parsed_sha IS NULL ORDER BY path`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		stale = append(stale, p)
	}
	if len(stale) != 1 || stale[0] != "p/a.go" {
		t.Errorf("files with cleared parsed marker = %v, want [p/a.go]", stale)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM symbols WHERE qualified_name = 'q.Q'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("symbol of a package with no survivor: count %d, err %v, want 0", n, err)
	}
}
