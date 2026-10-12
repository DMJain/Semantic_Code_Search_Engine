package main

import (
	"context"
	"fmt"
	"math"
	"net/http"

	"github.com/DMJain/Semantic_Code_Search_Engine/internal/config"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/embed"
	"github.com/DMJain/Semantic_Code_Search_Engine/internal/llm"
)

// Fixed probe inputs for doctor --probe-all (user story 25): one query-shaped
// string and two document-shaped strings, kept constant so runs are comparable.
const probeQuery = "how does the kubelet evict pods under memory pressure"

var probeDocs = []embed.Document{
	{Title: "evictPod", Text: "func (m *managerImpl) evictPod(pod *v1.Pod) bool"},
	{Title: "kubelet.go", Text: "The kubelet is the primary node agent that runs on each node."},
}

// runProbeAll probes every registry model on the fixed inputs and prints one
// line per model. It fails only on a bad probe or a skipped configured model,
// so an optional model that is not pulled does not break the run.
func runProbeAll(ctx context.Context) error {
	cfg := config.LoadEnv()

	models, err := llm.ListOllamaModels(ctx, http.DefaultClient, cfg.OllamaURL)
	if err != nil {
		return fmt.Errorf("ollama unreachable at %s: %w", cfg.OllamaURL, err)
	}

	registry, err := registryModels(cfg.OllamaURL, models)
	if err != nil {
		return err
	}
	failed := 0
	for _, m := range registry {
		if !m.pulled {
			fmt.Printf("%-24s SKIP (not pulled)  run: ollama pull %s\n", m.name, m.name)
			// HasModel's tag rule: the configured name may be untagged or
			// ":latest", so compare with the same matching, not exact equality.
			if llm.HasModel([]string{cfg.OllamaEmbedModel}, m.name) {
				failed++
			}
			continue
		}
		if !probeOne(ctx, m.e, m.name) {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d model(s) failed", failed)
	}
	fmt.Println("all embedding models passed")
	return nil
}

// probeOne probes one pulled model and prints `model dim norm PASS|FAIL`. It
// returns false on a wrong dimension, a NaN/Inf value, or a norm outside
// [NormMin, NormMax].
func probeOne(ctx context.Context, e *embed.Ollama, name string) bool {
	// The first call loads the model into memory, so allow more time.
	ctx, cancel := context.WithTimeout(ctx, embed.DefaultOllamaTimeout)
	defer cancel()

	norms, err := e.ProbeNorms(ctx, probeQuery, probeDocs)
	if err != nil {
		fmt.Printf("%-24s dim %-5d norm -       FAIL  %v\n", name, e.Dim(), err)
		return false
	}
	ok, minNorm := true, norms[0]
	for _, n := range norms {
		if n < minNorm {
			minNorm = n
		}
		if math.IsNaN(n) || math.IsInf(n, 0) || n < embed.NormMin || n > embed.NormMax {
			ok = false
		}
	}
	fmt.Printf("%-24s dim %-5d norm %.4f %s\n", name, e.Dim(), minNorm, verdict(ok, "PASS", "FAIL"))
	return ok
}
