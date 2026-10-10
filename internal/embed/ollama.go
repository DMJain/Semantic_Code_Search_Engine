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

// Probe accepts a raw vector as normalized when its L2 norm is in this range.
const (
	NormMin = 0.95
	NormMax = 1.05
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

// ProbeOversizedInput sends one input to /api/embed with truncate:false and
// returns the server's error, or nil if it accepted the input. It measures
// whether over-length input is rejected, which D-015's guard depends on.
func (o *Ollama) ProbeOversizedInput(ctx context.Context, input string) error {
	truncate := false
	_, err := o.embed(ctx, []string{input}, &truncate)
	return err
}

func (o *Ollama) embedNormalized(ctx context.Context, inputs []string) ([][]float32, error) {
	vecs, err := o.embedRaw(ctx, inputs)
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

// embed calls /api/embed and validates the output shape and values. A nil
// truncate omits the field (Ollama's default); a non-nil value sets it.
func (o *Ollama) embed(ctx context.Context, inputs []string, truncate *bool) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()

	reqBody := map[string]any{"model": o.model, "input": inputs}
	if truncate != nil {
		reqBody["truncate"] = *truncate
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embed", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed (%s): %w", o.model, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return nil, fmt.Errorf("ollama embed (%s): %s: %s", o.model, res.Status, strings.TrimSpace(string(msg)))
	}

	// Decode as float64 so values beyond float32 range surface as Inf in the
	// check below instead of as a decode error.
	var body struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("ollama embed (%s): decode response: %w", o.model, err)
	}
	if len(body.Embeddings) != len(inputs) {
		return nil, fmt.Errorf("ollama embed (%s): got %d vectors for %d inputs", o.model, len(body.Embeddings), len(inputs))
	}
	out := make([][]float32, len(body.Embeddings))
	for i, raw := range body.Embeddings {
		v := make([]float32, len(raw))
		for j, x := range raw {
			v[j] = float32(x)
		}
		if err := checkVector(v, o.spec.dim); err != nil {
			return nil, fmt.Errorf("ollama embed (%s): input %d: %w", o.model, i, err)
		}
		out[i] = v
	}
	return out, nil
}

func checkVector(v []float32, dim int) error {
	if len(v) != dim {
		return fmt.Errorf("vector has %d dimensions, want %d", len(v), dim)
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
