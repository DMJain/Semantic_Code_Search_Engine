# 13: Convention cleanup

Plan: S1 close-out spec, stories 40, 41, 42, 43. Review findings: package comments outside `doc.go`; ticket identifiers in code; loose bug note; test-only hooks in the GitHub client.

**What to build:** Package comments for the walk and GitHub packages move to `doc.go`, as in the other packages. Ticket identifiers (for example `S1-T12`, `S1-T14`, `sprint T03`) are removed from code and comments; they stay in docs. The GitHub package comment names its clock, option and base-URL hooks as test seams. The content of the standalone ETA bug note is folded into the S1 Results section and the loose file is removed.

**Blocked by:** 06

**Status:** ready-for-agent

- [ ] `grep` for ticket identifiers in non-doc source returns nothing
- [ ] Walk and GitHub packages each have a `doc.go` with the package comment
- [ ] The ETA bug note's content appears in Results; the loose file is gone
- [ ] `make ci-local` passes
- [ ] One commit `S1C-13: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Behavior changes.
