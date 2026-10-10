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
	fs := newFlagSet("doctor")
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

// registryModel is one embedding model from the registry together with
// whether Ollama has it pulled.
type registryModel struct {
	name   string
	e      *embed.Ollama
	pulled bool
}

// registryModels builds an embedder for every registry model and marks which
// are in pulledNames (HasModel's tag matching).
func registryModels(ollamaURL string, pulledNames []string) ([]registryModel, error) {
	var out []registryModel
	for _, name := range embed.KnownModels() {
		e, err := embed.NewOllama(ollamaURL, name, 0)
		if err != nil {
			return nil, err
		}
		out = append(out, registryModel{name: name, e: e, pulled: llm.HasModel(pulledNames, name)})
	}
	return out, nil
}

// verdict names a boolean outcome, for example verdict(ok, "PASS", "FAIL").
func verdict(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}
