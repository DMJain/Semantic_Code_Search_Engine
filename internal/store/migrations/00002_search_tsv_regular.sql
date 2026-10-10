-- +goose Up
-- Replace the generated full-text column with a plain tsvector column so a
-- later sprint can fill it with identifier-split tokens from Go. Dropping the
-- column also drops its GIN index.
ALTER TABLE chunks DROP COLUMN search_tsv;
ALTER TABLE chunks ADD COLUMN search_tsv tsvector NOT NULL DEFAULT '';
CREATE INDEX chunks_search_tsv_idx ON chunks USING gin (search_tsv);

-- +goose Down
-- Restore 00001's generated column and index exactly.
DROP INDEX chunks_search_tsv_idx;
ALTER TABLE chunks DROP COLUMN search_tsv;
ALTER TABLE chunks ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (to_tsvector('simple', header || ' ' || content)) STORED;
CREATE INDEX chunks_search_tsv_idx ON chunks USING gin (search_tsv);
