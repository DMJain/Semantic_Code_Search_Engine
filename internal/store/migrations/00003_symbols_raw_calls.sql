-- +goose Up
-- Corpus-stage columns: raw call names for summarization input (D-019), the
-- parse marker for idempotent/resumable parsing, a failure reason for
-- summaries, and the uniqueness rule that stops two runs double-writing a
-- summary. Down drops exactly these four objects.
ALTER TABLE symbols ADD COLUMN raw_calls text[] NOT NULL DEFAULT '{}';
ALTER TABLE files ADD COLUMN parsed_sha text;
ALTER TABLE summaries ADD COLUMN reason text;
CREATE UNIQUE INDEX summaries_uniq ON summaries (level, target_id, prompt_version);

-- +goose Down
DROP INDEX summaries_uniq;
ALTER TABLE summaries DROP COLUMN reason;
ALTER TABLE files DROP COLUMN parsed_sha;
ALTER TABLE symbols DROP COLUMN raw_calls;
