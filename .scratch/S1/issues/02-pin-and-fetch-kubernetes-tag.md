# 02: Pin and fetch the Kubernetes tag

Plan: S1-T01 (`docs/plan/sprints/S1-corpus-symbols.md`). Spec user stories: 9, 10, 11, 12.

**What to build:** The project has exactly one Kubernetes source tree. The latest stable release tag (no alpha, beta or release candidate) whose go directive is not newer than the local Go toolchain is chosen, set in the environment and its example, cloned shallowly with `vendor/` kept on disk, and recorded in D-006 with its date, go directive and commit. Fetching again changes nothing.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] `git describe --tags` in the clone prints the chosen tag
- [ ] The clone has a `vendor` directory and the client-go staging directory
- [ ] The go-directive check from the sprint file succeeds (directive not newer than the local toolchain)
- [ ] D-006 holds tag, date, go directive and commit sha
- [ ] A second `make fetch-repo` is a no-op and exits 0
- [ ] One commit `S1-02: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Changing the tag later.
