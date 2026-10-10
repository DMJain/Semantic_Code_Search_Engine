// Package parse extracts symbols from Go source in the Kubernetes checkout
// with tree-sitter.
//
// This package is deliberately pure: it reads bytes and returns symbols. The
// stage that reads files, assigns package-wide qualified names and writes the
// symbols to Postgres lives in cmd/indexer. Only function and method symbols
// are produced today; struct and interface extraction (S1-T05) and raw call
// extraction (S1-T06) plug in at the same node switch in [File].
package parse
