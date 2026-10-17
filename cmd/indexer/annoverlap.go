package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/cli"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/retrieve"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/seeded"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
)

// annOverlapK is the rank cut both sides of the overlap comparison use: the
// top-10 chunks (the gate) and, after the shared collapse helper, the top-10
// files (reported).
const annOverlapK = 10

// annFetch is the row count both queries fetch before the rank cut, so a file
// owning many near hits cannot starve the file-level comparison (D-084).
const annFetch = 200

// annDefaultEF is the HNSW search width used when --ef is not given.
const annDefaultEF = 200

// annMaxEF bounds an --ef value: a width above the HNSW ceiling is refused, not
// silently clamped.
const annMaxEF = 1000

// The probe set is seeded so the same corpus yields the same queries (D-027).
const (
	annProbeN    = 30
	annProbeSeed = 5
	annMinWords  = 12
)

// annGate is the chunk-level mean below which the run asks for a wider search.
const annGate = 0.95

// gateFailed reports whether a chunk-level mean misses the overlap gate.
func gateFailed(chunkMean float64) bool { return chunkMean < annGate }

// annChunkKinds is the chunk portion of the embeddings.target_kind enum this
// tool may read. The value is validated before it is inlined into SQL.
var annChunkKinds = map[string]bool{kindChunkRaw: true, kindChunkHeader: true}

// overlapPlan is one validated run: the model, kind and dimension to inline,
// the search widths to sweep and the resolved query source.
type overlapPlan struct {
	model   string
	kind    string
	dim     int
	ef      []int
	probe   bool
	queries []string
}

func runAnnOverlap(ctx context.Context, args []string) error {
	fs := newFlagSet("ann-overlap")
	model := fs.String("model", "", "embedding model (required)")
	kind := fs.String("kind", "", "chunk kind: chunk_raw or chunk_header (required)")
	queries := fs.String("queries", "probe", "query source: probe, or a file path with one query per line")
	ef := fs.String("ef", strconv.Itoa(annDefaultEF), "ef_search width, a value or a comma list (max 1000)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Validate every argument before opening the pool or the embedder, so an
	// invalid argument runs no SQL and makes no network call.
	plan, err := planOverlap(*model, *kind, *queries, *ef)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	emb, err := embed.NewOllama(cfg.OllamaURL, plan.model, 0)
	if err != nil {
		return err
	}
	return annOverlapStage(ctx, pool, emb, plan, os.Stdout)
}

// planOverlap validates the model against the registry, the kind against the
// chunk-kind enum and the --ef list against its range, then resolves the query
// source. Model, kind and dimension are inlined into SQL afterwards only from
// this validated plan.
func planOverlap(model, kind, queries, efCSV string) (overlapPlan, error) {
	if model == "" {
		return overlapPlan{}, fmt.Errorf("--model is required; known models: %s", strings.Join(embed.KnownModels(), ", "))
	}
	dim, err := embed.Dim(model)
	if err != nil {
		return overlapPlan{}, err
	}
	if kind == "" {
		return overlapPlan{}, fmt.Errorf("--kind is required; chunk kinds: %s", strings.Join(chunkKindNames(), ", "))
	}
	if !annChunkKinds[kind] {
		return overlapPlan{}, fmt.Errorf("unknown kind %q; chunk kinds: %s", kind, strings.Join(chunkKindNames(), ", "))
	}
	ef, err := parseEF(efCSV)
	if err != nil {
		return overlapPlan{}, err
	}
	plan := overlapPlan{model: model, kind: kind, dim: dim, ef: ef}
	if queries == "" || queries == "probe" {
		plan.probe = true
		return plan, nil
	}
	qs, err := readQueries(queries)
	if err != nil {
		return overlapPlan{}, err
	}
	plan.queries = qs
	return plan, nil
}

// chunkKindNames lists the accepted kinds in a stable order for messages.
func chunkKindNames() []string { return []string{kindChunkRaw, kindChunkHeader} }

// parseEF parses the --ef flag: an empty value is the default, otherwise one
// integer or a comma-separated list, each in 1..annMaxEF.
func parseEF(csv string) ([]int, error) {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return []int{annDefaultEF}, nil
	}
	var out []int
	for _, field := range strings.Split(csv, ",") {
		field = strings.TrimSpace(field)
		n, err := strconv.Atoi(field)
		if err != nil {
			return nil, fmt.Errorf("bad --ef value %q: want an integer", field)
		}
		if n <= 0 || n > annMaxEF {
			return nil, fmt.Errorf("--ef %d out of range: want 1..%d", n, annMaxEF)
		}
		out = append(out, n)
	}
	return out, nil
}

// readQueries reads one query per non-empty line from path.
func readQueries(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read --queries file: %w", err)
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--queries file %s has no queries", path)
	}
	return out, nil
}

// probeQueries builds the seeded probe set (D-027): the first sentence of the
// doc comment of every in-slice function or method whose first sentence has at
// least annMinWords words, drawn reproducibly to at most annProbeN queries. It
// is a smoke set and optimistic: every query sits near its own chunk.
func probeQueries(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT s.doc
		FROM symbols s
		JOIN files f ON f.id = s.file_id
		WHERE f.in_slice AND s.kind = ANY($1) AND s.doc IS NOT NULL AND s.doc <> ''
		ORDER BY s.id`, []string{parse.KindFunc, parse.KindMethod})
	if err != nil {
		return nil, fmt.Errorf("ann-overlap: select probe docs: %w", err)
	}
	defer rows.Close()

	var candidates []string
	for rows.Next() {
		var doc string
		if err := rows.Scan(&doc); err != nil {
			return nil, fmt.Errorf("ann-overlap: scan probe doc: %w", err)
		}
		if s := firstSentence(doc); len(strings.Fields(s)) >= annMinWords {
			candidates = append(candidates, s)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ann-overlap: read probe docs: %w", err)
	}
	return seeded.Draw(candidates, annProbeN, annProbeSeed), nil
}

// firstSentence returns the first sentence of a stored doc comment: the
// contiguous // lines joined into one paragraph with the comment markers
// removed, cut at the first sentence break (". ", "! " or "? "). A leading
// //go: directive is not part of the sentence and is dropped. A doc without a
// sentence break is returned whole.
func firstSentence(doc string) string {
	var b strings.Builder
	for _, line := range strings.Split(doc, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//go:") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "//"))
		if line == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(line)
	}
	text := strings.TrimSpace(b.String())
	end := len(text)
	for _, sep := range []string{". ", "! ", "? "} {
		if i := strings.Index(text, sep); i >= 0 && i+1 < end {
			end = i + 1
		}
	}
	return text[:end]
}

// annOverlapStage embeds every query once, then for each search width compares
// the HNSW top-10 chunks with the exact brute-force top-10 chunks under the
// same slice filter and 200-row fetch (the gate) and the file-level top-10
// after the shared collapse helper (reported). A single width gates on its own
// chunk-level mean; a sweep exits 3 only when no width clears annGate.
func annOverlapStage(ctx context.Context, pool *pgxpool.Pool, emb embed.Embedder, plan overlapPlan, out io.Writer) error {
	if emb.Dim() != plan.dim {
		return fmt.Errorf("ann-overlap: embedder dim %d, want %d for model %q", emb.Dim(), plan.dim, plan.model)
	}
	queries := plan.queries
	if plan.probe {
		q, err := probeQueries(ctx, pool)
		if err != nil {
			return err
		}
		if len(q) == 0 {
			return errors.New("ann-overlap: probe found no in-slice function doc with a 12-word first sentence")
		}
		queries = q
	}

	vecs, err := emb.EmbedQueries(ctx, queries)
	if err != nil {
		return fmt.Errorf("ann-overlap: embed queries: %w", err)
	}
	if len(vecs) != len(queries) {
		return fmt.Errorf("ann-overlap: embedded %d queries, want %d", len(vecs), len(queries))
	}
	for i, v := range vecs {
		if len(v) != plan.dim {
			return fmt.Errorf("ann-overlap: query %d vector length %d, want %d", i, len(v), plan.dim)
		}
	}

	sql := annHitSQL(plan.dim, plan.model, plan.kind)
	anyCleared := false
	for _, ef := range plan.ef {
		chunkSum, chunkMin := 0.0, 1.0
		fileSum, fileMin := 0.0, 1.0
		for _, v := range vecs {
			hnswHits, hnswIn, err := annHits(ctx, pool, sql, v, true, ef)
			if err != nil {
				return err
			}
			exactHits, exactIn, err := annHits(ctx, pool, sql, v, false, ef)
			if err != nil {
				return err
			}
			co := overlapAt(annChunkIDs(hnswHits), annChunkIDs(exactHits), annOverlapK)
			fo := overlapAt(annFiles(hnswHits, hnswIn), annFiles(exactHits, exactIn), annOverlapK)
			chunkSum += co
			fileSum += fo
			chunkMin = min(chunkMin, co)
			fileMin = min(fileMin, fo)
		}
		n := float64(len(vecs))
		chunkMean := chunkSum / n
		fmt.Fprintf(out, "ef=%d chunk_mean=%.2f chunk_min=%.2f file_mean=%.2f file_min=%.2f\n",
			ef, chunkMean, chunkMin, fileSum/n, fileMin)
		if !gateFailed(chunkMean) {
			anyCleared = true
		}
	}
	if !anyCleared {
		return cli.Exit(3, errors.New("tune ef_search"))
	}
	return nil
}

// annHitSQL is the shared ranking query: the same slice filter and 200-row
// fetch for both sides. On the HNSW side only hnsw.ef_search differs; on the
// exact side the transaction disables index scans. The model and kind literals
// come from the validated plan; the query vector is a parameter cast to the
// model's dimension.
func annHitSQL(dim int, model, kind string) string {
	return fmt.Sprintf(
		"SELECT c.id, fl.path, fl.in_slice, 1 - (e.vec::vector(%[1]d) <=> $1::vector(%[1]d)) AS score "+
			"FROM embeddings e "+
			"JOIN chunks c ON c.id = e.target_id "+
			"JOIN files fl ON fl.id = c.file_id "+
			"WHERE e.model = %[2]s AND e.target_kind = %[3]s AND fl.in_slice "+
			"ORDER BY e.vec::vector(%[1]d) <=> $1::vector(%[1]d) "+
			"LIMIT %[4]d",
		dim, quoteLiteral(model), quoteLiteral(kind), annFetch)
}

// annHits runs one ranking query in its own transaction: the HNSW side sets
// hnsw.ef_search to at least annFetch and disables sequential scans and sorting
// so the plan must walk the HNSW index (otherwise a small corpus makes the
// planner seq-scan and the comparison is exact-vs-exact); the exact side
// disables index scans so the same query is a brute-force sequential scan. It
// returns the hits in rank order plus each file's slice flag for the collapse
// helper.
func annHits(ctx context.Context, pool *pgxpool.Pool, sql string, vec []float32, hnsw bool, ef int) ([]retrieve.Hit, map[string]bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("ann-overlap: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if hnsw {
		if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL hnsw.ef_search = %d", max(ef, annFetch))); err != nil {
			return nil, nil, fmt.Errorf("ann-overlap: set hnsw.ef_search: %w", err)
		}
		for _, setting := range []string{"SET LOCAL enable_seqscan = off", "SET LOCAL enable_sort = off"} {
			if _, err := tx.Exec(ctx, setting); err != nil {
				return nil, nil, fmt.Errorf("ann-overlap: force hnsw index: %w", err)
			}
		}
	} else {
		if _, err := tx.Exec(ctx, "SET LOCAL enable_indexscan = off"); err != nil {
			return nil, nil, fmt.Errorf("ann-overlap: disable index scan: %w", err)
		}
	}

	rows, err := tx.Query(ctx, sql, pgvector.NewVector(vec))
	if err != nil {
		return nil, nil, fmt.Errorf("ann-overlap: query: %w", err)
	}
	hits := make([]retrieve.Hit, 0, annFetch)
	inSlice := make(map[string]bool)
	for rows.Next() {
		var h retrieve.Hit
		var slice bool
		if err := rows.Scan(&h.ChunkID, &h.File, &slice, &h.Score); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("ann-overlap: scan hit: %w", err)
		}
		h.Kind = ""
		inSlice[h.File] = slice
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, fmt.Errorf("ann-overlap: read hits: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("ann-overlap: commit: %w", err)
	}
	return hits, inSlice, nil
}

// annChunkIDs returns the chunk ids of the top annOverlapK hits, already in
// rank order.
func annChunkIDs(hits []retrieve.Hit) []int64 {
	top := min(annOverlapK, len(hits))
	out := make([]int64, top)
	for i := 0; i < top; i++ {
		out[i] = hits[i].ChunkID
	}
	return out
}

// annFiles collapses the fetched hits to files with the shared helper (slice
// filter, dedupe, top-10) and returns the ranked paths.
func annFiles(hits []retrieve.Hit, inSlice map[string]bool) []string {
	results := retrieve.CollapseToFiles(hits, retrieve.CollapseOpts{
		SliceOnly: true,
		InSlice:   func(path string) bool { return inSlice[path] },
		Limit:     annOverlapK,
	})
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.File
	}
	return out
}

// overlapAt is the overlap@k between two ranked result sets: the size of their
// intersection over k.
func overlapAt[T comparable](a, b []T, k int) float64 {
	set := make(map[T]struct{}, len(a))
	for _, x := range a {
		set[x] = struct{}{}
	}
	n := 0
	for _, x := range b {
		if _, ok := set[x]; ok {
			n++
		}
	}
	return float64(n) / float64(k)
}
