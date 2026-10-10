// Package config loads runtime configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
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

	// GroqAPIKey and GroqModel are optional until the Groq client exists.
	GroqAPIKey string
	GroqModel  string

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
	DefaultKubeDir            = "./data/kubernetes"
)

// Load reads the configuration from the process environment.
func Load() (Config, error) {
	return load(os.Getenv)
}

// LoadOllama reads the Ollama-related settings (with defaults) without
// requiring DATABASE_URL. It is for Ollama-only commands such as the doctor
// probe modes, which must work before Postgres is configured.
func LoadOllama() Config {
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
		DatabaseURL:        get("DATABASE_URL", ""),
		HTTPAddr:           get("HTTP_ADDR", DefaultHTTPAddr),
		OllamaURL:          strings.TrimRight(get("OLLAMA_URL", DefaultOllamaURL), "/"),
		OllamaSummaryModel: get("OLLAMA_SUMMARY_MODEL", DefaultOllamaSummaryModel),
		OllamaEmbedModel:   get("OLLAMA_EMBED_MODEL", DefaultOllamaEmbedModel),
		GroqAPIKey:         get("GROQ_API_KEY", ""),
		GroqModel:          get("GROQ_MODEL", ""),
		KubeTag:            get("KUBE_TAG", ""),
		KubeDir:            get("KUBE_DIR", DefaultKubeDir),
	}
}

// ErrMissing is returned when a required environment variable is unset.
var ErrMissing = errors.New("missing required environment variables")
