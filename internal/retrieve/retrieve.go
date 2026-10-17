// Package retrieve finds code relevant to a query. A Router picks which
// retrievers to run: lexical (Postgres FTS + trigram), structural (call
// graph walks) and semantic (pgvector). Their results are fused afterwards.
//
// Every Retriever returns a ResultSet: files ranked by score, each keeping the
// underlying chunk hits as evidence. The flat semantic retriever is the first
// implementation.
package retrieve

import (
	"context"
	"sort"
)

// Retriever returns ranked results for a query.
type Retriever interface {
	Retrieve(ctx context.Context, q Query) (ResultSet, error)
}

// Router decides which retrievers should handle a raw query string.
type Router interface {
	Route(ctx context.Context, q string) (RouteDecision, error)
}

// Kind names a retrieval strategy, or the source of a flat result.
type Kind string

// Retrieval strategies and flat-retriever sources.
const (
	Lexical    Kind = "lexical"
	Structural Kind = "structural"
	Semantic   Kind = "semantic"
	// FlatRaw is A0: raw chunks without a context header.
	FlatRaw Kind = "flat_raw"
	// FlatHeader is A1: chunks with a context header.
	FlatHeader Kind = "flat_header"
)

// Query is the request handed to a retriever.
type Query struct {
	Text string
	// Limit is the maximum number of results; 0 means retriever default.
	Limit int
}

// Hit is one underlying chunk match kept as evidence for a file.
type Hit struct {
	Kind      string
	ChunkID   int64
	File      string
	StartLine int
	EndLine   int
	Score     float64
}

// Evidence is a Hit used in a Result; the two names refer to the same shape.
type Evidence = Hit

// Result is one ranked file: its best hit's score and every hit for it.
type Result struct {
	File   string
	Score  float64
	Source Kind
	// Evidence is every fetched hit for the file, ordered by score
	// descending then chunk id ascending.
	Evidence []Evidence
}

// ResultSet is one retriever run. Short reports that fewer than the requested
// number of distinct files were found (short_result), so a caller reports the
// count instead of refetching. EFSearch records the HNSW search width a
// semantic retriever used, for the run record.
type ResultSet struct {
	Results  []Result
	Short    bool
	EFSearch int
}

// RouteDecision is provisional.
type RouteDecision struct {
	Retrievers []Kind
	// Reason explains the choice, for traces.
	Reason string
}

// CollapseOpts controls CollapseToFiles.
type CollapseOpts struct {
	// SliceOnly drops hits whose file is outside the slice when true.
	SliceOnly bool
	// InSlice reports whether a path is in the slice; read when SliceOnly.
	InSlice func(path string) bool
	// Limit caps the number of results; 0 means no cap.
	Limit int
}

// CollapseToFiles turns per-chunk hits into ranked files. In order (D-039): it
// filters to the slice, groups the rest by file keeping every hit as evidence
// ordered by score descending then chunk id ascending, scores each file by its
// best hit, sorts files by score descending then path ascending, and cuts to
// the limit. The slice filter runs before the grouping and the cut, so
// out-of-slice hits can never push in-slice files off the list. Source is left
// for the caller to stamp.
//
// The slice filter fails closed: with SliceOnly set and InSlice nil every hit
// is treated as out of slice and dropped, rather than silently skipping the
// filter and returning out-of-slice files.
func CollapseToFiles(hits []Hit, opts CollapseOpts) []Result {
	byFile := make(map[string][]Hit)
	for _, h := range hits {
		if opts.SliceOnly && (opts.InSlice == nil || !opts.InSlice(h.File)) {
			continue
		}
		byFile[h.File] = append(byFile[h.File], h)
	}
	results := make([]Result, 0, len(byFile))
	for file, ev := range byFile {
		sort.SliceStable(ev, func(i, j int) bool {
			if ev[i].Score != ev[j].Score {
				return ev[i].Score > ev[j].Score
			}
			return ev[i].ChunkID < ev[j].ChunkID
		})
		results = append(results, Result{File: file, Score: ev[0].Score, Evidence: ev})
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].File < results[j].File
	})
	if opts.Limit > 0 && len(results) > opts.Limit {
		results = results[:opts.Limit]
	}
	return results
}
