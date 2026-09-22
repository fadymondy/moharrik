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

/*
anthropicProvider is the one provider that is not OpenAI-shaped: the system
prompt is a top-level field, max_tokens is mandatory, and content comes back as
a list of blocks.

Written against the HTTP API with net/http, not the SDK — the issue's "no
provider SDK leaks" rule, and a dependency this repo does not need for what is
one POST.
*/
const (
	anthropicDefaultBaseURL = "https://api.anthropic.com/v1"
	anthropicVersion        = "2023-06-01"
	anthropicDefaultTokens  = 2048
)

type anthropicProvider struct {
	cfg    ProviderConfig
	client *http.Client
}

func newAnthropicProvider(pc ProviderConfig) *anthropicProvider {
	if pc.BaseURL == "" {
		pc.BaseURL = anthropicDefaultBaseURL
	}
	return &anthropicProvider{cfg: pc, client: &http.Client{Timeout: pc.Timeout}}
}

func (p *anthropicProvider) Name() string { return p.cfg.Name }

// Paid defaults to true here even when config forgot to say so: a call to
// api.anthropic.com costs money, and the accounting must not under-count
// because of a missing env var.
func (p *anthropicProvider) Paid() bool {
	return p.cfg.Paid || strings.Contains(p.cfg.BaseURL, "api.anthropic.com")
}

func (p *anthropicProvider) GenerateModel(Task) string { return p.cfg.Model }

type anthropicRequest struct {
	Model       string    `json:"model"`
	System      string    `json:"system,omitempty"`
	Messages    []Message `json:"messages"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float64   `json:"temperature,omitempty"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (p *anthropicProvider) Generate(ctx context.Context, task Task, req GenerateRequest) (string, Usage, error) {
	if p.cfg.APIKey == "" {
		return "", Usage{}, fmt.Errorf("ai: provider %q has no API key (set AI_PROVIDER_%s_API_KEY_ENV to the variable holding it)",
			p.cfg.Name, envToken(p.cfg.Name))
	}

	// Anthropic rejects a conversation that does not start with a user turn,
	// and rejects an absent max_tokens. Both are fixed here rather than in
	// every caller.
	messages := make([]Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			req.System = joinPrompt(req.System, m.Content)
			continue
		}
		messages = append(messages, m)
	}
	if len(messages) == 0 || messages[0].Role != "user" {
		return "", Usage{}, fmt.Errorf("ai: %s needs a user message for task %q", p.cfg.Name, task)
	}

	body := anthropicRequest{
		Model:       p.cfg.Model,
		System:      req.System,
		Messages:    messages,
		MaxTokens:   firstPositive(req.MaxTokens, p.cfg.MaxTokens, anthropicDefaultTokens),
		Temperature: firstNonZero(req.Temperature, p.cfg.Temperature),
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", Usage{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.BaseURL+"/messages", bytes.NewReader(payload))
	if err != nil {
		return "", Usage{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.cfg.APIKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	res, err := p.client.Do(httpReq)
	if err != nil {
		return "", Usage{}, fmt.Errorf("%s unreachable at %s: %w", p.cfg.Name, p.cfg.BaseURL, err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return "", Usage{}, fmt.Errorf("%s returned %s with an unreadable body: %w", p.cfg.Name, res.Status, err)
	}

	var decoded anthropicResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", Usage{}, fmt.Errorf("%s returned unparseable JSON (%s): %w", p.cfg.Name, res.Status, err)
	}
	if decoded.Error != nil {
		return "", Usage{}, fmt.Errorf("%s refused (%s): %s", p.cfg.Name, decoded.Error.Type, decoded.Error.Message)
	}
	if res.StatusCode >= 400 {
		return "", Usage{}, fmt.Errorf("%s returned %s: %s", p.cfg.Name, res.Status, snippet(raw))
	}

	var text strings.Builder
	for _, block := range decoded.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	usage := Usage{InputTokens: decoded.Usage.InputTokens, OutputTokens: decoded.Usage.OutputTokens}
	return text.String(), usage, nil
}
