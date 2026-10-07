// Package embed turns queries and documents into vectors. Embedding models
// are asymmetric: a query and a document about the same code get different
// prefixes, so the interface keeps the two apart.
package embed

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"text/template"
)

// Embedder embeds search queries and indexed documents.
type Embedder interface {
	EmbedQueries(ctx context.Context, queries []string) ([][]float32, error)
	EmbedDocuments(ctx context.Context, docs []Document) ([][]float32, error)
	// Dim is the length of every returned vector.
	Dim() int
	// Model is the embedding model name, stored alongside each vector.
	Model() string
}

// Document is one item to index, e.g. a code chunk or a summary.
type Document struct {
	Title string
	Text  string
}

// modelSpec holds the prompt templates and vector size for one model.
//
// These templates MUST be checked against each model's card before
// indexing: a wrong prefix silently degrades retrieval quality.
type modelSpec struct {
	dim   int
	query *template.Template
	doc   *template.Template
}

var registry = map[string]modelSpec{
	"embeddinggemma-2:270m": newSpec(768,
		"task: code retrieval | query: {{.Text}}",
		"title: {{.Title}} | text: {{.Text}}"),
	"qwen3-embedding:0.6b": newSpec(1024,
		"Instruct: Given a search query, retrieve relevant code\nQuery: {{.Text}}",
		"{{.Text}}"),
	"nomic-embed-text": newSpec(768,
		"search_query: {{.Text}}",
		"search_document: {{.Text}}"),
}

func newSpec(dim int, query, doc string) modelSpec {
	parse := func(src string) *template.Template {
		return template.Must(template.New("").Option("missingkey=error").Parse(src))
	}
	return modelSpec{dim: dim, query: parse(query), doc: parse(doc)}
}

// KnownModels lists the models with a template registry entry.
func KnownModels() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func lookup(model string) (modelSpec, error) {
	spec, ok := registry[model]
	if !ok {
		return modelSpec{}, fmt.Errorf("embedding model %q has no prompt templates; use one of: %s",
			model, strings.Join(KnownModels(), ", "))
	}
	return spec, nil
}

func (s modelSpec) renderQuery(q string) (string, error) {
	return render(s.query, Document{Text: q})
}

func (s modelSpec) renderDoc(d Document) (string, error) {
	if d.Title == "" {
		d.Title = "none"
	}
	return render(s.doc, d)
}

func render(t *template.Template, d Document) (string, error) {
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		return "", fmt.Errorf("render embedding prompt: %w", err)
	}
	return buf.String(), nil
}
