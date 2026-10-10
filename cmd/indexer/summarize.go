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

// defaultBenchOutput is where `indexer summarize --bench` writes its results.
const defaultBenchOutput = "eval/results/bench/summarize.json"

// benchFunctionN, benchFileN, benchPkgN and benchSeed are the fixed benchmark
// parameters of S1-T11: 200 in-slice functions, 20 file-level and 3
// package-level prompts, seed 7.
const (
	benchFunctionN = 200
	benchFileN     = 20
	benchPkgN      = 3
	benchSeed      = 7
)

func runSummarize(ctx context.Context, args []string) error {
	fs := newFlagSet("summarize")
	level := fs.String("level", onlyFunctionLevel, `summary level (only "function" is implemented)`)
	limit := fs.Int("limit", 0, "summarize at most N functions after sampling (0 = no limit)")
	sample := fs.Int("sample", 0, "draw a stratified sample of N eligible functions (0 = all)")
	seed := fs.Int64("seed", 0, "sampling seed")
	concurrency := fs.Int("concurrency", 1, "parallel summary requests (1 or 2)")
	retryFailed := fs.Bool("retry-failed", false, "re-pick rows that previously failed")
	printSample := fs.Bool("print-sample", false, "print the sampled function ids and exit")
	bench := fs.Bool("bench", false, "run the throughput benchmark (writes no summary rows) and exit")
	out := fs.String("out", defaultBenchOutput, "benchmark results JSON path (--bench only)")
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

	if *bench {
		res, err := runner.Bench(ctx, summarize.BenchOptions{
			FunctionN: benchFunctionN,
			FileN:     benchFileN,
			PkgN:      benchPkgN,
			Seed:      benchSeed,
		})
		if err != nil {
			return err
		}
		if err := summarize.WriteBenchResult(*out, res); err != nil {
			return err
		}
		fmt.Printf("bench: wrote %s (fn c1 %.1f/h, c2 %.1f/h; file %.1f/h; pkg %.1f/h)\n",
			*out, res.FnPerHourC1, res.FnPerHourC2, res.FilePerHour, res.PkgPerHour)
		return nil
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
