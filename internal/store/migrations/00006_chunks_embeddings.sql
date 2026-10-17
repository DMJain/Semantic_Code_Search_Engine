-- +goose Up
-- Chunking and embedding shape for S3 (D-078, D-081, D-082): an input hash and
-- a dedup key on embeddings, a chunker-parameter stamp on files, chunk line
-- spans and a degradation marker, and the per-model failure ledger. The
-- chunks.header and chunks.content_hash columns already exist from 00001 and
-- are left untouched here.
ALTER TABLE embeddings ADD COLUMN input_hash text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX embeddings_uniq ON embeddings (target_kind, target_id, model);

ALTER TABLE files ADD COLUMN chunked_sha text;

ALTER TABLE chunks ADD COLUMN start_line integer NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN end_line integer NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN header_degraded smallint NOT NULL DEFAULT 0;

CREATE TABLE embed_failures (
    target_kind text        NOT NULL,
    target_id   bigint      NOT NULL,
    model       text        NOT NULL,
    reason      text        NOT NULL,
    input_hash  text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (target_kind, target_id, model)
);

-- +goose Down
-- Reverse every object added here. chunks.header and chunks.content_hash are
-- not dropped: they predate this migration.
DROP TABLE embed_failures;

ALTER TABLE chunks DROP COLUMN header_degraded;
ALTER TABLE chunks DROP COLUMN end_line;
ALTER TABLE chunks DROP COLUMN start_line;

ALTER TABLE files DROP COLUMN chunked_sha;

DROP INDEX embeddings_uniq;
ALTER TABLE embeddings DROP COLUMN input_hash;
