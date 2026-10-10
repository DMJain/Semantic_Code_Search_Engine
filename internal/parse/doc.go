// Package parse extracts symbols from Go source in the Kubernetes checkout
// with tree-sitter.
//
// This package is deliberately pure: it reads bytes and returns symbols. The
// stage that reads files, assigns package-wide qualified names and writes the
// symbols to Postgres lives in cmd/indexer. Function, method, struct and
// interface symbols are produced today. Function and method symbols also carry
// their raw calls (S1-T06): the callee text of every call_expression in the
// body, walker-collected by [rawCalls] from the body node, deduplicated in
// source order and capped, with builtins and predeclared-type conversions
// dropped by [builtins]. Raw calls are summary-prompt input only and never feed
// the call graph (D-019).
package parse
