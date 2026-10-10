# 01: Choose and pin the Kubernetes tag (S1-T01)

Plan: S1-T01 (`docs/plan/sprints/S1-corpus-symbols.md`)

**What to build:** Choose the latest stable `v1.N.0` (no alpha, beta or rc) whose `go` directive is at most the local Go toolchain, set `KUBE_TAG` in `.env` and the example in `.env.example`, run `make fetch-repo`, and record D-006 (tag, date, go directive, commit sha). Process setup: the plan-doc commit and `.scratch/S1/` are already committed on `s1-corpus-symbols`.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

**Spec user stories:** 6, 7, 41, 43

- [ ] `git -C data/kubernetes describe --tags` prints the tag
- [ ] `test -d data/kubernetes/vendor && test -d data/kubernetes/staging/src/k8s.io/client-go` succeeds
- [ ] The go-directive check from the sprint file succeeds (directive <= local Go)
- [ ] D-006 filled with tag, date, go directive and commit sha; a second `make fetch-repo` is a no-op, exit 0
- [ ] One commit `S1-T01: ...`; nothing pushed
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Changing the tag later.
