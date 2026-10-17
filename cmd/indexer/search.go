package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/cli"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/retrieve"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
)

// searchConfigs maps a --config value to the chunk kind it reads and the source
// it stamps on every result. A0 is raw chunks, A1 is chunks with the context
// header.
var searchConfigs = map[string]struct {
	kind   string
	source retrieve.Kind
}{
	"A0": {kind: kindChunkRaw, source: retrieve.FlatRaw},
	"A1": {kind: kindChunkHeader, source: retrieve.FlatHeader},
}

// searchOptions configures one search run.
type searchOptions struct {
	Query string
	Kind  string
	Limit int
}

// inputHasher is the embedder part the coverage guard needs: the model name and
// the input-hash rule, reused so the guard recomputes the exact hash the embed
// stage stored (D-081) instead of duplicating it. *embed.Ollama implements it.
type inputHasher interface {
	Model() string
	InputHash(parts ...string) string
}

func runSearch(ctx context.Context, args []string) error {
	fs := newFlagSet("search")
	cfgName := fs.String("config", "", "config: A0 (raw chunks) or A1 (header chunks)")
	limit := fs.Int("limit", 10, "maximum number of ranked files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	spec, ok := searchConfigs[*cfgName]
	if !ok {
		return fmt.Errorf("unknown --config %q; want A0 or A1", *cfgName)
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("want exactly one query argument, got %d", fs.NArg())
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	client, err := embed.NewOllama(cfg.OllamaURL, cfg.OllamaEmbedModel, 0)
	if err != nil {
		return err
	}
	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	flat := retrieve.Flat{
		Model:      cfg.OllamaEmbedModel,
		TargetKind: spec.kind,
		Source:     spec.source,
		Pool:       pool,
		Embedder:   client,
	}
	return searchStage(ctx, pool, client, flat, searchOptions{
		Query: fs.Arg(0),
		Kind:  spec.kind,
		Limit: *limit,
	}, os.Stdout)
}

// searchStage guards that the config's vectors cover the in-slice corpus, then
// runs the retriever and prints one tab-separated line per ranked file. A
// shortfall is exit 2 with "have N, want M" on stderr; exit 3 is reserved for
// the overlap gate and is never used here.
func searchStage(ctx context.Context, pool *pgxpool.Pool, hasher inputHasher, r retrieve.Retriever, opts searchOptions, out io.Writer) error {
	have, want, err := searchCoverage(ctx, pool, hasher, opts.Kind)
	if err != nil {
		return err
	}
	if have < want {
		return cli.Exit(2, fmt.Errorf("have %d, want %d", have, want))
	}
	rs, err := r.Retrieve(ctx, retrieve.Query{Text: opts.Query, Limit: opts.Limit})
	if err != nil {
		return err
	}
	return printSearchResults(ctx, out, rs, func(ctx context.Context, chunkID int64) (string, error) {
		return chunkSignature(ctx, pool, chunkID)
	})
}

// searchCoverage returns the stored vector count for the config's (model, kind)
// and the number of in-slice chunks that should have one: every in-slice chunk
// less the current-hash failures, that is chunks whose failure row still
// matches the chunk's current input hash for the kind (D-081). It reuses the
// embed stage's candidate query, its per-kind hash parts and the embedder's
// input-hash rule, so the guard and the embed stage cannot disagree.
func searchCoverage(ctx context.Context, pool *pgxpool.Pool, hasher inputHasher, kind string) (have, want int, err error) {
	model := hasher.Model()
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM embeddings WHERE model = $1 AND target_kind = $2`,
		model, kind).Scan(&have); err != nil {
		return 0, 0, fmt.Errorf("count %s vectors: %w", kind, err)
	}
	candidates, err := loadCandidates(ctx, pool, model, kind)
	if err != nil {
		return 0, 0, err
	}
	for _, c := range candidates {
		if c.failHash != "" && c.failHash == hasher.InputHash(c.parts(kind)...) {
			continue
		}
		want++
	}
	return have, want, nil
}

// signatureLookup returns the signature of the symbol owning a chunk.
type signatureLookup func(ctx context.Context, chunkID int64) (string, error)

// printSearchResults prints one tab-separated line per ranked file: rank from
// one, the file score to four decimals, the file path, the best evidence
// chunk's start-end span and the owning symbol's signature.
func printSearchResults(ctx context.Context, out io.Writer, rs retrieve.ResultSet, signature signatureLookup) error {
	for i, res := range rs.Results {
		if len(res.Evidence) == 0 {
			return fmt.Errorf("result %d (%s) has no evidence chunk", i+1, res.File)
		}
		best := res.Evidence[0]
		sig, err := signature(ctx, best.ChunkID)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%d\t%.4f\t%s\t%d-%d\t%s\n",
			i+1, res.Score, res.File, best.StartLine, best.EndLine, sig)
	}
	return nil
}

// chunkSignature returns the signature of the symbol owning a chunk. A grouped
// chunk's owner is the type, so its signature is the first declaration's.
func chunkSignature(ctx context.Context, pool *pgxpool.Pool, chunkID int64) (string, error) {
	var signature string
	if err := pool.QueryRow(ctx,
		`SELECT coalesce(s.signature, '') FROM chunks c JOIN symbols s ON s.id = c.symbol_id WHERE c.id = $1`,
		chunkID).Scan(&signature); err != nil {
		return "", fmt.Errorf("read signature of chunk %d: %w", chunkID, err)
	}
	return signature, nil
}
