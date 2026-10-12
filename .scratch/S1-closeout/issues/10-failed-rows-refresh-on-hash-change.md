# 10: Failed rows refresh on hash change

Plan: S1 close-out spec, stories 16, 17. Review finding: a failed row is skipped without `--retry-failed` even when its input hash changed, contradicting "a summary is fresh only when its input hash matches".

**What to build:** A row of any status is current only when its stored input hash equals the current hash. A failed row with a changed hash becomes pending again on the next run. A failed row with an unchanged hash stays skipped unless the retry flag is given.

**Blocked by:** 09

**Status:** ready-for-agent

- [ ] A failing test first: a failed row whose symbol's code changed is re-attempted without the retry flag
- [ ] A test: a failed row with an unchanged hash is skipped without the flag and re-attempted with it
- [ ] Done rows behave as before (hash match skip, mismatch redo)
- [ ] Status counts reflect the new pending rows
- [ ] One commit `S1C-10: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Changing the prompt version or the hash computation.
