# 02: Tracer: `indexer chunk` for plain functions and methods

**What to build:** `indexer chunk` turns every function and method of the whole filtered repo into one chunk, with the leading doc comment, exact line spans and lexical text, idempotently and with stable ids. Struct chunks, parts and headers come in later tickets.

**Blocked by:** 01

**Status:** done

- [ ] Selects files where `chunked_sha` differs from the file sha and the graph sha equals the file sha; `chunked_sha` is a hash of the file sha plus `ChunkMaxTokens`, `GroupMaxTokens` and a chunker version (D-078).
- [ ] Doc comment is the S1 `doc()` rule only (D-083): the chunker reads the stored doc and does not redefine it. `start_line = symbol.start_line - n`, `n = 0` for an empty doc else its line count.
- [ ] Golden tests: `/* */` above a function gives `doc == ""` and `start_line` at the function line; marker plus `//` doc plus function gives `start_line` at the marker line; a detached comment with a blank line is excluded; an adjacent `//go:noinline` is included.
- [ ] For every chunk, `content == lines[start_line..end_line]` (1-based) on the fixture tree; `select count(*) from chunks where start_line<=0 or end_line<start_line` returns 0.
- [ ] Identifier splitter and tsvector builder: table test (`schedulePod` gives `schedulepod`, `schedule`, `pod`; `HTTPServer` gives `httpserver`, `http`, `server`; `pod_backoff` gives `pod_backoff`, `pod`, `backoff`), splits on `_ . / : -` and whitespace, unicode and empty input do not panic, `go test -fuzz=FuzzSplit -fuzztime=20s` finds no panic.
- [ ] `select count(*) from chunks where search_tsv = ''` is 0; `search_tsv @@ to_tsquery('simple','pod')` matches the fixture function; `token_count` is the content estimate.
- [ ] Chunks upsert on `(symbol_id, part_idx)` with update-on-conflict and delete only vanished parts or removed symbols; a rerun with no source change leaves every `chunks.id` unchanged (test); the second run prints `0 files re-split, 0 chunks changed`.
- [ ] Interrupt-and-rerun produces the same chunk count as an uninterrupted run; changing `ChunkMaxTokens` re-splits all files (test).
- [ ] Chunks and `search_tsv` cover the whole filtered repo (not only the slice).
