-- +goose Up
-- Completion time of a summary row, set when the row becomes done, so the
-- summarize status derives its rate from stored times instead of a live
-- sample. Nullable: rows that were already done keep NULL (no backfill).
ALTER TABLE summaries ADD COLUMN completed_at timestamptz;

-- +goose Down
ALTER TABLE summaries DROP COLUMN completed_at;
