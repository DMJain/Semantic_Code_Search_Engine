// Package llm defines the text-completion interface used for summarization
// (local Ollama) and multi-hop Q&A (Groq).
package llm

import (
	"context"
	"errors"
)

// LLM produces a completion for a chat-style request.
type LLM interface {
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
}

// Message is one chat turn. Role is "system", "user" or "assistant".
// Provisional: may gain tool-call fields once the Q&A loop exists.
type Message struct {
	Role    string
	Content string
}

// CompletionRequest is provisional; fields will change as summarization and
// Q&A are built.
type CompletionRequest struct {
	// System is an optional system prompt, sent before Messages.
	System   string
	Messages []Message
	// Temperature is passed through as-is; 0 means deterministic.
	Temperature float64
	// MaxTokens caps generated tokens; 0 leaves the provider default.
	MaxTokens int
}

// CompletionResponse is provisional.
type CompletionResponse struct {
	Text             string
	Model            string
	PromptTokens     int
	CompletionTokens int
}

// ErrNotImplemented is returned by stub clients.
var ErrNotImplemented = errors.New("not implemented")
