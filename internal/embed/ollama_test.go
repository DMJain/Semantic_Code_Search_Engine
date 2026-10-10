package embed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestKnownModels(t *testing.T) {
	got := KnownModels()
	want := []string{"nomic-embed-text", "qwen3-embedding:0.6b"}
	if !slices.Equal(got, want) {
		t.Fatalf("KnownModels() = %v, want %v (registry must hold exactly two models)", got, want)
	}
	if len(got) != 2 {
		t.Fatalf("len(KnownModels()) = %d, want 2", len(got))
	}
}

// TestRequestBodies drives the PUBLIC embedder against a fake Ollama and
// asserts the exact request body bytes (whitespace and newlines included).
// The document rule: a non-empty title is prepended as its own line.
func TestRequestBodies(t *testing.T) {
	const titled = "pkg/kubelet/kubelet.go (*Kubelet).evictPod"
	tests := []struct {
		name      string
		model     string
		dim       int
		query     string
		hasQuery  bool
		doc       Document
		hasDoc    bool
		wantInput string
	}{
		{
			name: "qwen3 query", model: "qwen3-embedding:0.6b", dim: 1024,
			query:     "pod eviction",
			hasQuery:  true,
			wantInput: "Instruct: Given a search query, retrieve relevant code\nQuery:pod eviction",
		},
		{
			name: "qwen3 document without title", model: "qwen3-embedding:0.6b", dim: 1024,
			doc:       Document{Text: "func evictPod()"},
			hasDoc:    true,
			wantInput: "func evictPod()",
		},
		{
			name: "qwen3 document with title", model: "qwen3-embedding:0.6b", dim: 1024,
			doc:       Document{Title: titled, Text: "func evictPod()"},
			hasDoc:    true,
			wantInput: titled + "\nfunc evictPod()",
		},
		{
			name: "nomic query", model: "nomic-embed-text", dim: 768,
			query:     "pod eviction",
			hasQuery:  true,
			wantInput: "search_query: pod eviction",
		},
		{
			name: "nomic document without title", model: "nomic-embed-text", dim: 768,
			doc:       Document{Text: "func evictPod()"},
			hasDoc:    true,
			wantInput: "search_document: func evictPod()",
		},
		{
			name: "nomic document with title", model: "nomic-embed-text", dim: 768,
			doc:       Document{Title: titled, Text: "func evictPod()"},
			hasDoc:    true,
			wantInput: "search_document: " + titled + "\nfunc evictPod()",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, bodies := captureOllama(t, tt.dim)
			e, err := NewOllama(srv.URL, tt.model, 0)
			if err != nil {
				t.Fatal(err)
			}
			if tt.hasQuery {
				if _, err := e.EmbedQueries(context.Background(), []string{tt.query}); err != nil {
					t.Fatalf("EmbedQueries: %v", err)
				}
			} else {
				if _, err := e.EmbedDocuments(context.Background(), []Document{tt.doc}); err != nil {
					t.Fatalf("EmbedDocuments: %v", err)
				}
			}
			if len(*bodies) != 1 {
				t.Fatalf("got %d requests, want 1", len(*bodies))
			}
			if got, want := (*bodies)[0], wantBody(tt.model, tt.wantInput); got != want {
				t.Errorf("request body mismatch:\n got %q\nwant %q", got, want)
			}
		})
	}
}

// wantBody builds the exact request body Embedder should send for one input.
func wantBody(model, input string) string {
	b, err := json.Marshal(map[string]any{"model": model, "input": []string{input}})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// captureOllama is a fake /api/embed that records each raw request body and
// answers with one dim-length unit vector per input.
func captureOllama(t *testing.T, dim int) (*httptest.Server, *[]string) {
	t.Helper()
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		bodies = append(bodies, string(b))
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.Unmarshal(b, &req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out := make([][]float64, len(req.Input))
		for i := range out {
			out[i] = constVec(dim, 1)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"embeddings": out}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func TestUnknownModel(t *testing.T) {
	_, err := NewOllama("http://localhost:11434", "mystery-embed", 0)
	if err == nil || !strings.Contains(err.Error(), "mystery-embed") || !strings.Contains(err.Error(), "nomic-embed-text") {
		t.Fatalf("err = %v, want unknown-model error listing known models", err)
	}
}

// fakeOllama serves /api/embed, answering every input with vec and
// recording the inputs it received.
func fakeOllama(t *testing.T, vec func(i int) []float64) (*httptest.Server, *[]string) {
	t.Helper()
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Errorf("path = %s, want /api/embed", r.URL.Path)
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		got = append(got, req.Input...)
		out := make([][]float64, len(req.Input))
		for i := range req.Input {
			out[i] = vec(i)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"embeddings": out}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

// constVec returns a dim-length vector whose L2 norm is norm.
func constVec(dim int, norm float64) []float64 {
	v := make([]float64, dim)
	for i := range v {
		v[i] = norm / math.Sqrt(float64(dim))
	}
	return v
}

func TestEmbedPrefixesAndNormalizes(t *testing.T) {
	srv, inputs := fakeOllama(t, func(int) []float64 { return constVec(768, 3) })
	e, err := NewOllama(srv.URL, "nomic-embed-text", 0)
	if err != nil {
		t.Fatal(err)
	}

	qv, err := e.EmbedQueries(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("EmbedQueries: %v", err)
	}
	dv, err := e.EmbedDocuments(context.Background(), []Document{{Text: "c"}})
	if err != nil {
		t.Fatalf("EmbedDocuments: %v", err)
	}
	want := []string{"search_query: a", "search_query: b", "search_document: c"}
	if strings.Join(*inputs, "|") != strings.Join(want, "|") {
		t.Errorf("inputs = %q, want %q", *inputs, want)
	}
	for _, v := range append(qv, dv...) {
		if len(v) != e.Dim() {
			t.Fatalf("len = %d, want %d", len(v), e.Dim())
		}
		if n := l2(v); math.Abs(n-1) > 1e-4 {
			t.Errorf("norm = %v, want 1", n)
		}
	}
}

func TestProbe(t *testing.T) {
	tests := []struct {
		name           string
		vec            func(i int) []float64
		wantErr        string
		wantNormalized bool
	}{
		{name: "ok", vec: func(int) []float64 { return constVec(1024, 1) }, wantNormalized: true},
		{name: "unnormalized is reported, not failed", vec: func(int) []float64 { return constVec(1024, 12.5) }},
		{name: "wrong dimension", vec: func(int) []float64 { return constVec(768, 1) }, wantErr: "768 dimensions, want 1024"},
		{name: "infinite value", vec: func(int) []float64 {
			v := constVec(1024, 1)
			v[3] = 1e39 // overflows float32
			return v
		}, wantErr: "+Inf"},
		{name: "zero vector", vec: func(int) []float64 { return make([]float64, 1024) }, wantErr: "zero vector"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := fakeOllama(t, tt.vec)
			e, err := NewOllama(srv.URL, "qwen3-embedding:0.6b", 0)
			if err != nil {
				t.Fatal(err)
			}
			res, err := e.Probe(context.Background())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			if res.Normalized() != tt.wantNormalized {
				t.Errorf("Normalized() = %v (norms %v, %v), want %v", res.Normalized(), res.QueryNorm, res.DocNorm, tt.wantNormalized)
			}
		})
	}
}

func TestProbeHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	e, err := NewOllama(srv.URL, "qwen3-embedding:0.6b", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Probe(context.Background()); err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("err = %v, want 404 body", err)
	}
}

func TestProbeOversizedInputSendsTruncateFalse(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		http.Error(w, `{"error":"input length exceeds context window"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	e, err := NewOllama(srv.URL, "nomic-embed-text", 0)
	if err != nil {
		t.Fatal(err)
	}
	err = e.ProbeOversizedInput(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "input length exceeds context window") {
		t.Fatalf("err = %v, want the server error", err)
	}
	if !strings.Contains(got, `"truncate":false`) {
		t.Errorf("request body = %s, want truncate:false", got)
	}
}

func TestCheckVectorNaN(t *testing.T) {
	// NaN cannot travel through JSON, so check the validator directly.
	v := []float32{0.5, float32(math.NaN())}
	if err := checkVector(v, 2); err == nil || !strings.Contains(err.Error(), "NaN") {
		t.Fatalf("err = %v, want NaN error", err)
	}
}

// TestRenderedTemplateHashesArePinned fixes the exact bytes of every rendered
// template with the sha256 values recorded in D-003 (S0-T05). A change to any
// template, even one character, changes a hash and fails here, so the change
// must be made deliberately together with a new decision.
func TestRenderedTemplateHashesArePinned(t *testing.T) {
	const (
		query = "pod eviction"
		text  = "func evictPod()"
		title = "pkg/kubelet/kubelet.go (*Kubelet).evictPod"
	)
	tests := []struct {
		name, model string
		dim         int
		isQuery     bool
		doc         Document
		wantSHA256  string
	}{
		{"qwen3 query", "qwen3-embedding:0.6b", 1024, true, Document{}, "f44facdd632c133383e6f36cc06c141f9f16776d97926ee800046edc40001e6a"},
		{"qwen3 document", "qwen3-embedding:0.6b", 1024, false, Document{Text: text}, "f9c00a192adb9bc11cd97b3c420b90e0564ec31f3d0676d4ec0d88ed2186893d"},
		{"qwen3 document with title", "qwen3-embedding:0.6b", 1024, false, Document{Title: title, Text: text}, "99a44a99894d86b7b56aa3069fdf864461be551741c306426b5bc9504275384c"},
		{"nomic query", "nomic-embed-text", 768, true, Document{}, "e1ce55bdb8c19ba3bc53a73c71aa5cf2869d5c9415cda10da19dda0defcb171f"},
		{"nomic document", "nomic-embed-text", 768, false, Document{Text: text}, "7de036e71c1bb500bad38d85ad0bfd141cd3fa5b284430cd5e6ab5840aca29d0"},
		{"nomic document with title", "nomic-embed-text", 768, false, Document{Title: title, Text: text}, "886261933d0f40c5aaa60799ad69f2a33b9b82f38167a44afa39f59997e37b92"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, bodies := captureOllama(t, tt.dim)
			e, err := NewOllama(srv.URL, tt.model, 0)
			if err != nil {
				t.Fatal(err)
			}
			if tt.isQuery {
				_, err = e.EmbedQueries(context.Background(), []string{query})
			} else {
				_, err = e.EmbedDocuments(context.Background(), []Document{tt.doc})
			}
			if err != nil {
				t.Fatal(err)
			}
			var req struct {
				Input []string `json:"input"`
			}
			if err := json.Unmarshal([]byte((*bodies)[0]), &req); err != nil || len(req.Input) != 1 {
				t.Fatalf("decode request: %v (%d inputs)", err, len(req.Input))
			}
			sum := sha256.Sum256([]byte(req.Input[0]))
			if got := hex.EncodeToString(sum[:]); got != tt.wantSHA256 {
				t.Errorf("sha256 of rendered %q = %s, want %s (template changed? record a decision)", req.Input[0], got, tt.wantSHA256)
			}
		})
	}
}
