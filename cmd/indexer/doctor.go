package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/llm"
)

const doctorCheckTimeout = 10 * time.Second

// Fixed probe inputs for doctor --probe-all (user story 25): one query-shaped
// string and two document-shaped strings, kept constant so runs are comparable.
const probeQuery = "how does the kubelet evict pods under memory pressure"

var probeDocs = []embed.Document{
	{Title: "evictPod", Text: "func (m *managerImpl) evictPod(pod *v1.Pod) bool"},
	{Title: "kubelet.go", Text: "The kubelet is the primary node agent that runs on each node."},
}

// oversizedProbeInput is longer than every registry model's context window
// (qwen3-embedding:0.6b has the largest, 32768 tokens), so truncate:false must
// reject it if the model enforces its context.
var oversizedProbeInput = strings.Repeat("kubelet evicts pods under memory pressure ", 8000)

// doctor prints one line per check and remembers whether any failed.
type doctor struct {
	failed int
}

func (d *doctor) pass(name, detail string) {
	fmt.Printf("PASS  %-28s %s\n", name, detail)
}

func (d *doctor) warn(name, detail string) {
	fmt.Printf("WARN  %-28s %s\n", name, detail)
}

func (d *doctor) skip(name, reason string) {
	fmt.Printf("SKIP  %-28s %s\n", name, reason)
}

func (d *doctor) fail(name string, err error, fix string) {
	d.failed++
	fmt.Printf("FAIL  %-28s %v\n", name, err)
	fmt.Printf("      fix: %s\n", fix)
}

func runDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("indexer doctor", flag.ContinueOnError)
	probeAll := fs.Bool("probe-all", false, "probe every pulled embedding model on fixed strings")
	ollamaChecks := fs.Bool("ollama-checks", false, "run the Ollama generation and over-length checks")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *probeAll && *ollamaChecks:
		return errors.New("--probe-all and --ollama-checks are mutually exclusive")
	case *probeAll:
		return runProbeAll(ctx)
	case *ollamaChecks:
		return runOllamaChecks(ctx)
	}
	var d doctor

	cfg, err := config.Load()
	if err != nil {
		d.fail("config", err, "cp .env.example .env and fill it in (make loads .env automatically)")
		return errors.New("1 check failed")
	}
	d.pass("config", "loaded")

	d.checkPostgres(ctx, cfg)
	d.checkOllama(ctx, cfg)

	if d.failed > 0 {
		return fmt.Errorf("%d check(s) failed", d.failed)
	}
	fmt.Println("all checks passed")
	return nil
}

func (d *doctor) checkPostgres(ctx context.Context, cfg config.Config) {
	ctx, cancel := context.WithTimeout(ctx, doctorCheckTimeout)
	defer cancel()

	// A plain connection, not store.NewPool: the pool needs the vector
	// extension, and doctor must work before migrations have run.
	conn, err := pgx.Connect(ctx, cfg.DatabaseURL)
	if err == nil {
		err = conn.Ping(ctx)
	}
	if err != nil {
		d.fail("postgres connection", err, "run: make up (and check DATABASE_URL in .env)")
		d.skip("extension vector", "postgres unreachable")
		d.skip("extension pg_trgm", "postgres unreachable")
		return
	}
	defer conn.Close(context.WithoutCancel(ctx))

	var version string
	if err := conn.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil {
		version = "unknown version"
	}
	d.pass("postgres connection", "PostgreSQL "+version)

	for _, ext := range []string{"vector", "pg_trgm"} {
		name := "extension " + ext
		var extVersion string
		err := conn.QueryRow(ctx, "SELECT extversion FROM pg_extension WHERE extname = $1", ext).Scan(&extVersion)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			d.fail(name, errors.New("not installed"), "run: make migrate")
		case err != nil:
			d.fail(name, err, "check the database user can read pg_extension")
		default:
			d.pass(name, "version "+extVersion)
		}
	}
}

func (d *doctor) checkOllama(ctx context.Context, cfg config.Config) {
	ctx, cancel := context.WithTimeout(ctx, doctorCheckTimeout)
	defer cancel()

	summaryCheck := "model " + cfg.OllamaSummaryModel
	embedCheck := "model " + cfg.OllamaEmbedModel
	const probeCheck = "embedding probe"

	models, err := llm.ListOllamaModels(ctx, http.DefaultClient, cfg.OllamaURL)
	if err != nil {
		d.fail("ollama reachable", err,
			"install Ollama natively (brew install ollama, or https://ollama.com/download); "+
				"start Ollama: ollama serve (or open the app); OLLAMA_URL is "+cfg.OllamaURL)
		d.skip(summaryCheck, "ollama unreachable")
		d.skip(embedCheck, "ollama unreachable")
		d.skip(probeCheck, "ollama unreachable")
		return
	}
	d.pass("ollama reachable", cfg.OllamaURL)

	for _, m := range []string{cfg.OllamaSummaryModel, cfg.OllamaEmbedModel} {
		if llm.HasModel(models, m) {
			d.pass("model "+m, "pulled")
		} else {
			d.fail("model "+m, errors.New("not pulled"), "run: ollama pull "+m)
		}
	}

	embedder, err := embed.NewOllama(cfg.OllamaURL, cfg.OllamaEmbedModel, 0)
	if err != nil {
		d.fail(probeCheck, err, "set OLLAMA_EMBED_MODEL to a model in internal/embed's template registry, or add one")
		return
	}
	if !llm.HasModel(models, cfg.OllamaEmbedModel) {
		d.skip(probeCheck, "embedding model not pulled")
		return
	}

	// The first call loads the model into memory, so allow more time.
	probeCtx, probeCancel := context.WithTimeout(context.WithoutCancel(ctx), embed.DefaultOllamaTimeout)
	defer probeCancel()
	res, err := embedder.Probe(probeCtx)
	if err != nil {
		d.fail(probeCheck, err, "check the model with: ollama run "+cfg.OllamaEmbedModel+
			"; if its output size changed, update the dimension in internal/embed")
		return
	}
	detail := fmt.Sprintf("dim %d, query norm %.4f, doc norm %.4f", embedder.Dim(), res.QueryNorm, res.DocNorm)
	if res.Normalized() {
		d.pass(probeCheck, detail)
		return
	}
	d.warn(probeCheck, detail+fmt.Sprintf(": model returns unnormalized vectors (outside [%.2f, %.2f]); the embedder L2-normalizes them",
		embed.NormMin, embed.NormMax))
}

// runProbeAll probes every registry model on the fixed inputs and prints one
// line per model. It fails only on a bad probe or a skipped configured model,
// so an optional model that is not pulled does not break the run.
func runProbeAll(ctx context.Context) error {
	cfg := config.LoadOllama()

	models, err := llm.ListOllamaModels(ctx, http.DefaultClient, cfg.OllamaURL)
	if err != nil {
		return fmt.Errorf("ollama unreachable at %s: %w", cfg.OllamaURL, err)
	}

	failed := 0
	for _, name := range embed.KnownModels() {
		e, err := embed.NewOllama(cfg.OllamaURL, name, 0)
		if err != nil {
			fmt.Printf("%-24s FAIL %v\n", name, err)
			failed++
			continue
		}
		if !llm.HasModel(models, name) {
			fmt.Printf("%-24s SKIP (not pulled)  run: ollama pull %s\n", name, name)
			// HasModel's tag rule: the configured name may be untagged or
			// ":latest", so compare with the same matching, not exact equality.
			if llm.HasModel([]string{cfg.OllamaEmbedModel}, name) {
				failed++
			}
			continue
		}
		if !probeOne(ctx, e, name) {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d model(s) failed", failed)
	}
	fmt.Println("all embedding models passed")
	return nil
}

// probeOne probes one pulled model and prints `model dim norm PASS|FAIL`. It
// returns false on a wrong dimension, a NaN/Inf value, or a norm outside
// [NormMin, NormMax].
func probeOne(ctx context.Context, e *embed.Ollama, name string) bool {
	// The first call loads the model into memory, so allow more time.
	ctx, cancel := context.WithTimeout(ctx, embed.DefaultOllamaTimeout)
	defer cancel()

	norms, err := e.ProbeNorms(ctx, probeQuery, probeDocs)
	if err != nil {
		fmt.Printf("%-24s dim %-5d norm -       FAIL  %v\n", name, e.Dim(), err)
		return false
	}
	ok, worst := true, norms[0]
	for _, n := range norms {
		if n < worst {
			worst = n
		}
		if math.IsNaN(n) || math.IsInf(n, 0) || n < embed.NormMin || n > embed.NormMax {
			ok = false
		}
	}
	verdict := "PASS"
	if !ok {
		verdict = "FAIL"
	}
	fmt.Printf("%-24s dim %-5d norm %.4f %s\n", name, e.Dim(), worst, verdict)
	return ok
}

// runOllamaChecks measures two runtime facts the truncation guard depends on:
// whether an identical generation request returns different prompt-token counts
// (prompt caching), and whether over-length embed input is rejected with
// truncation disabled.
func runOllamaChecks(ctx context.Context) error {
	cfg := config.LoadOllama()

	models, err := llm.ListOllamaModels(ctx, http.DefaultClient, cfg.OllamaURL)
	if err != nil {
		return fmt.Errorf("ollama unreachable at %s: %w", cfg.OllamaURL, err)
	}

	gen := llm.NewOllama(cfg.OllamaURL, cfg.OllamaSummaryModel, 0)
	req := llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "reply with the single word: ok"}},
	}
	first, err := gen.Complete(ctx, req)
	if err != nil {
		return fmt.Errorf("generation request 1: %w", err)
	}
	second, err := gen.Complete(ctx, req)
	if err != nil {
		return fmt.Errorf("generation request 2: %w", err)
	}
	fmt.Printf("gen_count_1 %d\n", first.PromptTokens)
	fmt.Printf("gen_count_2 %d\n", second.PromptTokens)

	failures := 0
	for _, name := range embed.KnownModels() {
		e, err := embed.NewOllama(cfg.OllamaURL, name, 0)
		if err != nil {
			return err
		}
		if !llm.HasModel(models, name) {
			fmt.Printf("embed_oversized: skip (%s not pulled)\n", name)
			continue
		}
		accepts := func(input string) bool {
			checkCtx, cancel := context.WithTimeout(ctx, embed.DefaultOllamaTimeout)
			defer cancel()
			return e.ProbeInput(checkCtx, input) == nil
		}
		verdict := "ok"
		if !accepts(oversizedProbeInput) {
			verdict = "error"
		}
		fmt.Printf("embed_oversized: %s (%s)\n", verdict, name)

		// The registry num_ctx must be the size Ollama really enforces: an
		// input within 0.8 x num_ctx (estimated, bytes/4) is accepted and one
		// at 1.5 x num_ctx is rejected. A mismatch means the registry value is
		// wrong (D-003).
		numCtx := e.NumCtx()
		within := accepts(ctxProbeInput(numCtx * 4 * 8 / 10))
		over := accepts(ctxProbeInput(numCtx * 4 * 3 / 2))
		fmt.Printf("embed_ctx_within: %s (%s, num_ctx %d)\n", okOrError(within), name, numCtx)
		fmt.Printf("embed_ctx_over: %s (%s, num_ctx %d)\n", okOrError(over), name, numCtx)
		if !within || over {
			failures++
			fmt.Printf("FAIL  %s: expected within=ok over=error at num_ctx %d; fix the registry value (D-003)\n", name, numCtx)
		}
	}
	if failures > 0 {
		return fmt.Errorf("embedding context-size check failed for %d model(s)", failures)
	}
	return nil
}

func okOrError(accepted bool) string {
	if accepted {
		return "ok"
	}
	return "error"
}

// ctxProbeInput returns about n bytes of English-like filler, which Ollama
// tokenizes at roughly 5 bytes per token (measured in S0-12), so an input of
// 0.8 x num_ctx x 4 bytes is well inside the context and 1.5 x is well over.
func ctxProbeInput(n int) string {
	const unit = "kubelet evicts pods under memory pressure "
	return strings.Repeat(unit, n/len(unit)+1)[:n]
}
