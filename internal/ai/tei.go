package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

/*
teiProvider talks to HuggingFace text-embeddings-inference.

Two containers run on LXC 109 (the CaBrain box): BAAI/bge-m3 on 127.0.0.1:18081
and BAAI/bge-reranker-v2-m3 on 127.0.0.1:18082. They are bound to loopback, so
production cannot reach them today — see the infrastructure addendum in
docs/opportunity-radar/00-inspection.md. That is a bind change, not a code
change, and this provider is written so the day it happens is one env var.

TEI is not OpenAI-shaped: POST /embed takes {"inputs": [...]} and returns a bare
array of vectors, and POST /rerank takes {"query", "texts"} and returns scored
indices. Hence its own provider rather than a flag on the OpenAI one.

The model is not chosen per request — a TEI container serves exactly one model,
which is why the base URL is the model selector here. The name is read from
GET /info when it is not configured, so provenance records what actually
answered rather than what someone typed.
*/
type teiProvider struct {
	cfg    ProviderConfig
	client *http.Client
}

func newTEIProvider(pc ProviderConfig) *teiProvider {
	return &teiProvider{cfg: pc, client: &http.Client{Timeout: pc.Timeout}}
}

func (p *teiProvider) Name() string { return p.cfg.Name }
func (p *teiProvider) Paid() bool   { return p.cfg.Paid }

func (p *teiProvider) EmbedModel() string {
	if p.cfg.EmbedModel != "" {
		return p.cfg.EmbedModel
	}
	return p.modelFromInfo()
}

func (p *teiProvider) RerankModel() string {
	if p.cfg.RerankModel != "" {
		return p.cfg.RerankModel
	}
	return p.modelFromInfo()
}

// modelFromInfo asks the container what it loaded. Best-effort: an unknown
// model name must not fail a call, but it must not be silently invented
// either, so the fallback string says it is unknown.
func (p *teiProvider) modelFromInfo() string {
	if p.cfg.Model != "" {
		return p.cfg.Model
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.BaseURL+"/info", nil)
	if err != nil {
		return "unknown"
	}
	res, err := p.client.Do(req)
	if err != nil {
		return "unknown"
	}
	defer res.Body.Close()

	var info struct {
		ModelID string `json:"model_id"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&info) != nil || info.ModelID == "" {
		return "unknown"
	}
	return info.ModelID
}

func (p *teiProvider) Embed(ctx context.Context, texts []string) ([][]float32, Usage, error) {
	body := map[string]any{"inputs": texts, "truncate": true}

	var vectors [][]float32
	if err := p.post(ctx, "/embed", body, &vectors); err != nil {
		return nil, Usage{}, err
	}
	if len(vectors) != len(texts) {
		return nil, Usage{}, fmt.Errorf("%s: asked for %d embeddings, got %d", p.cfg.Name, len(texts), len(vectors))
	}
	if p.cfg.EmbedDim > 0 {
		for i, v := range vectors {
			if len(v) != p.cfg.EmbedDim {
				return nil, Usage{}, fmt.Errorf(
					"%s returned %d dimensions for input %d, expected %d",
					p.cfg.Name, len(v), i, p.cfg.EmbedDim)
			}
		}
	}
	// TEI returns vectors in input order; there is no index field to pair on.
	return vectors, Usage{}, nil
}

func (p *teiProvider) Rerank(ctx context.Context, query string, documents []string, topN int) ([]RerankHit, Usage, error) {
	body := map[string]any{"query": query, "texts": documents, "truncate": true}

	var raw []struct {
		Index int     `json:"index"`
		Score float64 `json:"score"`
	}
	if err := p.post(ctx, "/rerank", body, &raw); err != nil {
		return nil, Usage{}, err
	}

	hits := make([]RerankHit, 0, len(raw))
	for _, r := range raw {
		if r.Index < 0 || r.Index >= len(documents) {
			return nil, Usage{}, fmt.Errorf("%s returned index %d, out of range", p.cfg.Name, r.Index)
		}
		hits = append(hits, RerankHit{Index: r.Index, Score: r.Score})
	}
	return hits, Usage{}, nil
}

func (p *teiProvider) post(ctx context.Context, path string, body, into any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}

	res, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s unreachable at %s: %w", p.cfg.Name, p.cfg.BaseURL, err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("%s returned %s with an unreadable body: %w", p.cfg.Name, res.Status, err)
	}
	if res.StatusCode >= 400 {
		return fmt.Errorf("%s returned %s: %s", p.cfg.Name, res.Status, snippet(raw))
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("%s returned unparseable JSON (%s): %w", p.cfg.Name, res.Status, err)
	}
	return nil
}
