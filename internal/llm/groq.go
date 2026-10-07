package llm

import (
	"context"
	"fmt"
)

// Groq will call Groq's hosted API for the multi-hop Q&A loop. It is a stub.
type Groq struct {
	APIKey string
	Model  string
}

var _ LLM = (*Groq)(nil)

// NewGroq returns a Groq client stub.
func NewGroq(apiKey, model string) *Groq {
	return &Groq{APIKey: apiKey, Model: model}
}

// Complete always returns ErrNotImplemented.
func (g *Groq) Complete(context.Context, CompletionRequest) (CompletionResponse, error) {
	return CompletionResponse{}, fmt.Errorf("groq: %w", ErrNotImplemented)
}
