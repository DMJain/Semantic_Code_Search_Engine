package summarize

import (
	"fmt"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/parse"
)

// MinBodyLines is the eligibility threshold of D-014: a function or method is
// summarized only when end_line - start_line (signature line excluded) is at
// least this many lines. It is the only place the number is written in
// non-test code.
const MinBodyLines = 3

// Eligible reports whether sym is a function or method symbol in the slice
// whose body is at least [MinBodyLines] lines (D-014). Types are never
// eligible.
func Eligible(sym parse.Symbol, inSlice bool) bool {
	if !inSlice || (sym.Kind != parse.KindFunc && sym.Kind != parse.KindMethod) {
		return false
	}
	return sym.EndLine-sym.StartLine >= MinBodyLines
}

// InSliceFuncWhere is the SQL condition for an in-slice function or method,
// with no body-length threshold. It expects the aliases s (symbols) and f
// (files).
func InSliceFuncWhere() string {
	return fmt.Sprintf("f.in_slice AND s.kind IN ('%s', '%s')", parse.KindFunc, parse.KindMethod)
}

// EligibleWhere returns the SQL form of [Eligible] as a WHERE condition over
// the aliases s (symbols) and f (files).
func EligibleWhere() string {
	return fmt.Sprintf("%s AND s.end_line - s.start_line >= %d", InSliceFuncWhere(), MinBodyLines)
}

// SkippedWhere returns the SQL condition for in-slice functions and methods
// that are not eligible because their body is too short, over the aliases s
// and f.
func SkippedWhere() string {
	return fmt.Sprintf("%s AND s.end_line - s.start_line < %d", InSliceFuncWhere(), MinBodyLines)
}
