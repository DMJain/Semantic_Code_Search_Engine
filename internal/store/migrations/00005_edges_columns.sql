-- +goose Up
-- Edge shape for S2 (D-073, D-076): evidence line, call kind, unresolved reason,
-- the fourth resolution value and its shape CHECKs, cascading callee FK, the
-- unique edge index, and the freshness columns on files.
ALTER TABLE files ADD COLUMN graphed_sha text;
ALTER TABLE files ADD COLUMN graphed_symbols_fp text;

ALTER TABLE edges ADD COLUMN call_line integer NOT NULL DEFAULT 0;
ALTER TABLE edges ADD COLUMN call_kind text NOT NULL DEFAULT 'unknown';
ALTER TABLE edges ADD COLUMN unresolved_reason text;
ALTER TABLE edges ADD CONSTRAINT edges_call_kind_check
    CHECK (call_kind IN ('func', 'concrete_method', 'interface_method', 'func_value', 'unknown'));
ALTER TABLE edges ADD CONSTRAINT edges_unresolved_reason_check
    CHECK (unresolved_reason IN ('external', 'no_match', 'unknown_receiver', 'func_value', 'candidates_capped'));

ALTER TABLE edges DROP CONSTRAINT edges_resolution_check;
ALTER TABLE edges ADD CONSTRAINT edges_resolution_check
    CHECK (resolution IN ('exact', 'name_only', 'ambiguous', 'unresolved'));

-- Convert rows the new shape would reject before adding its CHECKs. The old
-- schema allowed a NULL callee for exact/name_only/ambiguous too, so every
-- non-unresolved NULL-callee row is rewritten, not only name_only. On an empty
-- table this is a no-op.
UPDATE edges SET resolution = 'unresolved', unresolved_reason = 'no_match'
    WHERE callee_id IS NULL AND resolution <> 'unresolved';

ALTER TABLE edges ADD CONSTRAINT edges_shape_callee_check
    CHECK ((resolution = 'unresolved') = (callee_id IS NULL));
ALTER TABLE edges ADD CONSTRAINT edges_shape_reason_check
    CHECK ((resolution = 'unresolved') = (unresolved_reason IS NOT NULL));

ALTER TABLE edges DROP CONSTRAINT edges_callee_id_fkey;
ALTER TABLE edges ADD CONSTRAINT edges_callee_id_fkey
    FOREIGN KEY (callee_id) REFERENCES symbols (id) ON DELETE CASCADE;

CREATE UNIQUE INDEX edges_uniq
    ON edges (source, caller_id, callee_raw, call_line, coalesce(callee_id, 0));

-- +goose Down
-- Reverse exactly, converting unresolved rows back to name_only before the
-- restored three-value CHECK so the down works even with data.
DROP INDEX edges_uniq;

ALTER TABLE edges DROP CONSTRAINT edges_callee_id_fkey;
ALTER TABLE edges ADD CONSTRAINT edges_callee_id_fkey
    FOREIGN KEY (callee_id) REFERENCES symbols (id) ON DELETE SET NULL;

ALTER TABLE edges DROP CONSTRAINT edges_shape_callee_check;
ALTER TABLE edges DROP CONSTRAINT edges_shape_reason_check;
UPDATE edges SET resolution = 'name_only', unresolved_reason = NULL WHERE resolution = 'unresolved';

ALTER TABLE edges DROP CONSTRAINT edges_resolution_check;
ALTER TABLE edges ADD CONSTRAINT edges_resolution_check
    CHECK (resolution IN ('exact', 'name_only', 'ambiguous'));

ALTER TABLE edges DROP CONSTRAINT edges_call_kind_check;
ALTER TABLE edges DROP CONSTRAINT edges_unresolved_reason_check;
ALTER TABLE edges DROP COLUMN unresolved_reason;
ALTER TABLE edges DROP COLUMN call_kind;
ALTER TABLE edges DROP COLUMN call_line;

ALTER TABLE files DROP COLUMN graphed_symbols_fp;
ALTER TABLE files DROP COLUMN graphed_sha;
