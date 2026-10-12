//go:build live

package summarize

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestLiveSummarize runs one small symbol against the real Ollama server. It
// is opt-in (`make test-live`, build tag "live") so the default run has no
// skip; with the tag it fails, never skips, when OLLAMA_URL is not set.
func TestLiveSummarize(t *testing.T) {
	url := os.Getenv("OLLAMA_URL")
	if url == "" {
		t.Fatal("OLLAMA_URL not set; the live test never skips")
	}
	model := os.Getenv("OLLAMA_SUMMARY_MODEL")
	if model == "" {
		model = "qwen2.5-coder:7b"
	}
	in := Input{
		Signature: "func add(a, b int) int",
		Doc:       "// add returns the sum of a and b.",
		Code:      "func add(a, b int) int {\n\treturn a + b\n}",
	}
	got := NewClient(url, model, 4096).Summarize(context.Background(), SystemPrompt, in.UserMessage())
	if !got.OK() {
		t.Fatalf("live summarize reason = %q", got.Reason)
	}
	if strings.TrimSpace(got.Text) == "" {
		t.Fatal("live summarize returned empty text")
	}
	t.Logf("live summary: %q", got.Text)
}
