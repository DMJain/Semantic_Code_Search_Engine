// Package config loads runtime configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the typed runtime configuration shared by every binary.
type Config struct {
	// DatabaseURL is the Postgres connection string. Required.
	DatabaseURL string
	// HTTPAddr is the listen address for cmd/server.
	HTTPAddr string

	OllamaURL          string
	OllamaSummaryModel string
	OllamaEmbedModel   string

	// OllamaSummaryNumCtx is the explicit context size sent with every
	// function-summary request (D-015). It is never zero: Load falls back to
	// DefaultOllamaSummaryNumCtx on an unset or invalid value.
	OllamaSummaryNumCtx int

	// GroqAPIKey and GroqModel are optional until the Groq client exists.
	GroqAPIKey string
	GroqModel  string

	// GithubToken is a fine-grained read-only token for public repos, used by
	// the eval GitHub probes. Optional; commands that need it check it
	// themselves and never print it.
	GithubToken string

	// KubeTag is the Kubernetes release tag to index. Only fetch-repo
	// requires it, so it is validated there rather than here.
	KubeTag string
	KubeDir string
}

// Defaults for optional keys.
const (
	DefaultHTTPAddr           = ":8080"
	DefaultOllamaURL          = "http://localhost:11434"
	DefaultOllamaSummaryModel = "qwen2.5-coder:7b"
	DefaultOllamaEmbedModel   = "qwen3-embedding:0.6b"
	// DefaultOllamaSummaryNumCtx is the summary request context size (D-015).
	DefaultOllamaSummaryNumCtx = 4096
	DefaultKubeDir             = "./data/kubernetes"

	// DefaultCallgraphResultPath and DefaultGTStorePath are the call-graph
	// evaluation artifacts. The report reads the GT store partition that
	// callgraph-gt writes, so both commands share one source of truth and the
	// two paths cannot drift.
	DefaultCallgraphResultPath = "eval/results/callgraph/result.json"
	DefaultGTStorePath         = "eval/results/callgraph/gt-store.json"
	// DefaultErrorSamplePath is the seeded error sample the error-analysis
	// ticket commits alongside the report.
	DefaultErrorSamplePath = "eval/results/callgraph/error-sample.json"
)

// Load reads the configuration from the process environment.
func Load() (Config, error) {
	return load(os.Getenv)
}

// LoadEnv reads every setting (with defaults) without requiring any key. It is
// for commands that need only optional values and no database, such as the
// doctor probe modes and the GitHub mining probe.
func LoadEnv() Config {
	return loadEnv(os.Getenv)
}

func load(getenv func(string) string) (Config, error) {
	cfg := loadEnv(getenv)

	var missing []string
	if cfg.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("%w: %s (see .env.example)", ErrMissing, strings.Join(missing, ", "))
	}
	return cfg, nil
}

func loadEnv(getenv func(string) string) Config {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}

	return Config{
		DatabaseURL:         get("DATABASE_URL", ""),
		HTTPAddr:            get("HTTP_ADDR", DefaultHTTPAddr),
		OllamaURL:           strings.TrimRight(get("OLLAMA_URL", DefaultOllamaURL), "/"),
		OllamaSummaryModel:  get("OLLAMA_SUMMARY_MODEL", DefaultOllamaSummaryModel),
		OllamaEmbedModel:    get("OLLAMA_EMBED_MODEL", DefaultOllamaEmbedModel),
		OllamaSummaryNumCtx: getInt(getenv, "OLLAMA_SUMMARY_NUM_CTX", DefaultOllamaSummaryNumCtx),
		GroqAPIKey:          get("GROQ_API_KEY", ""),
		GroqModel:           get("GROQ_MODEL", ""),
		GithubToken:         get("GITHUB_TOKEN", ""),
		KubeTag:             get("KUBE_TAG", ""),
		KubeDir:             get("KUBE_DIR", DefaultKubeDir),
	}
}

// getInt returns the positive integer in key, or def when the variable is
// unset, not an integer, or not positive.
func getInt(getenv func(string) string, key string, def int) int {
	v := strings.TrimSpace(getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// ErrMissing is returned when a required environment variable is unset.
var ErrMissing = errors.New("missing required environment variables")
