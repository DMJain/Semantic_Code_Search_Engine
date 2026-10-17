package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/chunk"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
)

// embedBatchSize is the most documents sent in one request and committed in one
// transaction (D-081).
const embedBatchSize = 32

// The embeddings.target_kind values this stage writes (architecture "Data
// model"). Summary kinds are not embedded here.
const (
	kindChunkRaw    = "chunk_raw"
	kindChunkHeader = "chunk_header"
)

// defaultEmbedKinds is the kind set `indexer embed` processes by default.
var defaultEmbedKinds = []string{kindChunkRaw, kindChunkHeader}

// validTargetKinds is the embeddings.target_kind enum from architecture "Data
// model", so --kinds is validated against the schema vocabulary.
var validTargetKinds = map[string]bool{
	kindChunkRaw: true, kindChunkHeader: true,
	"summary_function": true, "summary_file": true, "summary_package": true,
}

// embedClient is the part of the embedder the stage uses: the Embedder
// interface plus the pre-run probe and the input hash (D-081). *embed.Ollama
// implements it.
type embedClient interface {
	embed.Embedder
	Probe(ctx context.Context) (embed.ProbeResult, error)
	InputHash(parts ...string) string
}

func runEmbed(ctx context.Context, args []string) error {
	fs := newFlagSet("embed")
	model := fs.String("model", "", "embedding model (default OLLAMA_EMBED_MODEL)")
	kinds := fs.String("kinds", strings.Join(defaultEmbedKinds, ","), "comma-separated target kinds")
	limit := fs.Int("limit", 0, "cap the selection to the lowest N chunks per kind (0 = all)")
	retryFailed := fs.Bool("retry-failed", false, "re-attempt items whose stored failure input_hash still matches")
	if err := fs.Parse(args); err != nil {
		return err
	}
	kindList, err := parseEmbedKinds(*kinds)
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	m := *model
	if m == "" {
		m = cfg.OllamaEmbedModel
	}
	// NewOllama errors on a model without prompt templates: an unknown model is
	// a startup error (D-079, D-081).
	client, err := embed.NewOllama(cfg.OllamaURL, m, 0)
	if err != nil {
		return err
	}
	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	return embedStage(ctx, pool, client, embedOptions{
		Kinds:       kindList,
		Limit:       *limit,
		RetryFailed: *retryFailed,
	}, os.Stdout)
}

// parseEmbedKinds validates a comma-separated kind list against the target_kind
// enum and keeps only the chunk kinds this stage embeds.
func parseEmbedKinds(csv string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, k := range strings.Split(csv, ",") {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			continue
		}
		if !validTargetKinds[k] {
			return nil, fmt.Errorf("unknown kind %q; chunk kinds: %s", k, strings.Join(defaultEmbedKinds, ", "))
		}
		if k != kindChunkRaw && k != kindChunkHeader {
			return nil, fmt.Errorf("kind %q is not embedded yet; only %s", k, strings.Join(defaultEmbedKinds, ", "))
		}
		seen[k] = true
		out = append(out, k)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no kinds given; want one of %s", strings.Join(defaultEmbedKinds, ", "))
	}
	return out, nil
}

// embedOptions configures one embed run.
type embedOptions struct {
	// Kinds are the target kinds to embed, each processed in turn.
	Kinds []string
	// Limit caps the ordered selection per kind before skipping (0 = no
	// limit), so a rerun re-picks the same chunks and skips those done.
	Limit int
	// RetryFailed re-attempts items whose stored failure input_hash still
	// matches the current one.
	RetryFailed bool
}

// kindCounts is one kind's contribution to the run summary.
type kindCounts struct {
	embedded  int
	skipped   int
	tooLarge  int
	truncated int
}

// embedStage embeds in-slice chunks in the requested kinds, idempotently and
// resumably (D-079, D-081, D-082). It probes first, deletes orphan vectors of
// the two chunk kinds in one transaction, then works each kind in chunk-id order
// in batches of 32, one transaction per batch. A failure is skipped only while
// its stored input_hash matches; failures above 1% of in-slice chunks stop the
// run and are raised. DB and embed work use a context a signal does not cancel,
// so the in-flight batch always commits; ctx is checked only between kinds, so
// a signal stops the run cleanly with the partial summary and returns nil
// (architecture "SIGINT/SIGTERM finishes the in-flight item, commits, exits 0").
func embedStage(ctx context.Context, pool *pgxpool.Pool, client embedClient, opts embedOptions, out io.Writer) error {
	dbCtx := context.WithoutCancel(ctx)

	// The probe must pass before any vector is written (D-079, D-081).
	if _, err := client.Probe(ctx); err != nil {
		return fmt.Errorf("embed: probe: %w", err)
	}

	// Start with one transaction that removes orphan chunk vectors: rows whose
	// chunk no longer exists. Only the two chunk kinds are touched, and only
	// this stage ever deletes (D-081).
	orphans, err := deleteOrphanChunkVectors(dbCtx, pool)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "orphans_deleted: %d\n", orphans)

	inSlice, err := countChunks(dbCtx, pool,
		`SELECT count(*) FROM chunks c JOIN files f ON f.id = c.file_id WHERE f.in_slice`)
	if err != nil {
		return err
	}

	var total kindCounts
	for _, kind := range opts.Kinds {
		// A signal stops the run cleanly between kinds: the in-flight work
		// already committed and the partial summary is printed below.
		if ctx.Err() != nil {
			break
		}
		counts, err := embedKind(ctx, pool, client, kind, opts.Limit, opts.RetryFailed)
		if err != nil {
			return err
		}
		printKindCounts(out, kind, counts)
		total.embedded += counts.embedded
		total.skipped += counts.skipped
		total.tooLarge += counts.tooLarge
		total.truncated += counts.truncated

		// Failures above 1% of in-slice chunks stop the run before the next
		// kind starts (D-082). The threshold is the in-slice chunk count, not
		// the total, and the check is per kind so an identical failure in both
		// arms is counted once. Failure sets are content-based, so stopping
		// early does not change which chunks fail.
		if fails := counts.tooLarge + counts.truncated; inSlice > 0 && fails*100 > inSlice {
			printTotalCounts(out, total)
			return fmt.Errorf("embed: %s: %d failures exceed 1%% of %d in-slice chunks; stopping",
				kind, fails, inSlice)
		}
	}
	printTotalCounts(out, total)
	return nil
}

// embedCandidate is one in-slice chunk and the stored hashes that decide whether
// it is already embedded or already failed.
type embedCandidate struct {
	id       int64
	content  string
	header   string
	tokens   int
	embHash  string
	failHash string
}

// parts returns the input-hash parts for a kind: content for chunk_raw, header
// and content for chunk_header (D-081, D-079).
func (c embedCandidate) parts(kind string) []string {
	if kind == kindChunkHeader {
		return []string{c.header, c.content}
	}
	return []string{c.content}
}

// docText returns the document body for a kind. Both kinds carry no title
// (D-079): chunk_raw is the content, chunk_header is header plus content.
func (c embedCandidate) docText(kind string) string {
	if kind == kindChunkHeader {
		return c.header + "\n" + c.content
	}
	return c.content
}

// embedItem is one chunk queued for embedding, with its current input hash.
type embedItem struct {
	id     int64
	text   string
	hash   string
	tokens int
}

// embedKind embeds one kind's in-slice work list in batches of 32. It returns
// the run counts and a hard error for a wrong-dimension reply (never stored).
// DB and embed work run under a context a signal does not cancel, so the
// in-flight batch always commits; ctx is checked only between batches, so a
// signal stops the kind cleanly after the batch that already committed.
func embedKind(ctx context.Context, pool *pgxpool.Pool, client embedClient, kind string, limit int, retryFailed bool) (kindCounts, error) {
	var counts kindCounts
	dbCtx := context.WithoutCancel(ctx)
	candidates, err := loadCandidates(dbCtx, pool, client.Model(), kind)
	if err != nil {
		return counts, err
	}

	// The limit caps the selection by chunk id before skipping, so rerunning
	// the same command re-picks the same chunks and skips the ones already
	// done (the summarize stage uses the same rule). loadCandidates orders by
	// chunk id.
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}

	work := make([]embedItem, 0, len(candidates))
	for _, c := range candidates {
		hash := client.InputHash(c.parts(kind)...)
		if c.embHash == hash {
			counts.skipped++
			continue
		}
		if !retryFailed && c.failHash == hash {
			counts.skipped++
			continue
		}
		work = append(work, embedItem{id: c.id, text: c.docText(kind), hash: hash, tokens: c.tokens})
	}

	// Pre-send guard: an item whose content is over the embeddable ceiling is
	// never sent and is recorded too_large. The check uses content tokens, so
	// chunk_raw and chunk_header fail on exactly the same chunks (D-082).
	sendable := make([]embedItem, 0, len(work))
	var tooLarge []embedItem
	for _, it := range work {
		if it.tokens > chunk.EmbeddableContentMax {
			tooLarge = append(tooLarge, it)
			counts.tooLarge++
			continue
		}
		sendable = append(sendable, it)
	}
	if len(tooLarge) > 0 {
		if err := writeFailures(dbCtx, pool, kind, client.Model(), tooLarge, "too_large"); err != nil {
			return counts, err
		}
	}

	for start := 0; start < len(sendable); start += embedBatchSize {
		if ctx.Err() != nil {
			break
		}
		end := min(start+embedBatchSize, len(sendable))
		embedded, truncated, err := sendBatch(dbCtx, pool, client, kind, sendable[start:end])
		if err != nil {
			return counts, err
		}
		counts.embedded += embedded
		counts.truncated += truncated
	}
	return counts, nil
}

// sendBatch embeds one batch of at most 32 documents in one transaction. On a
// batch error it re-sends each item individually: an input-too-long error is a
// truncation failure; any other per-item error (a wrong dimension, a transport
// or server error) is a hard error and nothing from that item is stored.
func sendBatch(ctx context.Context, pool *pgxpool.Pool, client embedClient, kind string, batch []embedItem) (embedded, truncated int, err error) {
	docs := make([]embed.Document, len(batch))
	for i, it := range batch {
		docs[i] = embed.Document{Text: it.text}
	}
	vecs, err := client.EmbedDocuments(ctx, docs)
	if err == nil {
		succ := make([]embeddingRow, len(batch))
		for i, it := range batch {
			succ[i] = embeddingRow{item: it, vec: vecs[i]}
		}
		if err := writeBatch(ctx, pool, kind, client.Model(), succ, nil); err != nil {
			return 0, 0, err
		}
		return len(succ), 0, nil
	}

	var succ []embeddingRow
	var trunc []embedItem
	for _, it := range batch {
		v, verr := client.EmbedDocuments(ctx, []embed.Document{{Text: it.text}})
		if verr != nil {
			if !errors.Is(verr, embed.ErrInputTooLong) {
				return 0, 0, fmt.Errorf("embed %s chunk %d: %w", kind, it.id, verr)
			}
			trunc = append(trunc, it)
			continue
		}
		succ = append(succ, embeddingRow{item: it, vec: v[0]})
	}
	if err := writeBatch(ctx, pool, kind, client.Model(), succ, trunc); err != nil {
		return 0, 0, err
	}
	return len(succ), len(trunc), nil
}

// embeddingRow is one vector to write.
type embeddingRow struct {
	item embedItem
	vec  []float32
}

// loadCandidates returns every in-slice chunk ordered by id, with the stored
// embedding and failure input hashes for (kind, model).
func loadCandidates(ctx context.Context, pool *pgxpool.Pool, model, kind string) ([]embedCandidate, error) {
	rows, err := pool.Query(ctx, `
		SELECT c.id, c.content, c.header, c.token_count,
		       coalesce(e.input_hash, ''), coalesce(ef.input_hash, '')
		FROM chunks c
		JOIN files f ON f.id = c.file_id
		LEFT JOIN embeddings e
		       ON e.target_kind = $1 AND e.target_id = c.id AND e.model = $2
		LEFT JOIN embed_failures ef
		       ON ef.target_kind = $1 AND ef.target_id = c.id AND ef.model = $2
		WHERE f.in_slice
		ORDER BY c.id`, kind, model)
	if err != nil {
		return nil, fmt.Errorf("list %s candidates: %w", kind, err)
	}
	defer rows.Close()

	var out []embedCandidate
	for rows.Next() {
		var c embedCandidate
		if err := rows.Scan(&c.id, &c.content, &c.header, &c.tokens, &c.embHash, &c.failHash); err != nil {
			return nil, fmt.Errorf("scan %s candidate: %w", kind, err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// writeBatch writes one batch's vectors and truncation failures in a single
// transaction: an upsert on (target_kind, target_id, model) for each vector, a
// failure-row delete for each success, and an upsert for each failure.
func writeBatch(ctx context.Context, pool *pgxpool.Pool, kind, model string, succ []embeddingRow, failures []embedItem) error {
	if len(succ) == 0 && len(failures) == 0 {
		return nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s batch: %w", kind, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, s := range succ {
		if _, err := tx.Exec(ctx, `
			INSERT INTO embeddings (target_kind, target_id, model, vec, input_hash)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (target_kind, target_id, model) DO UPDATE
			  SET vec = EXCLUDED.vec, input_hash = EXCLUDED.input_hash`,
			kind, s.item.id, model, pgvector.NewVector(s.vec), s.item.hash); err != nil {
			return fmt.Errorf("upsert %s embedding for chunk %d: %w", kind, s.item.id, err)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM embed_failures WHERE target_kind = $1 AND target_id = $2 AND model = $3`,
			kind, s.item.id, model); err != nil {
			return fmt.Errorf("clear %s failure for chunk %d: %w", kind, s.item.id, err)
		}
	}
	if err := upsertFailures(ctx, tx, kind, model, failures, "truncated"); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s batch: %w", kind, err)
	}
	return nil
}

// writeFailures records pre-send failures (too_large) in one transaction.
func writeFailures(ctx context.Context, pool *pgxpool.Pool, kind, model string, items []embedItem, reason string) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s failures: %w", kind, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := upsertFailures(ctx, tx, kind, model, items, reason); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s failures: %w", kind, err)
	}
	return nil
}

// upsertFailures writes one failure row per item, keyed on
// (target_kind, target_id, model), so re-recording a changed chunk replaces the
// stale row.
func upsertFailures(ctx context.Context, tx pgx.Tx, kind, model string, items []embedItem, reason string) error {
	for _, it := range items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO embed_failures (target_kind, target_id, model, reason, input_hash)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (target_kind, target_id, model) DO UPDATE
			  SET reason = EXCLUDED.reason, input_hash = EXCLUDED.input_hash, created_at = now()`,
			kind, it.id, model, reason, it.hash); err != nil {
			return fmt.Errorf("record %s failure for chunk %d: %w", kind, it.id, err)
		}
	}
	return nil
}

// deleteOrphanChunkVectors deletes chunk-kind vectors with no chunk row and
// returns the count. Only the two chunk kinds are touched (D-081).
func deleteOrphanChunkVectors(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	tag, err := pool.Exec(ctx, `
		DELETE FROM embeddings
		WHERE target_kind IN ($1, $2)
		  AND NOT EXISTS (SELECT 1 FROM chunks c WHERE c.id = embeddings.target_id)`,
		kindChunkRaw, kindChunkHeader)
	if err != nil {
		return 0, fmt.Errorf("delete orphan chunk vectors: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// printKindCounts prints one kind's summary line.
func printKindCounts(out io.Writer, kind string, c kindCounts) {
	fmt.Fprintf(out, "%s: embedded=%d skipped=%d too_large=%d truncated=%d\n",
		kind, c.embedded, c.skipped, c.tooLarge, c.truncated)
}

// printTotalCounts prints the run total line.
func printTotalCounts(out io.Writer, c kindCounts) {
	fmt.Fprintf(out, "total: embedded=%d skipped=%d too_large=%d truncated=%d\n",
		c.embedded, c.skipped, c.tooLarge, c.truncated)
}
