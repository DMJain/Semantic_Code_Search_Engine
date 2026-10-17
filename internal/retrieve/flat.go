package retrieve

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"
)

// ChunkFetch is how many chunks a flat query fetches before collapsing to
// files, so two files owning many hits cannot starve the file list (D-084).
const ChunkFetch = 100

// defaultLimit is how many ranked files a flat query returns unless the query
// asks for a different limit.
const defaultLimit = 10

// chunkKinds is the chunk portion of the embeddings.target_kind enum a flat
// query may read. The value is validated before it is inlined into SQL.
var chunkKinds = map[string]bool{
	"chunk_raw":    true,
	"chunk_header": true,
}

// Flat is the flat semantic retriever for the A0 (raw chunks) and A1 (header
// chunks) configs. It embeds the query once, fetches ChunkFetch chunks by
// cosine distance inside one transaction, and collapses them to ranked files.
type Flat struct {
	// Model is the embedding model stored alongside each vector; it must be
	// a known model.
	Model string
	// TargetKind is the embedding kind to read, chunk_raw or chunk_header.
	TargetKind string
	// Source is stamped on every result, flat_raw or flat_header.
	Source Kind
	Pool   *pgxpool.Pool
	// Embedder embeds the query text.
	Embedder embed.Embedder
	// EFSearch raises the HNSW search width above the ChunkFetch floor; 0
	// keeps the floor.
	EFSearch int
}

var _ Retriever = Flat{}

// Retrieve embeds the query once, sets the HNSW search width to at least
// ChunkFetch, fetches ChunkFetch chunks by score = 1 - (vec <=> q) with the
// model and kind literals validated and inlined, joins chunks and files, then
// collapses to files. Short is set when fewer than the requested number of
// distinct files remain; the query is never refetched.
func (f Flat) Retrieve(ctx context.Context, q Query) (ResultSet, error) {
	if err := f.validate(); err != nil {
		return ResultSet{}, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	dim := f.Embedder.Dim()

	vecs, err := f.Embedder.EmbedQueries(ctx, []string{q.Text})
	if err != nil {
		return ResultSet{}, fmt.Errorf("flat: embed query: %w", err)
	}
	if len(vecs) != 1 {
		return ResultSet{}, fmt.Errorf("flat: embed query: got %d vectors, want 1", len(vecs))
	}
	if len(vecs[0]) != dim {
		return ResultSet{}, fmt.Errorf("flat: embed query: vector length %d, want %d", len(vecs[0]), dim)
	}

	ef := f.EFSearch
	if ef < ChunkFetch {
		ef = ChunkFetch
	}

	tx, err := f.Pool.Begin(ctx)
	if err != nil {
		return ResultSet{}, fmt.Errorf("flat: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL hnsw.ef_search = %d", ef)); err != nil {
		return ResultSet{}, fmt.Errorf("flat: set hnsw.ef_search: %w", err)
	}
	if err := tx.QueryRow(ctx, "SELECT current_setting('hnsw.ef_search')::int").Scan(&ef); err != nil {
		return ResultSet{}, fmt.Errorf("flat: read hnsw.ef_search: %w", err)
	}

	// The model and kind literals are validated above and then inlined: a
	// generic prepared plan cannot match the partial-index predicate. The
	// query vector is a parameter cast to the embedder's dimension.
	sql := fmt.Sprintf(
		"SELECT c.id, fl.path, c.start_line, c.end_line, "+
			"1 - (e.vec::vector(%[1]d) <=> $1::vector(%[1]d)) AS score, fl.in_slice "+
			"FROM embeddings e "+
			"JOIN chunks c ON c.id = e.target_id "+
			"JOIN files fl ON fl.id = c.file_id "+
			"WHERE e.model = %[2]s AND e.target_kind = %[3]s "+
			"ORDER BY e.vec::vector(%[1]d) <=> $1::vector(%[1]d) "+
			"LIMIT %[4]d",
		dim, quoteLiteral(f.Model), quoteLiteral(f.TargetKind), ChunkFetch)

	rows, err := tx.Query(ctx, sql, pgvector.NewVector(vecs[0]))
	if err != nil {
		return ResultSet{}, fmt.Errorf("flat: query: %w", err)
	}
	hits := make([]Hit, 0, ChunkFetch)
	inSlice := make(map[string]bool)
	for rows.Next() {
		var h Hit
		var slice bool
		if err := rows.Scan(&h.ChunkID, &h.File, &h.StartLine, &h.EndLine, &h.Score, &slice); err != nil {
			rows.Close()
			return ResultSet{}, fmt.Errorf("flat: scan hit: %w", err)
		}
		h.Kind = f.TargetKind
		inSlice[h.File] = slice
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return ResultSet{}, fmt.Errorf("flat: read hits: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return ResultSet{}, fmt.Errorf("flat: commit: %w", err)
	}

	results := CollapseToFiles(hits, CollapseOpts{
		SliceOnly: true,
		InSlice:   func(path string) bool { return inSlice[path] },
		Limit:     limit,
	})
	for i := range results {
		results[i].Source = f.Source
	}
	return ResultSet{Results: results, Short: len(results) < limit, EFSearch: ef}, nil
}

// validate refuses an incomplete retriever, a model or kind outside the
// registries, and an embedder whose dimension disagrees with the model's
// registry dimension, before any SQL runs.
func (f Flat) validate() error {
	if f.Pool == nil {
		return errors.New("flat: nil pool")
	}
	if f.Embedder == nil {
		return errors.New("flat: nil embedder")
	}
	dim, err := embed.Dim(f.Model)
	if err != nil {
		return err
	}
	if f.Embedder.Dim() != dim {
		return fmt.Errorf("flat: embedder dim %d, want %d for model %q", f.Embedder.Dim(), dim, f.Model)
	}
	if !chunkKinds[f.TargetKind] {
		return fmt.Errorf("flat: unknown chunk kind %q; want chunk_raw or chunk_header", f.TargetKind)
	}
	return nil
}

// quoteLiteral wraps s in single quotes, doubling any single quote, so a
// validated value can be inlined without changing its meaning.
func quoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
