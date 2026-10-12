# 02: [human] Re-run the yield and record the outcome

Plan: S1 close-out spec, stories 5, 6, 7. Review finding: the yield of 150 recorded in D-070 may be overstated.

**What to build:** After ticket 01, re-run the mining-yield probe on the frozen candidate slice with the corrected rule. The agent records the new figure, the date and the rejection counts in a new append-only decision entry that references D-070 and leaves it unedited. If tiers 1 plus 2 give at least 30 candidates, S1 stands. If fewer, the agent stops and the owner decides between the options in D-005 (for example extending the mining window); no agent changes the slice.

**Blocked by:** 01

**Status:** ready-for-human

- [ ] The probe is run with the corrected filter; the output file is saved in the S1 results folder
- [ ] A new decision entry records the figure, the date, the rejection counts and the comparison with 150
- [ ] The frozen-slice hash check (`make verify-slice`) still passes and the slice file list is untouched
- [ ] If the figure is under 30: the owner's decision is recorded in the entry and the sprint doc, before this ticket closes
- [ ] One commit `S1C-02: ...`
- [ ] Report: the commands run (trimmed output) and the commit hash

Out of scope: Changing the slice composition without an owner decision.
