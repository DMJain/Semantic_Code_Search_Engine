package walk

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// batchSize is the number of rows per upsert statement (sprint T03).
const batchSize = 500

// Sync upserts files into the files table in batches inside one transaction,
// then deletes rows whose path is not in files. It returns the number of rows
// inserted or updated, so an unchanged second walk reports 0 changed, and the
// number of vanished rows deleted.
func Sync(ctx context.Context, pool *pgxpool.Pool, files []File) (changed, deleted int, err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for start := 0; start < len(files); start += batchSize {
		end := min(start+batchSize, len(files))
		n, err := upsertBatch(ctx, tx, files[start:end])
		if err != nil {
			return 0, 0, err
		}
		changed += n
	}

	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	if err := rehomeVanishedSymbols(ctx, tx, paths); err != nil {
		return 0, 0, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM files WHERE path <> ALL($1::text[])`, paths)
	if err != nil {
		return 0, 0, fmt.Errorf("delete vanished files: %w", err)
	}
	deleted = int(tag.RowsAffected())

	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("commit: %w", err)
	}
	return changed, deleted, nil
}

// rehomeVanishedSymbols keeps symbol ids (and so their summaries) alive across
// the delete of vanished files, which would otherwise cascade the symbols away
// while summaries have no foreign key. Symbols of a vanished file move to the
// first surviving file of the same package, and that file's parsed marker is
// cleared so the parse stage re-parses the package and deletes, by its
// whole-package seen set, the symbols that really are gone (S1C-11). Symbols
// of a package with no surviving file cascade away; the parse stage then
// deletes the function summaries left without a symbol.
func rehomeVanishedSymbols(ctx context.Context, tx pgx.Tx, paths []string) error {
	if _, err := tx.Exec(ctx, `
		WITH moved AS (
			UPDATE symbols s SET file_id = t.tid
			FROM (
				SELECT g.id AS gid,
				       (SELECT f.id FROM files f
				         WHERE f.package = g.package AND f.path = ANY($1::text[])
				         ORDER BY f.path LIMIT 1) AS tid
				FROM files g WHERE g.path <> ALL($1::text[])
			) t
			WHERE s.file_id = t.gid AND t.tid IS NOT NULL
			RETURNING t.tid
		)
		UPDATE files SET parsed_sha = NULL WHERE id IN (SELECT tid FROM moved)`, paths); err != nil {
		return fmt.Errorf("rehome symbols of vanished files: %w", err)
	}
	return nil
}

// upsertBatch writes one multi-row upsert and returns the rows it changed
// (inserted or updated). A row whose stored values already match is left
// untouched and not counted, which is what makes a second walk report zero.
func upsertBatch(ctx context.Context, tx pgx.Tx, files []File) (int, error) {
	var b strings.Builder
	b.WriteString("INSERT INTO files (path, package, sha, loc, in_slice) VALUES ")
	args := make([]any, 0, len(files)*5)
	for i, f := range files {
		if i > 0 {
			b.WriteByte(',')
		}
		p := i * 5
		fmt.Fprintf(&b, "($%d,$%d,$%d,$%d,$%d)", p+1, p+2, p+3, p+4, p+5)
		args = append(args, f.Path, f.Package, f.SHA, f.LOC, f.InSlice)
	}
	b.WriteString(` ON CONFLICT (path) DO UPDATE SET
		package = EXCLUDED.package, sha = EXCLUDED.sha, loc = EXCLUDED.loc, in_slice = EXCLUDED.in_slice
		WHERE (files.package, files.sha, files.loc, files.in_slice)
			IS DISTINCT FROM (EXCLUDED.package, EXCLUDED.sha, EXCLUDED.loc, EXCLUDED.in_slice)`)

	tag, err := tx.Exec(ctx, b.String(), args...)
	if err != nil {
		return 0, fmt.Errorf("upsert files: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
