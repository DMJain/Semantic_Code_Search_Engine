package chunk

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"
)

// update regenerates the golden file. It is manual only: `go test` and CI never
// pass -update, so a golden is never rewritten by a normal test run.
var update = flag.Bool("update", false, "regenerate golden files (manual only)")

// tokenCase is one entry of the token-estimate golden. `repeat`/`count` build a
// long input so the golden stays readable.
type tokenCase struct {
	Input  string `json:"input"`
	Repeat string `json:"repeat"`
	Count  int    `json:"count"`
	Want   int    `json:"want"`
}

// TestEstimateTokensGolden compares EstimateTokens against a checked-in golden.
// A normal run only reads the golden; -update rewrites it manually.
func TestEstimateTokensGolden(t *testing.T) {
	path := filepath.Join("testdata", "tokens.golden")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var cases []tokenCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("golden has no cases")
	}
	for i := range cases {
		in := cases[i].Input
		if cases[i].Repeat != "" {
			in = strings.Repeat(cases[i].Repeat, cases[i].Count)
		}
		got := EstimateTokens(in)
		if *update {
			cases[i].Want = got
			continue
		}
		if got != cases[i].Want {
			t.Errorf("EstimateTokens(%s) = %d, want %d (regenerate with -update if intended)", describe(in), got, cases[i].Want)
		}
	}
	if *update {
		out, err := json.MarshalIndent(cases, "", "  ")
		if err != nil {
			t.Fatalf("marshal golden: %v", err)
		}
		if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
}

func describe(s string) string {
	if len(s) > 32 {
		return fmt.Sprintf("%q...(%d bytes)", s[:32], len(s))
	}
	return fmt.Sprintf("%q", s)
}

// TestChunkSizeConstants pins the model-independent ceilings from D-078.
func TestChunkSizeConstants(t *testing.T) {
	if ChunkMaxTokens != 1000 {
		t.Errorf("ChunkMaxTokens = %d, want 1000", ChunkMaxTokens)
	}
	if GroupMaxTokens != 550 {
		t.Errorf("GroupMaxTokens = %d, want 550", GroupMaxTokens)
	}
}

// TestRequestBudgetDerivedFromRegistry pins the derived request budget: it is
// 0.8 of the smallest registry context (nomic 2048), truncated.
func TestRequestBudgetDerivedFromRegistry(t *testing.T) {
	if got := embed.MinNumCtx(); got != 2048 {
		t.Fatalf("embed.MinNumCtx() = %d, want 2048", got)
	}
	if RequestBudget != 1638 {
		t.Errorf("RequestBudget = %d, want 1638", RequestBudget)
	}
}
