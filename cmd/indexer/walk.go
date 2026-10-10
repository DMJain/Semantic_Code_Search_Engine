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
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
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

// printWalkStats prints one excluded line per rule, then the kept totals.
func printWalkStats(stats walk.Stats) {
	for _, rule := range walk.RuleNames() {
		fmt.Printf("excluded %-20s %d\n", rule, stats.Excluded[rule])
	}
	fmt.Printf("indexed %d files (slice %d), LOC %d (slice %d)\n",
		stats.Indexed, stats.InSlice, stats.LOC, stats.InSliceLOC)
}
