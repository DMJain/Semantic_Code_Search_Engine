package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/source"
)

// errorSample is one sampled error, with enough detail to classify its cause by
// reading the caller source (spec story 89). Cause is filled by the human
// classification pass; the sampler leaves it empty.
type errorSample struct {
	Type       string   `json:"type"` // "fp" or "fn"
	Kind       string   `json:"kind"`
	Caller     string   `json:"caller"`
	Callee     string   `json:"callee,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	CalleeRaw  string   `json:"callee_raw,omitempty"`
	CallLine   int      `json:"call_line"`
	File       string   `json:"file"`
	Source     string   `json:"source"`
	Cause      string   `json:"cause"`
}

// errorSampleDoc is the committed error-sample file.
type errorSampleDoc struct {
	Seed       int            `json:"seed"`
	Allocation string         `json:"allocation"`
	FPTotal    int            `json:"fp_total"`
	FNTotal    int            `json:"fn_total"`
	FPByKind   map[string]int `json:"fp_by_kind"`
	FNByKind   map[string]int `json:"fn_by_kind"`
	FPQuota    map[string]int `json:"fp_quota"`
	FNQuota    map[string]int `json:"fn_quota"`
	Samples    []errorSample  `json:"samples"`
}

// symDetail is what a sampled error needs about a symbol: its qualified name and
// the file and lines that let the sampler read the call site.
type symDetail struct {
	Qualified string
	File      string
	StartLine int
	EndLine   int
}

// sampleDeps is the rendering seam: symbol lookup and a source reader, so
// buildErrorSample can be tested without Postgres or the real tree.
type sampleDeps struct {
	Symbol func(id int64) symDetail
	Source func(file string, line int) string
}

// buildErrorSample enumerates the FP/FN errors, draws the seeded stratified
// sample and renders each item with its caller, callee, kind, line and source.
func buildErrorSample(in scoreInput, deps sampleDeps) (errorSampleDoc, error) {
	fps, fns, err := enumerateErrors(in)
	if err != nil {
		return errorSampleDoc{}, err
	}
	fpSel, fnSel, fpQuota, fnQuota := sampleErrors(fps, fns)
	doc := errorSampleDoc{
		Seed:       sampleSeed,
		Allocation: "proportional to kind counts, largest remainder, capped at stratum size",
		FPTotal:    len(fps),
		FNTotal:    len(fns),
		FPByKind:   countFPByKind(fps),
		FNByKind:   countFNByKind(fns),
		FPQuota:    fpQuota,
		FNQuota:    fnQuota,
	}
	for _, u := range fpSel {
		doc.Samples = append(doc.Samples, renderFP(u, deps))
	}
	for _, p := range fnSel {
		doc.Samples = append(doc.Samples, renderFN(p, deps))
	}
	return doc, nil
}

func renderFP(u fpUnit, deps sampleDeps) errorSample {
	d := deps.Symbol(u.CallerID)
	s := errorSample{
		Type: "fp", Kind: u.Kind, Caller: d.Qualified, CalleeRaw: u.Raw,
		CallLine: u.Line, File: d.File, Source: deps.Source(d.File, u.Line),
	}
	for _, c := range u.CandIDs {
		s.Candidates = append(s.Candidates, deps.Symbol(c).Qualified)
	}
	return s
}

func renderFN(p fnPair, deps sampleDeps) errorSample {
	d := deps.Symbol(p.CallerID)
	line := p.Line
	if line <= 0 {
		line = d.StartLine
	}
	return errorSample{
		Type: "fn", Kind: p.Kind, Caller: d.Qualified, Callee: deps.Symbol(p.CalleeID).Qualified,
		CallLine: p.Line, File: d.File, Source: deps.Source(d.File, line),
	}
}

func countFPByKind(fps []fpUnit) map[string]int {
	m := map[string]int{}
	for _, u := range fps {
		m[u.Kind]++
	}
	return m
}

func countFNByKind(fns []fnPair) map[string]int {
	m := map[string]int{}
	for _, p := range fns {
		m[p.Kind]++
	}
	return m
}

// sampleCallgraph is the sampler's DB path: it refuses while any file is stale
// (like the report) and writes the error-sample file.
func sampleCallgraph(ctx context.Context, pool *pgxpool.Pool, kubeDir, out string, stdout io.Writer) error {
	stale, err := staleFileCount(ctx, pool, kubeDir)
	if err != nil {
		return err
	}
	if stale > 0 {
		return fmt.Errorf("%d files are stale (see `indexer graph --status`); graph the tree before sampling", stale)
	}
	in, err := loadScoreInput(ctx, pool)
	if err != nil {
		return err
	}
	syms, err := loadSymbolDetails(ctx, pool)
	if err != nil {
		return err
	}
	deps := sampleDeps{
		Symbol: func(id int64) symDetail { return syms[id] },
		Source: func(file string, line int) string {
			if line <= 0 {
				return ""
			}
			s, err := source.ReadRange(kubeDir, file, line, line)
			if err != nil {
				return ""
			}
			return strings.TrimRight(s, "\n")
		},
	}
	doc, err := buildErrorSample(in, deps)
	if err != nil {
		return err
	}
	if err := writeErrorSample(out, doc); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "error sample: %d fp (of %d) + %d fn (of %d), seed %d -> %s\n",
		lenFP(doc), doc.FPTotal, lenFN(doc), doc.FNTotal, sampleSeed, out)
	return nil
}

func lenFP(doc errorSampleDoc) int {
	n := 0
	for _, s := range doc.Samples {
		if s.Type == "fp" {
			n++
		}
	}
	return n
}

func lenFN(doc errorSampleDoc) int {
	n := 0
	for _, s := range doc.Samples {
		if s.Type == "fn" {
			n++
		}
	}
	return n
}

// loadSymbolDetails reads every symbol's qualified name and source location.
func loadSymbolDetails(ctx context.Context, pool *pgxpool.Pool) (map[int64]symDetail, error) {
	rows, err := pool.Query(ctx, `
		SELECT s.id, s.qualified_name, f.path, s.start_line, s.end_line
		FROM symbols s JOIN files f ON f.id = s.file_id`)
	if err != nil {
		return nil, fmt.Errorf("load symbol details: %w", err)
	}
	defer rows.Close()
	out := map[int64]symDetail{}
	for rows.Next() {
		var id int64
		var d symDetail
		if err := rows.Scan(&id, &d.Qualified, &d.File, &d.StartLine, &d.EndLine); err != nil {
			return nil, fmt.Errorf("scan symbol detail: %w", err)
		}
		out[id] = d
	}
	return out, rows.Err()
}

// writeErrorSample writes the sample JSON, creating its directory.
func writeErrorSample(path string, doc errorSampleDoc) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
