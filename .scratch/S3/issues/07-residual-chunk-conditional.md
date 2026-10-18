# 07: Residual chunk (conditional on D-080 trigger)

**What to build:** CONDITIONAL: do not start unless ticket 06 recorded `zero_chunk_files_with_decls >= 18`; otherwise close this ticket as not needed. Adds one chunk per file for uncovered top-level declarations so those files can be retrieved.

**Blocked by:** 06

**Status:** done

- [ ] Only starts if the trigger fired in ticket 06; otherwise the ticket is closed with the recorded number and no code change.
- [ ] One residual chunk per file holding its uncovered top-level const, var and type-alias declarations, statement-aligned, split at `ChunkMaxTokens`, with the same header, doc rule, stable ids and idempotency as other chunks.
- [ ] `zero_chunk_files_with_decls` drops to 0 on the real run; the new count is recorded in Results.
- [ ] `content == lines[start_line..end_line]`, `search_tsv` non-empty and golden fixtures (a const-only file, a var-only file, a file with a type alias) pass.
- [ ] A new decision entry records the residual chunk rule and the migration (if any).
