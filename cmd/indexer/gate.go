package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/store"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/summarize"
)

// gateEntry is one eligible in-slice function in the quality-gate sample: its
// identity, source span, slice root and stored current-version summary.
type gateEntry struct {
	ID            int64
	QualifiedName string
	Path          string
	StartLine     int
	EndLine       int
	Root          string
	Status        string
	InputHash     string
	Text          string
}

// gateVerdict is one jsonl line of the quality-gate sheet. Verdict and
// Criterion are left empty for the author to fill in (D-068); the gate tool
// never grades.
type gateVerdict struct {
	QualifiedName string `json:"qualified_name"`
	InputHash     string `json:"input_hash"`
	Verdict       string `json:"verdict"`
	Criterion     string `json:"criterion"`
}

// runGate reproduces the S1-T14 quality-gate sample without any Ollama call:
// it draws the same `--sample N --seed S` set the gate run summarized, takes
// `--draw` done rows by a seeded shuffle, and writes the human sheet and the
// empty-verdict jsonl the author grades.
func runGate(ctx context.Context, args []string) error {
	fs := newFlagSet("gate")
	sampleN := fs.Int("sample", 200, "gate sample size (must match the summarize run)")
	seed := fs.Int64("seed", 3, "sampling and draw seed")
	drawN := fs.Int("draw", 30, "done rows to print and write for grading")
	mdPath := fs.String("md", "", "markdown sheet path (default .scratch/S1/quality-gate-<version>.md)")
	jsonlPath := fs.String("jsonl", "", "jsonl verdict path (default .scratch/S1/quality-gate-<version>.jsonl)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *mdPath == "" {
		*mdPath = fmt.Sprintf(".scratch/S1/quality-gate-%s.md", summarize.PromptVersion)
	}
	if *jsonlPath == "" {
		*jsonlPath = fmt.Sprintf(".scratch/S1/quality-gate-%s.jsonl", summarize.PromptVersion)
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

	rows, err := gateRows(ctx, pool)
	if err != nil {
		return err
	}
	cands := make([]summarize.Candidate, len(rows))
	byID := make(map[int64]gateEntry, len(rows))
	for i, r := range rows {
		cands[i] = summarize.Candidate{ID: r.ID, QualifiedName: r.QualifiedName, Root: r.Root}
		byID[r.ID] = r
	}
	ids := summarize.Sample(cands, *sampleN, *seed)
	gate := make([]gateEntry, 0, len(ids))
	for _, id := range ids {
		gate = append(gate, byID[id])
	}
	fmt.Printf("gate sample: %d rows (seed %d) of %d eligible\n", len(gate), *seed, len(rows))

	done := make([]gateEntry, 0, len(gate))
	for _, e := range gate {
		if e.Status == "done" {
			done = append(done, e)
		}
	}
	selected := drawGate(done, *drawN, *seed)
	fmt.Printf("gate draw: %d of %d done rows (seed %d)\n", len(selected), len(done), *seed)

	for _, e := range selected {
		code, err := readGateCode(cfg.KubeDir, e)
		if err != nil {
			return err
		}
		fmt.Printf("\n=== %s\ninput_hash %s\nsummary: %s\ncode:\n%s\n", e.QualifiedName, e.InputHash, e.Text, code)
	}

	if err := writeGateSheet(*mdPath, selected, cfg.KubeDir, *seed, len(gate), len(rows)); err != nil {
		return err
	}
	if err := writeGateJSONL(*jsonlPath, selected); err != nil {
		return err
	}
	fmt.Printf("\nwrote %s and %s\n", *mdPath, *jsonlPath)
	return nil
}

// gateRows loads every eligible in-slice function/method with its stored
// fn-v1 summary fields, ordered by qualified_name — the same candidates the
// summarize run samples.
func gateRows(ctx context.Context, pool *pgxpool.Pool) ([]gateEntry, error) {
	rows, err := pool.Query(ctx, `
		SELECT s.id, s.qualified_name, f.path, s.start_line, s.end_line,
		       coalesce(sm.status, ''), coalesce(sm.input_hash, ''), coalesce(sm.text, '')
		FROM symbols s
		JOIN files f ON f.id = s.file_id
		LEFT JOIN summaries sm
		  ON sm.level = 'function' AND sm.target_id = s.id AND sm.prompt_version = $1
		WHERE `+summarize.EligibleWhere()+`
		ORDER BY s.qualified_name`, summarize.PromptVersion)
	if err != nil {
		return nil, fmt.Errorf("list gate rows: %w", err)
	}
	defer rows.Close()

	var out []gateEntry
	for rows.Next() {
		var e gateEntry
		if err := rows.Scan(&e.ID, &e.QualifiedName, &e.Path, &e.StartLine, &e.EndLine,
			&e.Status, &e.InputHash, &e.Text); err != nil {
			return nil, fmt.Errorf("scan gate row: %w", err)
		}
		e.Root = summarize.SliceRoot(e.Path)
		out = append(out, e)
	}
	return out, rows.Err()
}

// drawGate returns n rows drawn reproducibly from rows: the rows must already
// be ordered by qualified_name, and the seeded shuffle then makes the same
// (rows, n, seed) always yield the same sample.
func drawGate(rows []gateEntry, n int, seed int64) []gateEntry {
	out := slices.Clone(rows)
	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	if n < 0 {
		n = 0
	}
	if n > len(out) {
		n = len(out)
	}
	return out[:n]
}

// readGateCode returns lines start_line..end_line (1-based, inclusive) of the
// symbol's file under root, the exact source span the author grades against.
func readGateCode(root string, e gateEntry) (string, error) {
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.Path)))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", e.Path, err)
	}
	lines := strings.Split(string(src), "\n")
	start, end := e.StartLine, e.EndLine
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return "", nil
	}
	return strings.Join(lines[start-1:end], "\n"), nil
}

// writeGateSheet writes the human-readable sheet: one section per sampled row
// with its qualified name, code, summary and a blank verdict line.
func writeGateSheet(path string, rows []gateEntry, root string, seed int64, gateN, eligible int) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Quality gate %s (seed %d)\n\n", summarize.PromptVersion, seed)
	fmt.Fprintf(&b, "Gate sample: %d rows of %d eligible (seed %d). Grading rows below: %d.\n\n", gateN, eligible, seed, len(rows))
	for i, e := range rows {
		code, err := readGateCode(root, e)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "## %d. %s\n\n", i+1, e.QualifiedName)
		fmt.Fprintf(&b, "input_hash: `%s`\n\n", e.InputHash)
		b.WriteString("```go\n")
		b.WriteString(code)
		if !strings.HasSuffix(code, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
		fmt.Fprintf(&b, "summary: %s\n\n", e.Text)
		b.WriteString("verdict: \n\n")
	}
	return writeGateFile(path, []byte(b.String()))
}

// writeGateJSONL writes one line per sampled row with empty verdict and
// criterion fields for the author to fill (D-068); the tool never invents a
// verdict.
func writeGateJSONL(path string, rows []gateEntry) error {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	for _, e := range rows {
		if err := enc.Encode(gateVerdict{QualifiedName: e.QualifiedName, InputHash: e.InputHash}); err != nil {
			return err
		}
	}
	return writeGateFile(path, []byte(b.String()))
}

func writeGateFile(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644)
}
