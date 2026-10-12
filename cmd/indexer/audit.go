package main

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/summarize"
)

// defaultAuditSeed is the fixed seed the T08 audit samples with; the same seed
// and candidate ordering always gives the same sample.
const defaultAuditSeed = 42

// auditRow is one stored symbol as the audit re-reads it from the database:
// the identity fields plus the source span and doc to check against the file.
type auditRow struct {
	QualifiedName string
	Name          string
	Receiver      string
	StartLine     int
	EndLine       int
	Doc           string
	Path          string
}

func runAudit(ctx context.Context, args []string) error {
	fs := newFlagSet("audit")
	seed := fs.Int64("seed", defaultAuditSeed, "deterministic sampling seed")
	sample := fs.Int("sample", 30, "number of in-slice func/method symbols to check")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	rows, err := selectAuditRows(ctx, pool)
	if err != nil {
		return err
	}
	return auditSymbols(cfg.KubeDir, rows, *sample, *seed, os.Stdout)
}

// selectAuditRows loads every in-slice func/method symbol ordered by
// qualified_name, which is the deterministic order [sampleRows] shuffles. The
// audit deliberately has no body-length threshold, so it uses the in-slice
// predicate only, not [summarize.EligibleWhere].
func selectAuditRows(ctx context.Context, pool *pgxpool.Pool) ([]auditRow, error) {
	rows, err := pool.Query(ctx, `
		SELECT s.qualified_name, s.name, coalesce(s.receiver, ''),
		       s.start_line, s.end_line, coalesce(s.doc, ''), f.path
		FROM symbols s JOIN files f ON f.id = s.file_id
		WHERE `+summarize.InSliceFuncWhere()+`
		ORDER BY s.qualified_name`)
	if err != nil {
		return nil, fmt.Errorf("list in-slice symbols: %w", err)
	}
	defer rows.Close()

	var out []auditRow
	for rows.Next() {
		var r auditRow
		if err := rows.Scan(&r.QualifiedName, &r.Name, &r.Receiver,
			&r.StartLine, &r.EndLine, &r.Doc, &r.Path); err != nil {
			return nil, fmt.Errorf("scan symbol: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// sampleRows returns n rows drawn from rows in a reproducible order: rows must
// already be ordered by qualified name, and the seeded shuffle then makes the
// same (rows, seed) pair always yield the same sample.
func sampleRows(rows []auditRow, n int, seed int64) []auditRow {
	shuffled := slices.Clone(rows)
	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	if n < 0 {
		n = 0
	}
	if n > len(shuffled) {
		n = len(shuffled)
	}
	return shuffled[:n]
}

// auditSymbols samples in-slice symbols, checks each against its source lines,
// prints one table row per symbol, and returns an error if any mismatch is
// found.
func auditSymbols(root string, rows []auditRow, n int, seed int64, out io.Writer) error {
	sample := sampleRows(rows, n, seed)
	fmt.Fprintf(out, "audit sample: %d in-slice func/method symbols (seed %d)\n", len(sample), seed)

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "qualified_name\tresult")
	mismatches := 0
	for _, r := range sample {
		reason, err := checkSource(root, r)
		if err != nil {
			return err
		}
		result := "ok"
		if reason != "" {
			result = "MISMATCH: " + reason
			mismatches++
		}
		fmt.Fprintf(tw, "%s\t%s\n", r.QualifiedName, result)
	}
	tw.Flush()

	if mismatches > 0 {
		return fmt.Errorf("%d of %d sampled symbols mismatch", mismatches, len(sample))
	}
	fmt.Fprintf(out, "%d/%d ok\n", len(sample), len(sample))
	return nil
}

// checkSource re-reads the symbol's source file and returns "" when the stored
// row is consistent, or a human-readable mismatch reason. A read error is
// returned as an error, not a mismatch.
func checkSource(root string, r auditRow) (string, error) {
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(r.Path)))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", r.Path, err)
	}
	return checkSymbol(r, src), nil
}

// checkSymbol verifies one stored symbol against its file bytes, independently
// of the parser that produced the row: the declaration line must name the
// symbol, the receiver type re-read from the source must equal the stored one,
// and the doc must equal the contiguous // comment lines immediately above the
// declaration. It returns "" when all three hold.
func checkSymbol(r auditRow, src []byte) string {
	lines := strings.Split(string(src), "\n")
	if r.StartLine < 1 || r.EndLine > len(lines) || r.StartLine > r.EndLine {
		return fmt.Sprintf("line range %d..%d outside source (%d lines)", r.StartLine, r.EndLine, len(lines))
	}
	first := lines[r.StartLine-1]
	if !containsWord(first, r.Name) {
		return fmt.Sprintf("first line %q does not declare name %q", first, r.Name)
	}
	if r.Receiver != "" {
		got, ok := receiverFromDecl(strings.Join(lines[r.StartLine-1:], "\n"))
		if !ok || got != r.Receiver {
			return fmt.Sprintf("receiver mismatch: stored %q, source %q", r.Receiver, got)
		}
	}
	if got := docAbove(lines, r.StartLine); got != r.Doc {
		return fmt.Sprintf("doc mismatch: stored %q, source %q", r.Doc, got)
	}
	return ""
}

// receiverFromDecl extracts the receiver type text from the raw source of a
// method declaration (for example *Scheduler from `func (s *Scheduler) F()`),
// scanning to the matching close paren so a multi-line receiver is handled. It
// reads the source, not the parser's own output, so it is an independent check.
func receiverFromDecl(decl string) (string, bool) {
	const marker = "func ("
	i := strings.Index(decl, marker)
	if i < 0 {
		return "", false
	}
	depth := 0
	for k := i + len(marker) - 1; k < len(decl); k++ {
		switch decl[k] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				fields := strings.Fields(decl[i+len(marker) : k])
				if len(fields) == 0 {
					return "", false
				}
				return fields[len(fields)-1], true
			}
		}
	}
	return "", false
}

// containsWord reports whether word occurs in s on identifier boundaries, so a
// stored name New is not satisfied by NewFoo.
func containsWord(s, word string) bool {
	if word == "" {
		return false
	}
	for idx := 0; ; {
		i := strings.Index(s[idx:], word)
		if i < 0 {
			return false
		}
		i += idx
		beforeOK := i == 0 || !isIdentByte(s[i-1])
		afterOK := i+len(word) == len(s) || !isIdentByte(s[i+len(word)])
		if beforeOK && afterOK {
			return true
		}
		idx = i + 1
	}
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// docAbove returns the contiguous // comment lines immediately above the
// 1-based declaration line startLine, the same span parse.doc stores. A blank
// line or any non-comment line ends the run.
func docAbove(lines []string, startLine int) string {
	i := startLine - 2
	for i >= 0 && strings.HasPrefix(strings.TrimSpace(lines[i]), "//") {
		i--
	}
	return strings.Join(lines[i+1:startLine-1], "\n")
}
