package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/llm"
)

// oversizedProbeInput is longer than every registry model's context window
// (qwen3-embedding:0.6b has the largest native one, 32768 tokens), so
// truncate:false must reject it if the model enforces its context.
var oversizedProbeInput = strings.Repeat("kubelet evicts pods under memory pressure ", 8000)

// The context-size probe uses D-015's token estimate (bytes / 4) and its 0.8
// pre-send fraction; the over-context input is 1.5 times the context.
const (
	bytesPerEstimatedToken = 4
	withinContextFraction  = 0.8
	overContextFactor      = 1.5
)

// runOllamaChecks measures two runtime facts the truncation guard depends on:
// whether an identical generation request returns different prompt-token counts
// (prompt caching), and whether over-length embed input is rejected with
// truncation disabled.
func runOllamaChecks(ctx context.Context) error {
	cfg := config.LoadEnv()

	models, err := llm.ListOllamaModels(ctx, http.DefaultClient, cfg.OllamaURL)
	if err != nil {
		return fmt.Errorf("ollama unreachable at %s: %w", cfg.OllamaURL, err)
	}

	gen := llm.NewOllama(cfg.OllamaURL, cfg.OllamaSummaryModel, 0)
	req := llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "reply with the single word: ok"}},
	}
	first, err := gen.Complete(ctx, req)
	if err != nil {
		return fmt.Errorf("generation request 1: %w", err)
	}
	second, err := gen.Complete(ctx, req)
	if err != nil {
		return fmt.Errorf("generation request 2: %w", err)
	}
	fmt.Printf("gen_count_1 %d\n", first.PromptTokens)
	fmt.Printf("gen_count_2 %d\n", second.PromptTokens)

	registry, err := registryModels(cfg.OllamaURL, models)
	if err != nil {
		return err
	}
	failures := 0
	for _, m := range registry {
		if !m.pulled {
			fmt.Printf("embed_oversized: skip (%s not pulled)\n", m.name)
			continue
		}
		accepts := func(input string) bool {
			checkCtx, cancel := context.WithTimeout(ctx, embed.DefaultOllamaTimeout)
			defer cancel()
			return m.e.ProbeInput(checkCtx, input) == nil
		}
		fmt.Printf("embed_oversized: %s (%s)\n", verdict(accepts(oversizedProbeInput), "ok", "error"), m.name)

		// The registry num_ctx must be the size Ollama really enforces: an
		// input within 0.8 x num_ctx (estimated) is accepted and one at
		// 1.5 x num_ctx is rejected. A mismatch means the registry value is
		// wrong (D-003).
		numCtx := m.e.NumCtx()
		within := accepts(ctxProbeInput(ctxProbeBytes(numCtx, withinContextFraction)))
		over := accepts(ctxProbeInput(ctxProbeBytes(numCtx, overContextFactor)))
		fmt.Printf("embed_ctx_within: %s (%s, num_ctx %d)\n", verdict(within, "ok", "error"), m.name, numCtx)
		fmt.Printf("embed_ctx_over: %s (%s, num_ctx %d)\n", verdict(over, "ok", "error"), m.name, numCtx)
		if !within || over {
			failures++
			fmt.Printf("FAIL  %s: expected within=ok over=error at num_ctx %d; fix the registry value (D-003)\n", m.name, numCtx)
		}
	}
	if failures > 0 {
		return fmt.Errorf("embedding context-size check failed for %d model(s)", failures)
	}
	return nil
}

// ctxProbeBytes is the byte length of an input whose estimated size is factor
// times numCtx tokens.
func ctxProbeBytes(numCtx int, factor float64) int {
	return int(float64(numCtx*bytesPerEstimatedToken) * factor)
}

// ctxProbeInput returns about n bytes of English-like filler, which Ollama
// tokenizes at roughly 5 bytes per token (measured in S0-12), so an input at
// 0.8 x num_ctx estimated tokens is well inside the context and 1.5 x is well over.
func ctxProbeInput(n int) string {
	const unit = "kubelet evicts pods under memory pressure "
	return strings.Repeat(unit, n/len(unit)+1)[:n]
}
