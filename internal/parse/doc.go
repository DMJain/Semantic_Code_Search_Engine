// Package parse extracts symbols from Go source in the Kubernetes checkout
// with tree-sitter.
//
// This package is deliberately pure: it reads bytes and returns symbols. The
// stage that reads files, assigns package-wide qualified names and writes the
// symbols to Postgres lives in cmd/indexer. Function, method, struct and
// interface symbols are produced today; raw call extraction (S1-T06) plugs in
// at the same node switch in [File].
package parse
