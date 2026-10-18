# 04: Struct, interface and group chunks

**What to build:** Every struct and interface gets its own chunk, and a type with a same-file constructor becomes one group chunk when the pair is small.

**Blocked by:** 02

**Status:** done

- [ ] Every struct and interface gets its own chunk: signature, doc comment and body text, up to the ceiling.
- [ ] A struct or interface is grouped with a function in the same file and package named `NewX` or `newX` (or whose single return type is `*X` or `X`) only when the pair is at most `GroupMaxTokens` (550); the group chunk's owner is the type's symbol and its span covers both declarations; a grouped chunk uses the first declaration's doc start for `start_line`.
- [ ] A constructor in another file is never grouped; a combined size above 550 gives two separate chunks; a grouped constructor appears only in the group chunk.
- [ ] Golden fixtures for: struct with constructor (one chunk), struct without constructor (one chunk), constructor over 550 combined (separate), constructor in another file (separate).
- [ ] `select symbol_id, count(*) from chunks where part_idx=0 group by 1 having count(*)>1` returns 0.
- [ ] `content == lines[start_line..end_line]` holds for group chunks (including the lines between the two declarations).
