package chunk

import "github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"

// Chunk ceilings from D-078, in estimated tokens (bytes/4). They are
// model-independent: a chunk row is shared across models, so only one ceiling
// keeps A0/A1 a pure model comparison.
const (
	ChunkMaxTokens = 1000
	GroupMaxTokens = 550
)

// RequestBudget is the largest estimated request a model can serve. It was
// 0.8 of the smallest registry context, 1638 (D-078). The nomic fit check
// replaced it with floor(0.95*2048/ratio_max), because nomic's real tokenizer
// produced up to 1.7941x the bytes/4 estimate on the embeddable
// chunk_header documents, so the 1638 budget overflowed nomic's 2048 context
// (D-078 outcome). It is now a measured constant, not the derived formula.
var RequestBudget = 1084

// MaxDocPrefixTokens is the largest static document prefix in the embed
// registry, in estimated tokens (nomic's "search_document: ", 5). The header
// budget subtracts it so an A1 document still fits the smallest context (D-082).
var MaxDocPrefixTokens = EstimateTokens(embed.MaxDocPrefix())

// EmbeddableContentMax is the largest chunk content that can embed once its
// header and the document prefix are added: RequestBudget less the largest
// document prefix (D-082). A chunk whose token_count is above it is
// oversized_failed; no header degradation can save it.
var EmbeddableContentMax = RequestBudget - MaxDocPrefixTokens

// EstimateTokens estimates a string's token count as ceil(bytes/4), the one
// estimate reused by chunking, embedding and the header budget (D-078).
func EstimateTokens(s string) int {
	return (len(s) + 3) / 4
}
