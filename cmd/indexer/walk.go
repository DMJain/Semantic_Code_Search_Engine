package main

import (
	"context"
	"fmt"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/walk"
)

func runWalk(ctx context.Context, args []string) error {
	fs := newFlagSet("walk")
	dryRun := fs.Bool("dry-run", false, "print stats only; do not write to the database")
	verifySlice := fs.Bool("verify-slice", false, "recompute the in-slice path hash and compare with internal/walk/slice.sha256")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if *verifySlice {
		return verifySliceHash(cfg.KubeDir)
	}

	files, stats, err := walk.Walk(cfg.KubeDir)
	if err != nil {
		return fmt.Errorf("walk %s: %w", cfg.KubeDir, err)
	}
	printWalkStats(stats)

	if *dryRun {
		fmt.Println("dry-run: no database writes")
		return nil
	}

	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	changed, deleted, err := walk.Sync(ctx, pool, files)
	if err != nil {
		return err
	}
	fmt.Printf("%d changed, %d deleted\n", changed, deleted)
	return nil
}

// verifySliceHash recomputes the sha256 of the sorted in-slice path list from
// the corpus and the CURRENT slice definition (walk.SlicePrefixes minus
// walk.SliceDropPrefixes), then compares it with the frozen hash in
// internal/walk/slice.sha256 (D-070). Recomputing from the slice definition,
// not from the database, is what makes a temporary edit of the drop list fail
// the check. It prints both hashes and returns an error
// on drift, so `make verify-slice` exits non-zero.
func verifySliceHash(root string) error {
	files, _, err := walk.Walk(root)
	if err != nil {
		return fmt.Errorf("walk %s: %w", root, err)
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		if f.InSlice {
			paths = append(paths, f.Path)
		}
	}

	fmt.Printf("in-slice paths %d\nexpected %s\ncomputed %s\n",
		len(paths), walk.ExpectedSliceHash(), walk.SliceHash(paths))
	if err := walk.VerifySliceHash(paths); err != nil {
		return err
	}
	fmt.Println("slice hash: match")
	return nil
}

// printWalkStats prints one excluded line per rule, then the kept totals.
func printWalkStats(stats walk.Stats) {
	for _, rule := range walk.RuleNames() {
		fmt.Printf("excluded %-20s %d\n", rule, stats.Excluded[rule])
	}
	fmt.Printf("indexed %d files (slice %d), LOC %d (slice %d)\n",
		stats.Indexed, stats.InSlice, stats.LOC, stats.InSliceLOC)
}
