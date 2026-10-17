package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// DefaultOllamaTimeout bounds a single /api/embed call.
const DefaultOllamaTimeout = 60 * time.Second

// ErrWrongDimension reports a vector whose length is not the model's Dim(). It
// is a distinct, typed error so the embed stage treats a malformed reply as a
// hard error and never stores the vector (architecture "Ollama requests").
var ErrWrongDimension = errors.New("embedding vector has the wrong dimension")

// ErrInputTooLong reports that Ollama rejected an input as over the model's
// context. It is a distinct, typed error so the embed stage records the item as
// a truncation failure instead of mistaking every per-item error for one
// (architecture "Ollama requests"). Only a 400/413/422 whose body names a
// context/length overflow is wrapped.
var ErrInputTooLong = errors.New("embedding input exceeds the model context")

// Probe accepts a raw vector as normalized when its L2 norm is in this range
// (plus or minus 1%; both supported models measure 1.0000, D-060).
const (
	NormMin = 0.99
	NormMax = 1.01
)

// Ollama embeds text through a local Ollama server's /api/embed endpoint.
// Returned vectors are always L2-normalized.
type Ollama struct {
	baseURL string
	model   string
	spec    modelSpec
	timeout time.Duration
	client  *http.Client
}

var _ Embedder = (*Ollama)(nil)

// NewOllama returns an embedder for model at baseURL. It fails for a model
// without a template registry entry. A zero timeout uses DefaultOllamaTimeout.
func NewOllama(baseURL, model string, timeout time.Duration) (*Ollama, error) {
	spec, err := lookup(model)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = DefaultOllamaTimeout
	}
	return &Ollama{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		spec:    spec,
		timeout: timeout,
		client:  &http.Client{},
	}, nil
}

// Dim implements Embedder.
func (o *Ollama) Dim() int { return o.spec.dim }

// Model implements Embedder.
func (o *Ollama) Model() string { return o.model }

// NumCtx is the context size sent with every embed request. The pre-send
// guard of D-015 compares request size with 0.8 x NumCtx.
func (o *Ollama) NumCtx() int { return o.spec.numCtx }

// EmbedQueries implements Embedder.
func (o *Ollama) EmbedQueries(ctx context.Context, queries []string) ([][]float32, error) {
	inputs := make([]string, len(queries))
	for i, q := range queries {
		s, err := o.spec.renderQuery(q)
		if err != nil {
			return nil, err
		}
		inputs[i] = s
	}
	return o.embedNormalized(ctx, inputs)
}

// EmbedDocuments implements Embedder.
func (o *Ollama) EmbedDocuments(ctx context.Context, docs []Document) ([][]float32, error) {
	inputs := make([]string, len(docs))
	for i, d := range docs {
		s, err := o.spec.renderDoc(d)
		if err != nil {
			return nil, err
		}
		inputs[i] = s
	}
	return o.embedNormalized(ctx, inputs)
}

// ProbeResult reports the raw (pre-normalization) L2 norms returned by the
// model for one query and one document.
type ProbeResult struct {
	QueryNorm float64
	DocNorm   float64
}

// Normalized reports whether the model already returns unit vectors.
func (r ProbeResult) Normalized() bool {
	in := func(n float64) bool { return n >= NormMin && n <= NormMax }
	return in(r.QueryNorm) && in(r.DocNorm)
}

// Probe embeds one query and one document and checks the raw output. It
// fails on NaN/Inf values, a length other than Dim, or a zero vector.
// Unnormalized output is not a failure: check ProbeResult.Normalized, and
// note that EmbedQueries/EmbedDocuments normalize regardless.
func (o *Ollama) Probe(ctx context.Context) (ProbeResult, error) {
	q, err := o.spec.renderQuery("how does the kubelet evict pods under memory pressure")
	if err != nil {
		return ProbeResult{}, err
	}
	d, err := o.spec.renderDoc(Document{Title: "evictPod", Text: "func (m *managerImpl) evictPod(pod *v1.Pod) bool"})
	if err != nil {
		return ProbeResult{}, err
	}
	vecs, err := o.embedRaw(ctx, []string{q, d})
	if err != nil {
		return ProbeResult{}, err
	}
	res := ProbeResult{QueryNorm: l2(vecs[0]), DocNorm: l2(vecs[1])}
	for _, n := range []float64{res.QueryNorm, res.DocNorm} {
		if n < 1e-6 {
			return res, errors.New("model returned a zero vector")
		}
	}
	return res, nil
}

// ProbeNorms embeds one query-shaped input and several document-shaped inputs
// and returns the raw (pre-normalization) L2 norm of each. It fails on a wrong
// dimension (the error names both sizes) or a NaN/Inf value. The doctor uses it
// to check a model's output quality before indexing.
func (o *Ollama) ProbeNorms(ctx context.Context, query string, docs []Document) ([]float64, error) {
	inputs := make([]string, 0, 1+len(docs))
	q, err := o.spec.renderQuery(query)
	if err != nil {
		return nil, err
	}
	inputs = append(inputs, q)
	for _, d := range docs {
		s, err := o.spec.renderDoc(d)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, s)
	}
	vecs, err := o.embedRaw(ctx, inputs)
	if err != nil {
		return nil, err
	}
	norms := make([]float64, len(vecs))
	for i, v := range vecs {
		norms[i] = l2(v)
	}
	return norms, nil
}

// ProbeInput sends one input with truncate:false and the model's NumCtx and
// returns the server's error, or nil if it accepted the input. It measures
// whether over-length input is rejected, which D-015's guard depends on: the
// doctor checks that an input within 0.8 x NumCtx is accepted and one well
// over NumCtx is rejected.
func (o *Ollama) ProbeInput(ctx context.Context, input string) error {
	truncate := false
	_, err := o.embed(ctx, []string{input}, &truncate)
	return err
}

func (o *Ollama) embedNormalized(ctx context.Context, inputs []string) ([][]float32, error) {
	// Indexing must never silently truncate: an over-length input is rejected
	// by the server and surfaces as an error (architecture "Ollama requests").
	truncate := false
	vecs, err := o.embed(ctx, inputs, &truncate)
	if err != nil {
		return nil, err
	}
	for i, v := range vecs {
		n := l2(v)
		if n < 1e-6 {
			return nil, fmt.Errorf("input %d: model returned a zero vector", i)
		}
		for j := range v {
			v[j] = float32(float64(v[j]) / n)
		}
	}
	return vecs, nil
}

// embedRaw calls /api/embed and validates the output shape and values.
func (o *Ollama) embedRaw(ctx context.Context, inputs []string) ([][]float32, error) {
	return o.embed(ctx, inputs, nil)
}

// embed calls /api/embed with no keep_alive override.
func (o *Ollama) embed(ctx context.Context, inputs []string, truncate *bool) ([][]float32, error) {
	vecs, _, err := o.postEmbed(ctx, inputs, truncate, "")
	return vecs, err
}

// EmbedInput embeds one already-rendered input (no template is applied) in its
// own /api/embed request with truncate:false and the model's num_ctx, and
// returns the response's aggregate prompt_eval_count. A zero count means the
// server reported none. keepAlive is sent as the keep_alive field when
// non-empty, so a one-off run can evict the model afterwards.
func (o *Ollama) EmbedInput(ctx context.Context, input, keepAlive string) (int, error) {
	truncate := false
	_, count, err := o.postEmbed(ctx, []string{input}, &truncate, keepAlive)
	return count, err
}

// Version returns the Ollama server version from GET /api/version.
func (o *Ollama) Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"/api/version", nil)
	if err != nil {
		return "", err
	}
	res, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama version: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama version: %s", res.Status)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("ollama version: decode response: %w", err)
	}
	return body.Version, nil
}

// Unload asks Ollama to evict the model from memory (keep_alive:0) so a
// one-off run does not leave it resident.
func (o *Ollama) Unload(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	payload, err := json.Marshal(map[string]any{
		"model":      o.model,
		"keep_alive": 0,
		"stream":     false,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/generate", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama unload (%s): %w", o.model, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama unload (%s): %s", o.model, res.Status)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	return nil
}

// postEmbed calls /api/embed and validates the output shape and values. A nil
// truncate omits the field (Ollama's default); a non-nil value sets it.
// keepAlive, when non-empty, is sent as keep_alive. It returns the vectors and
// the response's aggregate prompt_eval_count (zero when absent).
func (o *Ollama) postEmbed(ctx context.Context, inputs []string, truncate *bool, keepAlive string) ([][]float32, int, error) {
	if len(inputs) == 0 {
		return nil, 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()

	reqBody := map[string]any{
		"model":   o.model,
		"input":   inputs,
		"options": map[string]any{"num_ctx": o.spec.numCtx},
	}
	if truncate != nil {
		reqBody["truncate"] = *truncate
	}
	if keepAlive != "" {
		reqBody["keep_alive"] = keepAlive
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embed", bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := o.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("ollama embed (%s): %w", o.model, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		body := strings.TrimSpace(string(msg))
		if isInputTooLong(res.StatusCode, body) {
			return nil, 0, fmt.Errorf("ollama embed (%s): %s: %s: %w", o.model, res.Status, body, ErrInputTooLong)
		}
		return nil, 0, fmt.Errorf("ollama embed (%s): %s: %s", o.model, res.Status, body)
	}

	// Decode as float64 so values beyond float32 range surface as Inf in the
	// check below instead of as a decode error.
	var body struct {
		Embeddings      [][]float64 `json:"embeddings"`
		PromptEvalCount int         `json:"prompt_eval_count"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, 0, fmt.Errorf("ollama embed (%s): decode response: %w", o.model, err)
	}
	if len(body.Embeddings) != len(inputs) {
		return nil, 0, fmt.Errorf("ollama embed (%s): got %d vectors for %d inputs", o.model, len(body.Embeddings), len(inputs))
	}
	out := make([][]float32, len(body.Embeddings))
	for i, raw := range body.Embeddings {
		v := make([]float32, len(raw))
		for j, x := range raw {
			v[j] = float32(x)
		}
		if err := checkVector(v, o.spec.dim); err != nil {
			return nil, 0, fmt.Errorf("ollama embed (%s): input %d: %w", o.model, i, err)
		}
		out[i] = v
	}
	return out, body.PromptEvalCount, nil
}

// isInputTooLong reports whether a non-200 embed reply is Ollama rejecting an
// input for exceeding the model context. It requires both a status that can
// carry that rejection (400, 413, 422) and a body naming a context/length
// overflow, so a transport or server error is never mistaken for truncation.
func isInputTooLong(status int, body string) bool {
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
	default:
		return false
	}
	b := strings.ToLower(body)
	for _, phrase := range []string{"context", "length", "too long", "context window"} {
		if strings.Contains(b, phrase) {
			return true
		}
	}
	return false
}

func checkVector(v []float32, dim int) error {
	if len(v) != dim {
		return fmt.Errorf("%w: vector has %d dimensions, want %d", ErrWrongDimension, len(v), dim)
	}
	for i, x := range v {
		if f := float64(x); math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("vector value %d is %v", i, x)
		}
	}
	return nil
}

func l2(v []float32) float64 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum)
}
