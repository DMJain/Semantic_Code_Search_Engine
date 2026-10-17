// Command indexer builds the offline search index over a Kubernetes checkout.
//
// Usage:
//
//	indexer <command> [flags]
package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/cli"
)

var commands = []cli.Command{
	{Name: "migrate", Short: "apply database migrations", Run: runMigrate},
	{Name: "doctor", Short: "check Postgres, extensions, Ollama and models", Run: runDoctor},
	{Name: "fetch-repo", Short: "shallow-clone Kubernetes at KUBE_TAG into KUBE_DIR", Run: runFetchRepo},
	{Name: "walk", Short: "walk the corpus and fill the files table", Run: runWalk},
	{Name: "parse", Short: "parse Go files into function and method symbols", Run: runParse},
	{Name: "graph", Short: "resolve call expressions into tree-sitter edges", Run: runGraph},
	{Name: "audit", Short: "check a seeded sample of stored symbols against the source", Run: runAudit},
	{Name: "chunk", Short: "split function and method symbols into chunks", Run: runChunk},
	{Name: "summarize", Short: "summarize in-slice functions with a local LLM", Run: runSummarize},
	{Name: "embed", Short: "embed in-slice chunks as chunk_raw and chunk_header", Run: runEmbed},
	{Name: "vindex", Short: "build a per-model, per-kind partial HNSW vector index", Run: runVindex},
	{Name: "ann-overlap", Short: "gate HNSW search against exact brute force", Run: runAnnOverlap},
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	os.Exit(run(os.Args[1:]))
}

// run dispatches to a subcommand and returns the process exit code.
func run(args []string) int {
	return cli.App{Name: "indexer", Commands: commands}.Run(args)
}

// parseFlags parses a subcommand's flags with the shared flag set, so -h
// works consistently everywhere.
func parseFlags(name string, args []string) error {
	return newFlagSet(name).Parse(args)
}

// newFlagSet returns the flag set every subcommand uses, so help text and
// error handling stay the same everywhere.
func newFlagSet(name string) *flag.FlagSet {
	return cli.NewFlagSet("indexer", name)
}
