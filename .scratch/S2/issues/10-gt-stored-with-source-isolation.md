# 10: Ground truth stored with source isolation

Plan: S2 spec (`.scratch/S2/spec.md`). Spec user stories: 14-24, 35, 82, 100, 36, 97, 99.

**What to build:** The VTA call graph from ticket 01 is stored as ground-truth edges. Only edges whose caller and callee are both in slice files are kept. Each SSA function maps to a symbol's qualified name with pointer receivers preserved; closures attribute to the enclosing declared function; callers with no symbol (package variable initialisers, synthetic `init`, bound-method and thunk wrappers) are dropped and counted; generic instantiations map to their origin symbol; any in-slice edge that cannot be mapped to a symbol id is counted as `gt_unmapped` and printed. Kinds come from the SSA call mode (static function `func`, static method `concrete_method`, invoke `interface_method`, dynamic `func_value`). Rows have `source='gopackages'`, `resolution='exact'`, no reason, the callee's qualified name as raw text and the call line (0 if unavailable). All gopackages rows are replaced in one transaction; two runs give identical counts. Printed counts include a GT-side partition (mapped, dropped by reason, `gt_unmapped`) that sums to the total.

Isolation (D-077): `graph --stats` and `graph --status` ignore GT rows. The structural-retriever criterion is added to S6-T05 and recorded in D-077 (already written; verify it is present).

**Blocked by:** 01, 02

**Status:** ready-for-agent

- [ ] Committed tiny module fixture (interface, function value, concrete method, closure, a generic): `go run ./cmd/callgraph-gt` over it gives rows of all four kinds, correct closure attribution, dropped synthetic callers counted, `gt_unmapped` printed, and identical counts on a second run
- [ ] The GT-side partition printed by the run sums to the total (asserted)
- [ ] With one GT row and one tree-sitter row for the same caller/callee pair, `graph --stats` and `graph --status` output is unchanged by the GT row (test)
- [ ] Real run: `select call_kind, count(*) from edges where source='gopackages' group by 1` shows all four kinds; wall time and peak RSS recorded; `--check` still prints `file-set: equal`
- [ ] The S6-T05 acceptance and D-077 carry the one-edge isolation criterion; S6-T05 also states that the retriever follows only edges with a callee and lists unresolved rows separately with their reason (verify present)
- [ ] `make test` is green with a throwaway database per test and no new skips; `make ci-local` passes
- [ ] One commit `S2-10: ...` that also sets this ticket's Status to done
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: The report; any retriever; using GT in the server (forbidden).
