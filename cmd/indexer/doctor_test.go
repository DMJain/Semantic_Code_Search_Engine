package main

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// captureRun drives the CLI entry point and returns stdout plus the exit code,
// so the tests assert external behavior only.
func captureRun(t *testing.T, args ...string) (string, int) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := run(args)
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out), code
}

// unitVec returns a dim-length vector whose L2 norm is norm.
func unitVec(dim int, norm float64) []float64 {
	v := make([]float64, dim)
	for i := range v {
		v[i] = norm / math.Sqrt(float64(dim))
	}
	return v
}

// fakeOllama serves just enough of the Ollama API for the doctor modes. The
// embed function returns (vectors, httpStatus, body); a zero status means 200.
type fakeOllama struct {
	t          *testing.T
	models     []string
	embed      func(model string, inputs []string, truncate *bool) ([][]float64, int, string)
	chatCounts []int

	mu        sync.Mutex
	chatCalls int
}

func (f *fakeOllama) start() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	f.t.Cleanup(srv.Close)
	return srv
}

func (f *fakeOllama) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/tags":
		ms := make([]map[string]string, len(f.models))
		for i, m := range f.models {
			ms[i] = map[string]string{"name": m}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": ms})
	case "/api/embed":
		var req struct {
			Model    string   `json:"model"`
			Input    []string `json:"input"`
			Truncate *bool    `json:"truncate"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			f.t.Errorf("decode embed request: %v", err)
		}
		vecs, status, body := f.embed(req.Model, req.Input, req.Truncate)
		if status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": vecs})
	case "/api/chat":
		f.mu.Lock()
		i := f.chatCalls
		f.chatCalls++
		f.mu.Unlock()
		count := 0
		if i < len(f.chatCounts) {
			count = f.chatCounts[i]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":             "m",
			"message":           map[string]string{"role": "assistant", "content": "ok"},
			"prompt_eval_count": count,
			"eval_count":        1,
		})
	default:
		http.NotFound(w, r)
	}
}

func TestDoctorOllamaUnreachableFixMessage(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	t.Setenv("OLLAMA_URL", url)
	t.Setenv("DATABASE_URL", "postgres://x:x@127.0.0.1:1/x?sslmode=disable")

	out, code := captureRun(t, "doctor")
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero; output:\n%s", out)
	}
	if !strings.Contains(out, "FAIL  ollama reachable") {
		t.Errorf("failing check not named:\n%s", out)
	}
	if !strings.Contains(out, "start Ollama: ollama serve") {
		t.Errorf("fix message missing %q:\n%s", "start Ollama: ollama serve", out)
	}
}

func TestProbeAllGoodModelsPass(t *testing.T) {
	f := &fakeOllama{
		t:      t,
		models: []string{"qwen3-embedding:0.6b", "nomic-embed-text:latest"},
		embed: func(model string, inputs []string, _ *bool) ([][]float64, int, string) {
			dim := 1024
			if model == "nomic-embed-text" {
				dim = 768
			}
			out := make([][]float64, len(inputs))
			for i := range out {
				out[i] = unitVec(dim, 1)
			}
			return out, 0, ""
		},
	}
	srv := f.start()
	t.Setenv("OLLAMA_URL", srv.URL)
	t.Setenv("OLLAMA_EMBED_MODEL", "qwen3-embedding:0.6b")

	out, code := captureRun(t, "doctor", "--probe-all")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, out)
	}
	if got := strings.Count(out, "PASS"); got != 2 {
		t.Fatalf("PASS count = %d, want 2; output:\n%s", got, out)
	}
	if strings.Contains(out, "FAIL") {
		t.Errorf("unexpected FAIL:\n%s", out)
	}
}

func TestProbeAllWrongDimensionFailsWithBothSizes(t *testing.T) {
	f := &fakeOllama{
		t:      t,
		models: []string{"qwen3-embedding:0.6b"},
		embed: func(_ string, inputs []string, _ *bool) ([][]float64, int, string) {
			out := make([][]float64, len(inputs))
			for i := range out {
				out[i] = unitVec(768, 1) // wrong for qwen3 (1024)
			}
			return out, 0, ""
		},
	}
	srv := f.start()
	t.Setenv("OLLAMA_URL", srv.URL)
	t.Setenv("OLLAMA_EMBED_MODEL", "qwen3-embedding:0.6b")

	out, code := captureRun(t, "doctor", "--probe-all")
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero; output:\n%s", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("no FAIL in output:\n%s", out)
	}
	if !strings.Contains(out, "768") || !strings.Contains(out, "1024") {
		t.Errorf("want both actual 768 and expected 1024 in output:\n%s", out)
	}
}

func TestProbeAllNaNInfVectorFails(t *testing.T) {
	f := &fakeOllama{
		t:      t,
		models: []string{"qwen3-embedding:0.6b"},
		embed: func(_ string, inputs []string, _ *bool) ([][]float64, int, string) {
			out := make([][]float64, len(inputs))
			for i := range out {
				v := unitVec(1024, 1)
				v[3] = 1e39 // overflows float32 -> +Inf
				out[i] = v
			}
			return out, 0, ""
		},
	}
	srv := f.start()
	t.Setenv("OLLAMA_URL", srv.URL)
	t.Setenv("OLLAMA_EMBED_MODEL", "qwen3-embedding:0.6b")

	out, code := captureRun(t, "doctor", "--probe-all")
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero; output:\n%s", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("no FAIL in output:\n%s", out)
	}
}

func TestProbeAllSkippedConfiguredModelFails(t *testing.T) {
	f := &fakeOllama{
		t:      t,
		models: []string{"nomic-embed-text:latest"}, // configured qwen3 missing
		embed: func(_ string, inputs []string, _ *bool) ([][]float64, int, string) {
			out := make([][]float64, len(inputs))
			for i := range out {
				out[i] = unitVec(768, 1)
			}
			return out, 0, ""
		},
	}
	srv := f.start()
	t.Setenv("OLLAMA_URL", srv.URL)
	t.Setenv("OLLAMA_EMBED_MODEL", "qwen3-embedding:0.6b")

	out, code := captureRun(t, "doctor", "--probe-all")
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero (configured model skipped); output:\n%s", out)
	}
	if !strings.Contains(out, "SKIP (not pulled)") || !strings.Contains(out, "ollama pull qwen3-embedding:0.6b") {
		t.Errorf("want SKIP plus pull command:\n%s", out)
	}
}

func TestProbeAllSkippedOptionalModelStillExitsZero(t *testing.T) {
	f := &fakeOllama{
		t:      t,
		models: []string{"qwen3-embedding:0.6b"}, // optional nomic missing
		embed: func(_ string, inputs []string, _ *bool) ([][]float64, int, string) {
			out := make([][]float64, len(inputs))
			for i := range out {
				out[i] = unitVec(1024, 1)
			}
			return out, 0, ""
		},
	}
	srv := f.start()
	t.Setenv("OLLAMA_URL", srv.URL)
	t.Setenv("OLLAMA_EMBED_MODEL", "qwen3-embedding:0.6b")

	out, code := captureRun(t, "doctor", "--probe-all")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, out)
	}
	if !strings.Contains(out, "SKIP (not pulled)") {
		t.Errorf("want the optional model skipped:\n%s", out)
	}
}

func TestProbeAllSkippedConfiguredModelWithLatestTagFails(t *testing.T) {
	f := &fakeOllama{
		t:      t,
		models: []string{"qwen3-embedding:0.6b"}, // configured nomic-embed-text missing
		embed: func(_ string, inputs []string, _ *bool) ([][]float64, int, string) {
			out := make([][]float64, len(inputs))
			for i := range out {
				out[i] = unitVec(1024, 1)
			}
			return out, 0, ""
		},
	}
	srv := f.start()
	t.Setenv("OLLAMA_URL", srv.URL)
	// The configured name carries an explicit :latest tag; the skipped registry
	// name is untagged. They must still be treated as the same model.
	t.Setenv("OLLAMA_EMBED_MODEL", "nomic-embed-text:latest")

	out, code := captureRun(t, "doctor", "--probe-all")
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero (configured model skipped, :latest tag); output:\n%s", out)
	}
	if !strings.Contains(out, "SKIP (not pulled)") {
		t.Errorf("want SKIP line:\n%s", out)
	}
}

func TestOllamaChecksPrintsCountsAndOversizedError(t *testing.T) {
	f := &fakeOllama{
		t:      t,
		models: []string{"qwen3-embedding:0.6b", "nomic-embed-text:latest"},
		embed: func(model string, inputs []string, truncate *bool) ([][]float64, int, string) {
			if truncate != nil && !*truncate {
				return nil, http.StatusBadRequest, `{"error":"input length exceeds context window"}`
			}
			dim := 1024
			if model == "nomic-embed-text" {
				dim = 768
			}
			out := make([][]float64, len(inputs))
			for i := range out {
				out[i] = unitVec(dim, 1)
			}
			return out, 0, ""
		},
		chatCounts: []int{42, 35},
	}
	srv := f.start()
	t.Setenv("OLLAMA_URL", srv.URL)
	t.Setenv("OLLAMA_EMBED_MODEL", "qwen3-embedding:0.6b")
	t.Setenv("OLLAMA_SUMMARY_MODEL", "qwen2.5-coder:7b")

	out, code := captureRun(t, "doctor", "--ollama-checks")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; output:\n%s", code, out)
	}
	for _, want := range []string{"gen_count_1 42", "gen_count_2 35", "embed_oversized: error"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if got := strings.Count(out, "embed_oversized: error"); got != 2 {
		t.Errorf("embed_oversized error count = %d, want 2 (one per model):\n%s", got, out)
	}
}
