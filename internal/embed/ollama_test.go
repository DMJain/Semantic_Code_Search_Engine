package embed

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTemplates(t *testing.T) {
	tests := []struct {
		model     string
		query     string
		doc       Document
		wantQuery string
		wantDoc   string
	}{
		{
			model:     "embeddinggemma-2:270m",
			query:     "pod eviction",
			doc:       Document{Title: "evictPod", Text: "func evictPod()"},
			wantQuery: "task: code retrieval | query: pod eviction",
			wantDoc:   "title: evictPod | text: func evictPod()",
		},
		{
			model:     "embeddinggemma-2:270m",
			query:     "q",
			doc:       Document{Text: "body"},
			wantQuery: "task: code retrieval | query: q",
			wantDoc:   "title: none | text: body",
		},
		{
			model:     "qwen3-embedding:0.6b",
			query:     "pod eviction",
			doc:       Document{Title: "ignored", Text: "func evictPod()"},
			wantQuery: "Instruct: Given a search query, retrieve relevant code\nQuery: pod eviction",
			wantDoc:   "func evictPod()",
		},
		{
			model:     "nomic-embed-text",
			query:     "pod eviction",
			doc:       Document{Text: "func evictPod()"},
			wantQuery: "search_query: pod eviction",
			wantDoc:   "search_document: func evictPod()",
		},
	}
	for _, tt := range tests {
		t.Run(tt.model+"/"+tt.query, func(t *testing.T) {
			spec, err := lookup(tt.model)
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := spec.renderQuery(tt.query); got != tt.wantQuery {
				t.Errorf("query = %q, want %q", got, tt.wantQuery)
			}
			if got, _ := spec.renderDoc(tt.doc); got != tt.wantDoc {
				t.Errorf("doc = %q, want %q", got, tt.wantDoc)
			}
		})
	}
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

func TestCheckVectorNaN(t *testing.T) {
	// NaN cannot travel through JSON, so check the validator directly.
	v := []float32{0.5, float32(math.NaN())}
	if err := checkVector(v, 2); err == nil || !strings.Contains(err.Error(), "NaN") {
		t.Fatalf("err = %v, want NaN error", err)
	}
}
