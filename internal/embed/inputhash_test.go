package embed

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestDocPrefix pins the per-model static document prefix (the document
// template rendered with no title and no text): qwen3 has none, nomic's is
// "search_document: ". The input hash folds this in, so a template change
// re-embeds (D-081).
func TestDocPrefix(t *testing.T) {
	tests := []struct {
		model, want string
	}{
		{"qwen3-embedding:0.6b", ""},
		{"nomic-embed-text", "search_document: "},
	}
	for _, tt := range tests {
		e, err := NewOllama("http://localhost:11434", tt.model, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := e.DocPrefix(); got != tt.want {
			t.Errorf("%s DocPrefix() = %q, want %q", tt.model, got, tt.want)
		}
	}
}

// TestInputHashCoversModelTemplateAndParts proves the input hash changes when
// the model, the stored document template source or any part changes, and that
// a raw hash (one part) and a header hash (two parts) are distinct (D-081).
func TestInputHashCoversModelTemplateAndParts(t *testing.T) {
	nomic, err := NewOllama("http://localhost:11434", "nomic-embed-text", 0)
	if err != nil {
		t.Fatal(err)
	}
	qwen, err := NewOllama("http://localhost:11434", "qwen3-embedding:0.6b", 0)
	if err != nil {
		t.Fatal(err)
	}

	content := "func evictPod()"
	content2 := "func evictPod(pod *v1.Pod)"

	// The spec stores the raw document template source, not just the compiled
	// template: a newSpec that forgot to keep docSrc would leave the hash
	// blind to a template edit. Pin it to the registry source (D-081).
	wantDocSrc := map[string]string{
		"nomic-embed-text":     "search_document: {{if .Title}}{{.Title}}\n{{end}}{{.Text}}",
		"qwen3-embedding:0.6b": "{{if .Title}}{{.Title}}\n{{end}}{{.Text}}",
	}
	for _, e := range []*Ollama{nomic, qwen} {
		if got := e.spec.docSrc; got != wantDocSrc[e.Model()] {
			t.Errorf("%s spec.docSrc = %q, want the registry document source %q", e.Model(), got, wantDocSrc[e.Model()])
		}
	}

	if nomic.InputHash(content) == nomic.InputHash(content2) {
		t.Error("input hash did not change with content")
	}
	if nomic.InputHash(content) == qwen.InputHash(content) {
		t.Error("input hash did not change with model")
	}
	// One part vs two parts with the same concatenation must not collide: the
	// header is a distinct field.
	if nomic.InputHash(content) == nomic.InputHash("", content) {
		t.Error("raw and header hashes collided")
	}
	if nomic.InputHash("h", content) == nomic.InputHash("h2", content) {
		t.Error("input hash did not change with header")
	}
	// The stored template source is folded, not the empty-title render: a
	// template edit that leaves the empty render unchanged still changes the
	// hash (spec line 124: H(model, template, content)).
	const changedSrc = "search_document: {{/* changed */}}{{if .Title}}{{.Title}}\n{{end}}{{.Text}}"
	before := nomic.InputHash(content)
	nomic.spec.docSrc = changedSrc
	if before == nomic.InputHash(content) {
		t.Error("input hash did not change with the document template source")
	}
	if nomic.DocPrefix() != "search_document: " {
		t.Errorf("DocPrefix() = %q, want the unchanged render %q", nomic.DocPrefix(), "search_document: ")
	}
}

// TestEmbedWrongDimensionIsTyped proves a wrong-dimension reply is reported as
// ErrWrongDimension, so the embed stage treats it as a hard error rather than a
// truncation failure.
func TestEmbedWrongDimensionIsTyped(t *testing.T) {
	srv, _ := fakeOllama(t, func(int) []float64 { return constVec(768, 1) })
	e, err := NewOllama(srv.URL, "qwen3-embedding:0.6b", 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.EmbedDocuments(context.Background(), []Document{{Text: "x"}})
	if !errors.Is(err, ErrWrongDimension) {
		t.Fatalf("err = %v, want errors.Is ErrWrongDimension", err)
	}
	if err == nil || !strings.Contains(err.Error(), "768 dimensions, want 1024") {
		t.Fatalf("err = %v, want the dimension mismatch message", err)
	}
}

// TestEmbedSendsTruncateFalse proves every query and document request carries
// truncate:false, so an over-length input is rejected rather than silently cut
// (architecture "Ollama requests").
func TestEmbedSendsTruncateFalse(t *testing.T) {
	srv, bodies := captureOllama(t, 1024)
	e, err := NewOllama(srv.URL, "qwen3-embedding:0.6b", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.EmbedQueries(context.Background(), []string{"q"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.EmbedDocuments(context.Background(), []Document{{Text: "d"}}); err != nil {
		t.Fatal(err)
	}
	if len(*bodies) != 2 {
		t.Fatalf("got %d requests, want 2", len(*bodies))
	}
	for i, b := range *bodies {
		if !strings.Contains(b, `"truncate":false`) {
			t.Errorf("request %d body = %s, want truncate:false", i, b)
		}
	}
}
