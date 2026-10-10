package config

import (
	"errors"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := Config{
		DatabaseURL:        "postgres://x",
		HTTPAddr:           DefaultHTTPAddr,
		OllamaURL:          DefaultOllamaURL,
		OllamaSummaryModel: DefaultOllamaSummaryModel,
		OllamaEmbedModel:   DefaultOllamaEmbedModel,
		KubeDir:            DefaultKubeDir,
	}
	if cfg != want {
		t.Errorf("got %+v\nwant %+v", cfg, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := load(env(map[string]string{
		"DATABASE_URL":         "postgres://x",
		"HTTP_ADDR":            ":9000",
		"OLLAMA_URL":           "http://ollama:11434/",
		"OLLAMA_SUMMARY_MODEL": "llama3",
		"OLLAMA_EMBED_MODEL":   "nomic-embed-text",
		"GROQ_API_KEY":         "key",
		"GROQ_MODEL":           "m",
		"GITHUB_TOKEN":         "tok",
		"KUBE_TAG":             "v1.0.0",
		"KUBE_DIR":             "/tmp/k",
	}))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.OllamaURL != "http://ollama:11434" {
		t.Errorf("OllamaURL = %q, want trailing slash trimmed", cfg.OllamaURL)
	}
	if cfg.HTTPAddr != ":9000" || cfg.OllamaSummaryModel != "llama3" || cfg.OllamaEmbedModel != "nomic-embed-text" ||
		cfg.GroqAPIKey != "key" || cfg.GroqModel != "m" || cfg.GithubToken != "tok" ||
		cfg.KubeTag != "v1.0.0" || cfg.KubeDir != "/tmp/k" {
		t.Errorf("overrides not applied: %+v", cfg)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	for name, vars := range map[string]map[string]string{
		"unset": {},
		"blank": {"DATABASE_URL": "   "},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := load(env(vars))
			if !errors.Is(err, ErrMissing) {
				t.Fatalf("err = %v, want ErrMissing", err)
			}
		})
	}
}
