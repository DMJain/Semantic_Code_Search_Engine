// Package retrieve finds code relevant to a query. A Router picks which
// retrievers to run: lexical (Postgres FTS + trigram), structural (call
// graph walks) and semantic (pgvector). Their results are fused afterwards.
//
// Only the interfaces exist so far; no retriever is implemented.
package retrieve

import "context"

// Retriever returns ranked results for a query.
type Retriever interface {
	Retrieve(ctx context.Context, q Query) ([]Result, error)
}

// Router decides which retrievers should handle a raw query string.
type Router interface {
	Route(ctx context.Context, q string) (RouteDecision, error)
}

// Kind names a retrieval strategy.
type Kind string

// Retrieval strategies.
const (
	Lexical    Kind = "lexical"
	Structural Kind = "structural"
	Semantic   Kind = "semantic"
)

// Query is provisional.
type Query struct {
	Text string
	// Limit is the maximum number of results; 0 means retriever default.
	Limit int
}

// Result is provisional. TargetKind/TargetID mirror the embeddings table,
// e.g. ("chunk", 42) or ("summary", 7).
type Result struct {
	TargetKind string
	TargetID   int64
	Score      float64
	// Source is the retriever that produced this result.
	Source  Kind
	Snippet string
}

// RouteDecision is provisional.
type RouteDecision struct {
	Retrievers []Kind
	// Reason explains the choice, for traces.
	Reason string
}
