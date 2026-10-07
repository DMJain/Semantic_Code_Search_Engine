package main

import (
	"context"
	"log/slog"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
)

func runMigrate(ctx context.Context, args []string) error {
	if err := parseFlags("migrate", args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	results, err := store.Migrate(ctx, cfg.DatabaseURL)
	for _, r := range results {
		slog.Info("applied migration", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
	}
	if err != nil {
		return err
	}
	if len(results) == 0 {
		slog.Info("database already up to date")
	}
	return nil
}
