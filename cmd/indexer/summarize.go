package main

import (
	"context"
	"fmt"
	"os"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/summarize"
)

// onlyFunctionLevel is the one summary level this command implements; file and
// package levels arrive in S5.
const onlyFunctionLevel = "function"

func runSummarize(ctx context.Context, args []string) error {
	fs := newFlagSet("summarize")
	level := fs.String("level", onlyFunctionLevel, `summary level (only "function" is implemented)`)
	limit := fs.Int("limit", 0, "summarize at most N functions after sampling (0 = no limit)")
	sample := fs.Int("sample", 0, "draw a stratified sample of N eligible functions (0 = all)")
	seed := fs.Int64("seed", 0, "sampling seed")
	concurrency := fs.Int("concurrency", 1, "parallel summary requests (1 or 2)")
	retryFailed := fs.Bool("retry-failed", false, "re-pick rows that previously failed")
	printSample := fs.Bool("print-sample", false, "print the sampled function ids and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *level != onlyFunctionLevel {
		return fmt.Errorf("level %q is not supported; only %q is implemented", *level, onlyFunctionLevel)
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

	runner := &summarize.Runner{
		Pool:    pool,
		Client:  summarize.NewClient(cfg.OllamaURL, cfg.OllamaSummaryModel, cfg.OllamaSummaryNumCtx),
		KubeDir: cfg.KubeDir,
		Model:   cfg.OllamaSummaryModel,
		Out:     os.Stdout,
	}
	_, err = runner.Run(ctx, summarize.RunOptions{
		Limit:       *limit,
		Sample:      *sample,
		Seed:        *seed,
		Concurrency: *concurrency,
		RetryFailed: *retryFailed,
		PrintSample: *printSample,
	})
	return err
}
