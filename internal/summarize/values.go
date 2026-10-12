package summarize

// Status is the state of one summaries row. The spellings are the ones the
// summaries.status CHECK constraint accepts; this file is the only place they
// are written in non-test code, and SQL receives them as parameters.
type Status string

// The summary statuses.
const (
	StatusPending Status = "pending"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// String returns the stored spelling.
func (s Status) String() string { return string(s) }

// Level is the granularity a summaries row describes, matching the
// summaries.level CHECK constraint. Only [LevelFunction] is produced today;
// file and package arrive in S5.
type Level string

// The summary levels.
const (
	LevelFunction Level = "function"
	LevelFile     Level = "file"
	LevelPackage  Level = "package"
)

// String returns the stored spelling.
func (l Level) String() string { return string(l) }
