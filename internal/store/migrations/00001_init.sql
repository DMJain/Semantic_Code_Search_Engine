-- +goose Up
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE files (
    id       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    path     text    NOT NULL UNIQUE,
    package  text    NOT NULL,
    sha      text    NOT NULL,
    loc      integer NOT NULL,
    in_slice boolean NOT NULL DEFAULT false
);

CREATE TABLE symbols (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    file_id        bigint  NOT NULL REFERENCES files (id) ON DELETE CASCADE,
    kind           text    NOT NULL,
    name           text    NOT NULL,
    qualified_name text    NOT NULL UNIQUE,
    receiver       text,
    signature      text,
    start_line     integer NOT NULL,
    end_line       integer NOT NULL,
    doc            text
);
CREATE INDEX symbols_file_id_idx ON symbols (file_id);
CREATE INDEX symbols_name_trgm_idx ON symbols USING gin (name gin_trgm_ops);
CREATE INDEX symbols_qualified_name_trgm_idx ON symbols USING gin (qualified_name gin_trgm_ops);

CREATE TABLE chunks (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    symbol_id    bigint  NOT NULL REFERENCES symbols (id) ON DELETE CASCADE,
    file_id      bigint  NOT NULL REFERENCES files (id) ON DELETE CASCADE,
    part_idx     integer NOT NULL,
    content      text    NOT NULL,
    header       text    NOT NULL DEFAULT '',
    content_hash text    NOT NULL,
    token_count  integer NOT NULL,
    -- 'simple' config: no stemming or stopwords, which suits code identifiers.
    search_tsv   tsvector GENERATED ALWAYS AS (to_tsvector('simple', header || ' ' || content)) STORED,
    UNIQUE (symbol_id, part_idx)
);
CREATE INDEX chunks_file_id_idx ON chunks (file_id);
CREATE INDEX chunks_search_tsv_idx ON chunks USING gin (search_tsv);

CREATE TABLE edges (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    caller_id  bigint NOT NULL REFERENCES symbols (id) ON DELETE CASCADE,
    callee_id  bigint REFERENCES symbols (id) ON DELETE SET NULL,
    callee_raw text   NOT NULL,
    resolution text   NOT NULL CHECK (resolution IN ('exact', 'name_only', 'ambiguous')),
    source     text   NOT NULL CHECK (source IN ('treesitter', 'gopackages'))
);
CREATE INDEX edges_caller_id_idx ON edges (caller_id);
CREATE INDEX edges_callee_id_idx ON edges (callee_id);

-- target_id points at symbols, files or a package depending on level, so it
-- has no foreign key.
CREATE TABLE summaries (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    level          text   NOT NULL CHECK (level IN ('function', 'file', 'package')),
    target_id      bigint NOT NULL,
    text           text,
    model          text   NOT NULL,
    prompt_version text   NOT NULL,
    input_hash     text   NOT NULL,
    status         text   NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done', 'failed'))
);
CREATE INDEX summaries_level_target_idx ON summaries (level, target_id);

-- vec has no fixed dimension on purpose: several embedding models can
-- coexist. Per-model partial HNSW indexes with an expression cast, e.g.
-- ((vec::vector(1024))) WHERE model = '...', come in a later migration.
CREATE TABLE embeddings (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    target_kind text   NOT NULL,
    target_id   bigint NOT NULL,
    model       text   NOT NULL,
    vec         vector NOT NULL
);
CREATE INDEX embeddings_target_idx ON embeddings (target_kind, target_id);

CREATE TABLE llm_cache (
    prompt_hash text        NOT NULL,
    model       text        NOT NULL,
    response    text        NOT NULL,
    tokens      integer,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (prompt_hash, model)
);

CREATE TABLE traces (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    query      text        NOT NULL,
    route      text,
    payload    jsonb       NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
-- Extensions are left installed; other database objects may depend on them.
DROP TABLE traces;
DROP TABLE llm_cache;
DROP TABLE embeddings;
DROP TABLE summaries;
DROP TABLE edges;
DROP TABLE chunks;
DROP TABLE symbols;
DROP TABLE files;
