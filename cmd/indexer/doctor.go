package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/llm"
)

const doctorCheckTimeout = 10 * time.Second

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
	if err := parseFlags("doctor", args); err != nil {
		return err
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
			"install Ollama natively (brew install ollama, or https://ollama.com/download), "+
				"then start it (open the app or run: ollama serve); OLLAMA_URL is "+cfg.OllamaURL)
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
