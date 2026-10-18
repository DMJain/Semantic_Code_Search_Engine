package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/chunk"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/seeded"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
)

// nomicModel is the default embedding model the fit check measures: the
// smallest-context model, against which the request budget is sized (D-078).
// The --model flag overrides it to measure any other registry model.
const nomicModel = "nomic-embed-text"

// fitSampleN is how many documents each half of the sample draws: the largest
// by estimate and the seeded-random half.
const fitSampleN = 200

// defaultNomicFitPath is where the JSON record lands.
const defaultNomicFitPath = "eval/results/bench/nomic-fit.json"

// fitOptions configures one fit run.
type fitOptions struct {
	Seed     int64
	JSONPath string
}

// fitCandidate is one in-slice chunk_header document, already rendered the way
// the embed stage sends it: the model's document prefix followed by the header,
// a newline and the content.
type fitCandidate struct {
	ID       int64
	Estimate int
	Rendered string
}

// nomicFitResult is the JSON record written to the bench results folder.
type nomicFitResult struct {
	Model         string  `json:"model"`
	OllamaVersion string  `json:"ollama_version"`
	Seed          int64   `json:"seed"`
	N             int     `json:"n"`
	Accepted      int     `json:"accepted"`
	Rejected      int     `json:"rejected"`
	Fallback      bool    `json:"fallback"`
	RatioP50      float64 `json:"ratio_p50"`
	RatioP99      float64 `json:"ratio_p99"`
	RatioMax      float64 `json:"ratio_max"`
	EstMax        int     `json:"est_max"`
	RealMax       int     `json:"real_max"`
}

// runNomicFit measures whether chunk and header sizes fit the model's real
// context before any vector is written. It writes nothing to the embeddings
// table. A failure (any rejection, or an over-limit real/estimate ratio) prints
// the replacement request budget and returns an error, so the process exits
// non-zero and the orchestrator can re-run chunk and this check.
func runNomicFit(ctx context.Context, args []string) error {
	fs := newFlagSet("nomic-fit")
	seed := fs.Int64("seed", 1, "sampling seed")
	jsonPath := fs.String("json", defaultNomicFitPath, "JSON record path")
	model := fs.String("model", nomicModel, "embedding model to measure (a registry name)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// When --json is left at its default, derive the record path from the model
	// so measuring a second model never overwrites the nomic record. nomic still
	// writes the spec's default path.
	jsonSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "json" {
			jsonSet = true
		}
	})
	if !jsonSet {
		*jsonPath = fitJSONPath(*model)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	client, err := embed.NewOllama(cfg.OllamaURL, *model, 0)
	if err != nil {
		return err
	}
	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	return nomicFit(ctx, pool, client, fitOptions{Seed: *seed, JSONPath: *jsonPath}, os.Stdout)
}

// nomicFit is the run behind [runNomicFit], taking its database, client and
// writer so tests can drive it with a throwaway database and a fake server.
func nomicFit(ctx context.Context, pool *pgxpool.Pool, client *embed.Ollama, opts fitOptions, out io.Writer) error {
	cands, err := loadFitCandidates(ctx, pool, client.DocPrefix())
	if err != nil {
		return err
	}
	sample := selectFitSample(cands, opts.Seed, fitSampleN)
	fmt.Fprintf(out, "nomic-fit: %d documents sampled (seed %d), one per request\n", len(sample), opts.Seed)

	res := nomicFitResult{Model: client.Model(), Seed: opts.Seed, N: len(sample)}

	// First verify the installed Ollama reports a prompt token count on the
	// embed endpoint. A missing count is a valid fallback: the check then
	// records accept/reject only and reports zero ratios.
	fallback, err := probeTokenCount(ctx, client)
	if err != nil {
		return err
	}
	res.Fallback = fallback
	if fallback {
		fmt.Fprintln(out, "nomic-fit: no prompt_eval_count on /api/embed; recording accept/reject only")
	}
	version, err := client.Version(ctx)
	if err != nil {
		return fmt.Errorf("nomic-fit: version: %w", err)
	}
	res.OllamaVersion = version

	var ratios []float64
	for _, c := range sample {
		if c.Estimate > res.EstMax {
			res.EstMax = c.Estimate
		}
		count, err := client.EmbedInput(ctx, c.Rendered, "")
		if err != nil {
			// Only a genuine input-too-long rejection counts as a rejected
			// document; any other error (transport, 5xx) aborts the tool so
			// an outage is never reported as a fit failure.
			if !errors.Is(err, embed.ErrInputTooLong) {
				return fmt.Errorf("nomic-fit: embed: %w", err)
			}
			res.Rejected++
			continue
		}
		res.Accepted++
		if fallback {
			continue
		}
		ratios = append(ratios, float64(count)/float64(c.Estimate))
		if count > res.RealMax {
			res.RealMax = count
		}
	}
	res.RatioP50, res.RatioP99, res.RatioMax = ratioPercentiles(ratios)

	// The model must not stay resident: unload it before the run ends.
	if err := client.Unload(ctx); err != nil {
		return fmt.Errorf("nomic-fit: unload: %w", err)
	}

	if err := writeNomicFitResult(opts.JSONPath, res); err != nil {
		return err
	}
	fmt.Fprintf(out, "nomic-fit: wrote %s\n", opts.JSONPath)

	newBudget, failed := fitFailure(res.Rejected, res.RatioMax,
		chunk.RequestBudget, client.NumCtx())
	if failed {
		// In fallback mode no ratio is measurable, so there is no numeric
		// replacement budget to propose: say so and still exit non-zero.
		if res.Fallback {
			fmt.Fprintf(out, "nomic-fit: FAILED rejected=%d; request budget unchanged; ratio unavailable in fallback mode\n",
				res.Rejected)
			return fmt.Errorf(
				"nomic fit failed (rejected=%d); request budget unchanged, ratio unavailable in fallback mode",
				res.Rejected)
		}
		// RequestBudget is one shared value sized for the smallest registry
		// context. Only that model's fit may propose a replacement; another
		// model's num_ctx would give an unsound budget for the shared ceiling.
		if client.NumCtx() != embed.MinNumCtx() {
			fmt.Fprintf(out, "nomic-fit: FAILED rejected=%d ratio_max=%.4f; %s num_ctx %d is not the budget model (%d), no shared-budget replacement proposed\n",
				res.Rejected, res.RatioMax, res.Model, client.NumCtx(), embed.MinNumCtx())
			return fmt.Errorf(
				"fit failed for %s (rejected=%d, ratio_max=%.4f); RequestBudget is sized for the smallest-context model and is not replaced from %s",
				res.Model, res.Rejected, res.RatioMax, res.Model)
		}
		fmt.Fprintf(out, "nomic-fit: FAILED rejected=%d ratio_max=%.4f; RequestBudget %d -> %d\n",
			res.Rejected, res.RatioMax, chunk.RequestBudget, newBudget)
		return fmt.Errorf(
			"nomic fit failed (rejected=%d, ratio_max=%.4f); re-run chunk with RequestBudget = %d",
			res.Rejected, res.RatioMax, newBudget)
	}
	fmt.Fprintf(out, "nomic-fit: OK rejected=0 ratio_max=%.4f (RequestBudget %d fits %d)\n",
		res.RatioMax, chunk.RequestBudget, client.NumCtx())
	return nil
}

// probeTokenCount sends one fixed document and reports whether the server
// returned a positive prompt token count, which decides between measuring
// ratios and the accept/reject fallback.
func probeTokenCount(ctx context.Context, client *embed.Ollama) (bool, error) {
	count, err := client.EmbedInput(ctx, client.DocPrefix()+"nomic fit probe", "")
	if err != nil {
		return false, fmt.Errorf("nomic-fit: probe: %w", err)
	}
	return count == 0, nil
}

// loadFitCandidates reads every in-slice, embeddable chunk_header document
// ordered by chunk id and renders it with the model's document prefix. Chunks
// whose content token_count exceeds EmbeddableContentMax are excluded: the
// embed stage skips them as too_large and never sends them, so measuring them
// would fail the check on documents that can never be embedded (D-082). The
// estimate is the bytes/4 estimate of the rendered request (D-078).
func loadFitCandidates(ctx context.Context, pool *pgxpool.Pool, prefix string) ([]fitCandidate, error) {
	rows, err := pool.Query(ctx, `
		SELECT c.id, c.header, c.content
		FROM chunks c
		JOIN files f ON f.id = c.file_id
		WHERE f.in_slice AND c.token_count <= $1
		ORDER BY c.id`, chunk.EmbeddableContentMax)
	if err != nil {
		return nil, fmt.Errorf("list chunk_header documents: %w", err)
	}
	defer rows.Close()

	var out []fitCandidate
	for rows.Next() {
		var id int64
		var header, content string
		if err := rows.Scan(&id, &header, &content); err != nil {
			return nil, fmt.Errorf("scan chunk_header document: %w", err)
		}
		rendered := prefix + header + "\n" + content
		out = append(out, fitCandidate{ID: id, Estimate: chunk.EstimateTokens(rendered), Rendered: rendered})
	}
	return out, rows.Err()
}

// selectFitSample draws the document sample deterministically: the n largest
// by estimate (ties by chunk id ascending) followed by n drawn with the seed
// from the candidate list ordered by chunk id.
func selectFitSample(cands []fitCandidate, seed int64, n int) []fitCandidate {
	ordered := slices.Clone(cands)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })

	largest := slices.Clone(ordered)
	sort.SliceStable(largest, func(i, j int) bool {
		if largest[i].Estimate != largest[j].Estimate {
			return largest[i].Estimate > largest[j].Estimate
		}
		return largest[i].ID < largest[j].ID
	})
	if len(largest) > n {
		largest = largest[:n]
	}
	return append(largest, seeded.Draw(ordered, n, seed)...)
}

// ratioPercentiles returns the nearest-rank p50 and p99 and the maximum of
// ratios; all zero when there is no ratio (fallback mode).
func ratioPercentiles(ratios []float64) (p50, p99, maxRatio float64) {
	if len(ratios) == 0 {
		return 0, 0, 0
	}
	xs := slices.Clone(ratios)
	sort.Float64s(xs)
	return percentileValue(xs, 50), percentileValue(xs, 99), xs[len(xs)-1]
}

// percentileValue returns the nearest-rank percentile p (0-100) of a sorted
// slice; xs is never empty at a call site.
func percentileValue(xs []float64, p float64) float64 {
	idx := int(math.Ceil(p/100*float64(len(xs)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(xs) {
		idx = len(xs) - 1
	}
	return xs[idx]
}

// fitFailure applies the rule fixed before the data (D-078): it fails on any
// rejection, or when the largest measured ratio scaled to the request budget
// exceeds 0.95 of the model context. That is the exact negation of the
// advertised success condition, so there is no gap between them; in fallback
// mode the ratio is zero and only rejections fail. On failure with a
// measurable ratio it returns the replacement budget
// floor(0.95*numCtx/ratio_max).
func fitFailure(rejected int, ratioMax float64, budget, numCtx int) (newBudget int, failed bool) {
	failed = rejected > 0 || ratioMax*float64(budget) > 0.95*float64(numCtx)
	if !failed {
		return 0, false
	}
	if ratioMax <= 0 {
		return 0, true
	}
	return int(math.Floor(0.95 * float64(numCtx) / ratioMax)), true
}

// fitJSONPath is the default record path for a model: the spec's
// eval/results/bench/nomic-fit.json for nomic, and a model-suffixed path for
// any other model so measuring a second model never overwrites the nomic record.
func fitJSONPath(model string) string {
	if model == nomicModel {
		return defaultNomicFitPath
	}
	return "eval/results/bench/" + fitSlug(model) + "-fit.json"
}

// fitSlug turns a model name into a filename-safe slug, the same rule the
// vector index name uses: lowercase, every character outside [a-z0-9] becomes
// an underscore.
func fitSlug(model string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(model) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return b.String()
}

// writeNomicFitResult writes res as indented JSON, creating parent
// directories, in the same style as the summarize bench writer.
func writeNomicFitResult(path string, res nomicFitResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
