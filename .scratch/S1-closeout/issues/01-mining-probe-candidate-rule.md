# 01: Mining probe applies the candidate rule as written

Plan: S1 close-out spec (`.scratch/S1-closeout/spec.md`), stories 1, 2, 3, 4, 50. Review findings: probe skips non-Go files instead of rejecting the pull request; search uses "links an issue" instead of "closed an issue".

**What to build:** The mining-yield probe counts a merged pull request only when every file it changes is a non-test, non-generated Go file inside the candidate slice and the file count is one to five. Any other file type rejects the pull request. The search selects pull requests that closed an issue (closing-issue references), not ones that merely link an issue; if the API cannot express this directly, filter after fetching and document it. The probe prints the count of rejections per reason: non-Go file, test file, generated file, outside slice, too many files, no closing issue.

**Blocked by:** None (can start immediately)

**Status:** ready-for-agent

- [ ] A failing test first: a recorded pull request touching one slice Go file plus a workflow or docs file is rejected (it was counted before)
- [ ] A recorded pull request that only links an issue is rejected; one that closed an issue is accepted
- [ ] Rejection counts per reason are printed and tested
- [ ] Existing probe tests stay green; the cached client is reused (no live calls in tests)
- [ ] The probe is not re-run against the live API here (that is ticket 02)
- [ ] One commit `S1C-01: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Recording the new yield figure (ticket 02); changing the frozen slice.
