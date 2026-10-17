package retrieve

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/testdb"
)

// fakeEmbedder returns one fixed unit vector for every query and counts the
// calls, so a test can prove the query is embedded once.
type fakeEmbedder struct {
	model string
	dim   int
	vec   []float32
	calls int
}

func (f *fakeEmbedder) EmbedQueries(context.Context, []string) ([][]float32, error) {
	f.calls++
	return [][]float32{f.vec}, nil
}

func (f *fakeEmbedder) EmbedDocuments(context.Context, []embed.Document) ([][]float32, error) {
	return nil, nil
}

func (f *fakeEmbedder) Dim() int      { return f.dim }
func (f *fakeEmbedder) Model() string { return f.model }

// testDim is the registry dimension of qwen3-embedding:0.6b. Vectors are
// synthesized at this width so the embedder/model dimension cross-check in
// validate passes on the valid path.
const testDim = 1024

// unitVec returns a unit vector whose cosine similarity to [1,0,0,...] is s.
func unitVec(s float64) []float32 {
	v := make([]float32, testDim)
	v[0] = float32(s)
	v[1] = float32(math.Sqrt(1 - s*s))
	return v
}

// seedFile inserts one in- or out-of-slice file.
func seedFile(t *testing.T, pool *pgxpool.Pool, path string, inSlice bool) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO files (path, package, sha, loc, in_slice) VALUES ($1, 'pkg', 'sha', 1, $2) RETURNING id`,
		path, inSlice).Scan(&id); err != nil {
		t.Fatalf("insert file %s: %v", path, err)
	}
	return id
}

// seedChunk inserts a symbol, its single chunk and the chunk's embedding.
func seedChunk(t *testing.T, pool *pgxpool.Pool, fileID int64, name string, vec []float32, model, kind string) int64 {
	t.Helper()
	ctx := context.Background()
	var symID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO symbols (file_id, kind, name, qualified_name, start_line, end_line)
		 VALUES ($1, 'func', $2, $3, 1, 2) RETURNING id`,
		fileID, name, "q:"+name).Scan(&symID); err != nil {
		t.Fatalf("insert symbol %s: %v", name, err)
	}
	var chunkID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO chunks (symbol_id, file_id, part_idx, content, header, content_hash, token_count, start_line, end_line)
		 VALUES ($1, $2, 0, $3, '', 'h', 1, 1, 2) RETURNING id`,
		symID, fileID, "content "+name).Scan(&chunkID); err != nil {
		t.Fatalf("insert chunk %s: %v", name, err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO embeddings (target_kind, target_id, model, vec) VALUES ($1, $2, $3, $4)`,
		kind, chunkID, model, pgvector.NewVector(vec)); err != nil {
		t.Fatalf("insert embedding %s: %v", name, err)
	}
	return chunkID
}

// newFlat builds the retriever under test for one config with a fake embedder
// and a real pool, returning the fake so a test can read its call count.
func newFlat(t *testing.T, pool *pgxpool.Pool, kind string, source Kind) (Flat, *fakeEmbedder) {
	t.Helper()
	const model = "qwen3-embedding:0.6b"
	f := &fakeEmbedder{model: model, dim: testDim, vec: unitVec(1)}
	return Flat{Model: model, TargetKind: kind, Source: source, Pool: pool, Embedder: f}, f
}

// TestFlatRetrieveRanksFiles seeds fifteen chunks from two files and twenty
// more files, plus three out-of-slice files with the best scores, for both the
// A0 (chunk_raw, flat_raw) and A1 (chunk_header, flat_header) configs. The flat
// query returns ten distinct in-slice files with the two chunk-heavy files
// first, proving the slice filter runs before the cut, embeds the query once at
// a search width of at least ChunkFetch, and stamps the config's source.
func TestFlatRetrieveRanksFiles(t *testing.T) {
	const model = "qwen3-embedding:0.6b"
	ctx := context.Background()
	cases := []struct {
		name   string
		kind   string
		source Kind
	}{
		{"A0 chunk_raw", "chunk_raw", FlatRaw},
		{"A1 chunk_header", "chunk_header", FlatHeader},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testdb.New(t)

			if _, err := pool.Exec(ctx, fmt.Sprintf(
				"CREATE INDEX emb_hnsw_flat_test ON embeddings USING hnsw ((vec::vector(%d)) vector_cosine_ops) "+
					"WHERE model = %s AND target_kind = %s", testDim, quoteLiteral(model), quoteLiteral(tc.kind))); err != nil {
				t.Fatalf("create index: %v", err)
			}

			// Out-of-slice files with the best scores: the filter must drop them.
			for i, s := range []float64{0.999, 0.998, 0.997} {
				fid := seedFile(t, pool, fmt.Sprintf("outside_%d.go", i), false)
				seedChunk(t, pool, fid, fmt.Sprintf("outside_%d", i), unitVec(s), model, tc.kind)
			}
			// f1: 8 chunks, f2: 7 chunks, all ranked above the single-chunk files.
			f1 := seedFile(t, pool, "f1.go", true)
			for i := 0; i < 8; i++ {
				seedChunk(t, pool, f1, fmt.Sprintf("f1_%d", i), unitVec(0.99-float64(i)*0.005), model, tc.kind)
			}
			f2 := seedFile(t, pool, "f2.go", true)
			for i := 0; i < 7; i++ {
				seedChunk(t, pool, f2, fmt.Sprintf("f2_%d", i), unitVec(0.95-float64(i)*0.005), model, tc.kind)
			}
			for i := 0; i < 20; i++ {
				fid := seedFile(t, pool, fmt.Sprintf("other_%02d.go", i), true)
				seedChunk(t, pool, fid, fmt.Sprintf("other_%02d", i), unitVec(0.90-float64(i)*0.01), model, tc.kind)
			}

			flat, fake := newFlat(t, pool, tc.kind, tc.source)
			rs, err := flat.Retrieve(ctx, Query{Text: "anything"})
			if err != nil {
				t.Fatalf("Retrieve: %v", err)
			}
			if fake.calls != 1 {
				t.Errorf("EmbedQueries calls = %d, want 1", fake.calls)
			}
			if rs.EFSearch < ChunkFetch {
				t.Errorf("ef_search = %d, want at least ChunkFetch (%d)", rs.EFSearch, ChunkFetch)
			}
			if rs.Short {
				t.Errorf("Short = true, want false with more than ten in-slice files")
			}
			if len(rs.Results) != 10 {
				t.Fatalf("results = %d, want 10", len(rs.Results))
			}
			seen := map[string]bool{}
			for _, r := range rs.Results {
				if strings.HasPrefix(r.File, "outside_") {
					t.Errorf("result %s is out of slice", r.File)
				}
				if seen[r.File] {
					t.Errorf("duplicate file %s", r.File)
				}
				seen[r.File] = true
				if r.Source != tc.source {
					t.Errorf("source = %q, want %q", r.Source, tc.source)
				}
			}
			if rs.Results[0].File != "f1.go" || rs.Results[1].File != "f2.go" {
				t.Errorf("top results = %s, %s; want f1.go, f2.go", rs.Results[0].File, rs.Results[1].File)
			}
			if len(rs.Results[0].Evidence) != 8 {
				t.Errorf("f1 evidence = %d hits, want 8", len(rs.Results[0].Evidence))
			}
			if rs.Results[0].Score < 0.989 || rs.Results[0].Score > 0.991 {
				t.Errorf("f1 score = %v, want its best hit near 0.99", rs.Results[0].Score)
			}
		})
	}
}

// TestFlatRetrieveShortResult proves fewer than the requested distinct files
// sets Short, and that a limit inside the available count does not.
func TestFlatRetrieveShortResult(t *testing.T) {
	pool := testdb.New(t)
	const (
		model = "qwen3-embedding:0.6b"
		kind  = "chunk_raw"
	)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		fid := seedFile(t, pool, fmt.Sprintf("f%d.go", i), true)
		seedChunk(t, pool, fid, fmt.Sprintf("f%d", i), unitVec(0.9-float64(i)*0.1), model, kind)
	}

	flat, _ := newFlat(t, pool, "chunk_raw", FlatRaw)

	all, err := flat.Retrieve(ctx, Query{Text: "q", Limit: 10})
	if err != nil {
		t.Fatalf("Retrieve limit 10: %v", err)
	}
	if len(all.Results) != 3 || !all.Short {
		t.Errorf("limit 10: results=%d short=%v, want 3 and short", len(all.Results), all.Short)
	}

	capped, err := flat.Retrieve(ctx, Query{Text: "q", Limit: 2})
	if err != nil {
		t.Fatalf("Retrieve limit 2: %v", err)
	}
	if len(capped.Results) != 2 || capped.Short {
		t.Errorf("limit 2: results=%d short=%v, want 2 and not short", len(capped.Results), capped.Short)
	}
}

// TestFlatRefusesInvalidModelOrKind proves an unknown model or kind is refused
// before any SQL runs, so no invalid literal can reach the database.
func TestFlatRefusesInvalidModelOrKind(t *testing.T) {
	pool := testdb.New(t)
	fake := &fakeEmbedder{model: "qwen3-embedding:0.6b", dim: testDim, vec: unitVec(1)}

	bad := Flat{Model: "not-a-model", TargetKind: "chunk_raw", Source: FlatRaw, Pool: pool, Embedder: fake}
	if _, err := bad.Retrieve(context.Background(), Query{Text: "q"}); err == nil {
		t.Error("unknown model: got nil error, want refusal")
	}

	badKind := Flat{Model: "qwen3-embedding:0.6b", TargetKind: "not_a_kind", Source: FlatRaw, Pool: pool, Embedder: fake}
	if _, err := badKind.Retrieve(context.Background(), Query{Text: "q"}); err == nil {
		t.Error("unknown kind: got nil error, want refusal")
	}
}

// TestFlatRefusesDimModelMismatch proves an embedder whose dimension disagrees
// with the model's registry dimension is refused before any SQL runs: here a
// nomic-sized embedder is attached to a qwen3 model.
func TestFlatRefusesDimModelMismatch(t *testing.T) {
	pool := testdb.New(t)
	fake := &fakeEmbedder{model: "nomic-embed-text", dim: 768, vec: unitVec(1)}

	mismatch := Flat{Model: "qwen3-embedding:0.6b", TargetKind: "chunk_raw", Source: FlatRaw, Pool: pool, Embedder: fake}
	if _, err := mismatch.Retrieve(context.Background(), Query{Text: "q"}); err == nil {
		t.Error("dim/model mismatch: got nil error, want refusal")
	}
}
