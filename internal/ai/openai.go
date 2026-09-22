package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// newProvider builds a provider from its configuration. This is the only
// switch in the package: adding a provider means a Kind constant, a case here,
// and the file implementing the interface.
func newProvider(pc ProviderConfig) (Provider, error) {
	switch pc.Kind {
	case KindOpenAICompatible:
		return newOpenAIProvider(pc), nil
	case KindTEI:
		return newTEIProvider(pc), nil
	case KindAnthropic:
		return newAnthropicProvider(pc), nil
	case KindFake:
		return NewFake(pc.Name), nil
	default:
		return nil, fmt.Errorf("ai: provider %q has unknown kind %q", pc.Name, pc.Kind)
	}
}

/*
openAIProvider speaks the OpenAI HTTP API, which is the lingua franca of local
runtimes and hosted vendors alike: LM Studio, vLLM, llama-server, Ollama's
OpenAI endpoint, DashScope's compatible mode, OpenAI itself.

One implementation therefore covers "any OpenAI-compatible endpoint" from the
issue. What it adds over internal/brain/embed.go is an Authorization header —
without which only an unauthenticated local endpoint is reachable.
*/
type openAIProvider struct {
	cfg    ProviderConfig
	client *http.Client
}

func newOpenAIProvider(pc ProviderConfig) *openAIProvider {
	return &openAIProvider{cfg: pc, client: &http.Client{Timeout: pc.Timeout}}
}

func (p *openAIProvider) Name() string { return p.cfg.Name }
func (p *openAIProvider) Paid() bool   { return p.cfg.Paid }

func (p *openAIProvider) GenerateModel(Task) string { return p.cfg.Model }

func (p *openAIProvider) EmbedModel() string {
	if p.cfg.EmbedModel != "" {
		return p.cfg.EmbedModel
	}
	return p.cfg.Model
}

type oaChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	Stream      bool      `json:"stream"`
}

type oaChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (p *openAIProvider) Generate(ctx context.Context, task Task, req GenerateRequest) (string, Usage, error) {
	messages := make([]Message, 0, len(req.Messages)+1)
	if s := strings.TrimSpace(req.System); s != "" {
		messages = append(messages, Message{Role: "system", Content: s})
	}
	messages = append(messages, req.Messages...)

	body := oaChatRequest{
		Model:       p.cfg.Model,
		Messages:    messages,
		MaxTokens:   firstPositive(req.MaxTokens, p.cfg.MaxTokens),
		Temperature: firstNonZero(req.Temperature, p.cfg.Temperature),
	}

	var decoded oaChatResponse
	if err := p.post(ctx, "/chat/completions", body, &decoded); err != nil {
		return "", Usage{}, err
	}
	if decoded.Error != nil {
		return "", Usage{}, fmt.Errorf("%s refused: %s", p.cfg.Name, decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 {
		return "", Usage{}, fmt.Errorf("%s returned no choices for task %q", p.cfg.Name, task)
	}
	usage := Usage{
		InputTokens:  decoded.Usage.PromptTokens,
		OutputTokens: decoded.Usage.CompletionTokens,
	}
	return decoded.Choices[0].Message.Content, usage, nil
}

type oaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type oaEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (p *openAIProvider) Embed(ctx context.Context, texts []string) ([][]float32, Usage, error) {
	var decoded oaEmbedResponse
	err := p.post(ctx, "/embeddings", oaEmbedRequest{Model: p.EmbedModel(), Input: texts}, &decoded)
	if err != nil {
		return nil, Usage{}, err
	}
	if decoded.Error != nil {
		return nil, Usage{}, fmt.Errorf("%s refused: %s", p.cfg.Name, decoded.Error.Message)
	}
	if len(decoded.Data) != len(texts) {
		return nil, Usage{}, fmt.Errorf("%s: asked for %d embeddings, got %d", p.cfg.Name, len(texts), len(decoded.Data))
	}

	// Paired by the documented `index` field, not by position: returning
	// results out of order is permitted, and pairing by position would attach
	// the wrong vector to the wrong document. (Same rule as internal/brain.)
	out := make([][]float32, len(texts))
	for _, item := range decoded.Data {
		if item.Index < 0 || item.Index >= len(out) {
			return nil, Usage{}, fmt.Errorf("%s returned index %d, out of range", p.cfg.Name, item.Index)
		}
		if p.cfg.EmbedDim > 0 && len(item.Embedding) != p.cfg.EmbedDim {
			return nil, Usage{}, fmt.Errorf(
				"%s returned %d dimensions, expected %d — check the model and AI_PROVIDER_%s_EMBED_DIM",
				p.cfg.Name, len(item.Embedding), p.cfg.EmbedDim, envToken(p.cfg.Name))
		}
		out[item.Index] = item.Embedding
	}
	return out, Usage{InputTokens: decoded.Usage.PromptTokens}, nil
}

func (p *openAIProvider) post(ctx context.Context, path string, body, into any) error {
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
		// By far the common case is that nothing is listening. Say that
		// plainly; the fallback chain reads this message in its log line.
		return fmt.Errorf("%s unreachable at %s: %w", p.cfg.Name, p.cfg.BaseURL, err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
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

func snippet(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}

func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func firstNonZero(values ...float64) float64 {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}
