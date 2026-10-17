// Package embed turns queries and documents into vectors. Embedding models
// are asymmetric: a query and a document about the same code get different
// prefixes, so the interface keeps the two apart.
package embed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
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
	dim int
	// numCtx is the context size sent as options.num_ctx on every embed
	// request (D-015). Ollama clamps it to the model's own context length,
	// so it must not exceed what the model supports (D-003 outcome).
	numCtx int
	query  *template.Template
	doc    *template.Template
	// docSrc is the raw document template source. InputHash folds this
	// source rather than the rendered empty-title prefix, so any template
	// change — even one that leaves the empty render unchanged — changes the
	// hash and re-embeds (D-081, spec line 124: H(model, template, content)).
	docSrc string
}

// The document rule is the same for every model: a non-empty title (file
// path + qualified symbol name, per architecture.md#stage-embed) is prepended
// as its own line, followed by the text; an empty title contributes nothing.
// The model's document prefix, when it has one, wraps that.
var registry = map[string]modelSpec{
	// Cards: https://huggingface.co/Qwen/Qwen3-Embedding-0.6B
	// The canonical get_detailed_instruct is
	//   f'Instruct: {task}\nQuery:{query}'  (no space after "Query:").
	// Documents are unprefixed ("No need to add instruction for retrieval documents").
	"qwen3-embedding:0.6b": newSpec(1024, 4096,
		"Instruct: Given a search query, retrieve relevant code\nQuery:{{.Text}}",
		"{{if .Title}}{{.Title}}\n{{end}}{{.Text}}"),
	// Card: https://huggingface.co/nomic-ai/nomic-embed-text-v1.5
	// Query and document prefixes are "search_query: " and "search_document: ".
	// numCtx is 2048, not the card's 8192: Ollama serves the model with a
	// 2048-token context and rejects longer input even when asked for more
	// (measured in the S0 spike, recorded in D-003).
	"nomic-embed-text": newSpec(768, 2048,
		"search_query: {{.Text}}",
		"search_document: {{if .Title}}{{.Title}}\n{{end}}{{.Text}}"),
}

func newSpec(dim, numCtx int, query, doc string) modelSpec {
	parse := func(src string) *template.Template {
		return template.Must(template.New("").Option("missingkey=error").Parse(src))
	}
	return modelSpec{dim: dim, numCtx: numCtx, query: parse(query), doc: parse(doc), docSrc: doc}
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

// Dim returns the vector dimension of a known model, so callers that only
// build indexes need not construct an Ollama client.
func Dim(model string) (int, error) {
	spec, err := lookup(model)
	if err != nil {
		return 0, err
	}
	return spec.dim, nil
}

// MinNumCtx returns the smallest num_ctx across the model registry, the
// context every model can serve. The chunker derives its request budget from
// it (D-078).
func MinNumCtx() int {
	smallest := 0
	for _, spec := range registry {
		if smallest == 0 || spec.numCtx < smallest {
			smallest = spec.numCtx
		}
	}
	return smallest
}

// MaxDocPrefix returns the longest static document prefix in the registry: each
// model's document template rendered with an empty Document. The chunker
// subtracts its estimated token count from the request budget so a degraded
// header still fits every model (D-082).
func MaxDocPrefix() string {
	longest := ""
	for _, spec := range registry {
		s, err := render(spec.doc, Document{})
		if err != nil {
			continue
		}
		if len(s) > len(longest) {
			longest = s
		}
	}
	return longest
}

// DocPrefix returns the model's static document prefix: its document template
// rendered with no title and no text. It is what wraps every document for the
// model, and is folded into the input hash so a template change re-embeds.
func (o *Ollama) DocPrefix() string {
	s, _ := render(o.spec.doc, Document{})
	return s
}

// InputHash is the resumable identity of one embedded document (D-081): the
// sha256 of the model, the model's raw document template source and the given
// parts. A change to the model, the template or any part changes the hash, so a
// graph change re-embeds exactly the affected header rows. The template source
// is folded, not the rendered empty-title prefix, so a template edit that does
// not change the empty render still re-embeds (spec line 124:
// H(model, template, content)). Fields are length-prefixed so different part
// splits cannot collide.
func (o *Ollama) InputHash(parts ...string) string {
	h := sha256.New()
	writeField := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	writeField(o.model)
	writeField(o.spec.docSrc)
	for _, p := range parts {
		writeField(p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func lookup(model string) (modelSpec, error) {
	spec, ok := registry[model]
	if !ok {
		return modelSpec{}, fmt.Errorf("unknown embedding model %q; known: %s",
			model, strings.Join(KnownModels(), ", "))
	}
	return spec, nil
}

func (s modelSpec) renderQuery(q string) (string, error) {
	return render(s.query, Document{Text: q})
}

func (s modelSpec) renderDoc(d Document) (string, error) {
	return render(s.doc, d)
}

func render(t *template.Template, d Document) (string, error) {
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		return "", fmt.Errorf("render embedding prompt: %w", err)
	}
	return buf.String(), nil
}
