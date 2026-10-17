package chunk

import "github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"

// Chunk ceilings from D-078, in estimated tokens (bytes/4). They are
// model-independent: a chunk row is shared across models, so only one ceiling
// keeps A0/A1 a pure model comparison.
const (
	ChunkMaxTokens = 1000
	GroupMaxTokens = 550
)

// RequestBudget is the largest estimated request a model can serve: 0.8 of the
// smallest registry context (D-078), derived rather than hard-coded.
var RequestBudget = int(0.8 * float64(embed.MinNumCtx()))

// EstimateTokens estimates a string's token count as ceil(bytes/4), the one
// estimate reused by chunking, embedding and the header budget (D-078).
func EstimateTokens(s string) int {
	return (len(s) + 3) / 4
}
