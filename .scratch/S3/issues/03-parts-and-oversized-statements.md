# 03: Parts: split long functions, oversized statements

**What to build:** A function above the ceiling is split into parts at top-level statement boundaries; a single statement above the ceiling stays whole and is counted (D-078).

**Blocked by:** 02

**Status:** ready-for-agent

- [ ] A part is greedy-filled to at most `ChunkMaxTokens`; each part is prefixed with the signature line and a `part i/n` marker; `part_idx` starts at 0; part 0 carries the doc comment, later parts the signature only; the synthetic prefix lines are not counted in line spans.
- [ ] `function.start_line <= start_line <= end_line <= function.end_line` for every part.
- [ ] A single statement above 1000 tokens is one whole part (never split mid-statement) and is logged; `oversized_statements` is printed.
- [ ] Golden files: small function, a 3000-token function (every part at most 1000 except an oversized-statement case, all starting with the signature), one giant switch, generic function.
- [ ] `select count(*) from chunks where token_count > 1000` equals the printed `oversized_statements` count.
- [ ] Parts are idempotent and id-stable: rerun with no change leaves chunk ids unchanged; shrinking a function deletes only the vanished parts.
