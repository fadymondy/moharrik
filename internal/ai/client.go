package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

/*
Client is the only thing the pipeline touches.

It owns routing (task -> provider), the fallback chain, structured-output
validation with retry then drop, and the paid-call accounting. Providers below
it stay dumb: one HTTP call each, no policy.
*/
type Client struct {
	cfg       Config
	providers map[string]Provider
	log       *slog.Logger

	// The client-level ledger catches calls made outside any run, so spend is
	// never unattributed — it is attributed to "unattributed", visibly.
	mu      sync.Mutex
	ledger  *Run
	nowFunc func() time.Time
}

// New builds a Client from configuration. A configuration with no providers is
// valid: every call then returns ErrNoProvider, which is the honest state of
// production today.
func New(cfg Config, log *slog.Logger) (*Client, error) {
	if log == nil {
		log = slog.Default()
	}
	c := &Client{
		cfg:       cfg,
		providers: map[string]Provider{},
		log:       log.With("component", "ai"),
		ledger:    NewRun("unattributed", cfg.PaidCap),
		nowFunc:   time.Now,
	}
	for _, pc := range cfg.Providers {
		p, err := newProvider(pc)
		if err != nil {
			return nil, err
		}
		c.providers[pc.Name] = p
	}
	return c, nil
}

// NewFromEnv is the constructor the server uses.
func NewFromEnv(log *slog.Logger) (*Client, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	return New(cfg, log)
}

// WithProviders replaces the provider set. Tests use it to inject fakes; it is
// also how a caller can register a provider this package does not know about
// without editing this package.
func (c *Client) WithProviders(providers ...Provider) *Client {
	for _, p := range providers {
		c.providers[p.Name()] = p
	}
	return c
}

// Config returns the loaded configuration, for the admin/status surface.
func (c *Client) Config() Config { return c.cfg }

// ProviderFor reports which provider name serves a task right now, and whether
// one is configured at all. The UI uses it to say what is wired up.
func (c *Client) ProviderFor(task Task) (string, bool) {
	for _, name := range c.cfg.Chain(task) {
		if _, ok := c.providers[name]; ok {
			return name, true
		}
	}
	return "", false
}

// Ledger is the spend recorded outside any explicit run.
func (c *Client) Ledger() *Run { return c.ledger }

// ---------------------------------------------------------------------------
// Runs: paid-call accounting, per pipeline run
// ---------------------------------------------------------------------------

// Run counts and caps the calls made by one pipeline run, so paid spend is
// attributable to the run (and through it to an opportunity) and a runaway
// loop cannot quietly spend money.
type Run struct {
	ID  string
	Cap int // paid calls allowed; 0 = none, <0 = unlimited

	mu        sync.Mutex
	paid      int
	free      int
	byProider map[string]int
	tokensIn  int
	tokensOut int
}

// NewRun starts a ledger. cap < 0 is unlimited; cap == 0 forbids paid calls
// entirely, which is the local-only path the epic requires to exist.
func NewRun(id string, cap int) *Run {
	return &Run{ID: id, Cap: cap, byProider: map[string]int{}}
}

type runKey struct{}

// WithRun attaches a run to a context. Everything the pipeline does under that
// context is counted against it.
func WithRun(ctx context.Context, run *Run) context.Context {
	return context.WithValue(ctx, runKey{}, run)
}

// RunFrom returns the run on a context, if any.
func RunFrom(ctx context.Context) (*Run, bool) {
	run, ok := ctx.Value(runKey{}).(*Run)
	return run, ok && run != nil
}

// Reserve takes one slot outside a client call — for a caller (or a test)
// that accounts for work done elsewhere. Same cap rules as a real call.
func (r *Run) Reserve(provider string, paid bool) error { return r.reserve(provider, paid) }

// reserve takes one slot for a call. It fails closed on the cap.
func (r *Run) reserve(provider string, paid bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if paid {
		if r.Cap == 0 {
			return fmt.Errorf("%w: run %q allows no paid calls", ErrPaidCapExceeded, r.ID)
		}
		if r.Cap > 0 && r.paid >= r.Cap {
			return fmt.Errorf("%w: run %q has used its %d paid calls", ErrPaidCapExceeded, r.ID, r.Cap)
		}
		r.paid++
	} else {
		r.free++
	}
	r.byProider[provider]++
	return nil
}

func (r *Run) record(u Usage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokensIn += u.InputTokens
	r.tokensOut += u.OutputTokens
}

// Stats is a snapshot of a run's spend, safe to persist next to the run row.
type Stats struct {
	RunID        string         `json:"run_id"`
	PaidCalls    int            `json:"paid_calls"`
	FreeCalls    int            `json:"free_calls"`
	Cap          int            `json:"cap"`
	ByProvider   map[string]int `json:"by_provider"`
	InputTokens  int            `json:"input_tokens"`
	OutputTokens int            `json:"output_tokens"`
}

// Stats snapshots the ledger.
func (r *Run) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	by := make(map[string]int, len(r.byProider))
	for k, v := range r.byProider {
		by[k] = v
	}
	return Stats{
		RunID: r.ID, PaidCalls: r.paid, FreeCalls: r.free, Cap: r.Cap,
		ByProvider: by, InputTokens: r.tokensIn, OutputTokens: r.tokensOut,
	}
}

// PaidCalls is the count on its own, for a quick assertion or a log line.
func (r *Run) PaidCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paid
}

func (c *Client) runFor(ctx context.Context) *Run {
	if run, ok := RunFrom(ctx); ok {
		return run
	}
	return c.ledger
}

// ---------------------------------------------------------------------------
// The three call paths
// ---------------------------------------------------------------------------

// attempt is one provider's turn in the chain, shared by all three call paths
// so routing, fallback, accounting and provenance behave identically.
type attempt struct {
	provider Provider
	name     string
}

func (c *Client) chain(task Task) []attempt {
	var out []attempt
	for _, name := range c.cfg.Chain(task) {
		if p, ok := c.providers[name]; ok {
			out = append(out, attempt{provider: p, name: name})
		} else {
			c.log.Warn("configured provider is not built", "task", task, "provider", name)
		}
	}
	return out
}

func (c *Client) noProvider(task Task) error {
	reason := "set AI_TASK_" + upper(string(task)) + " or AI_TASK_DEFAULT"
	if len(c.providers) == 0 {
		reason = "no providers are defined in AI_PROVIDERS"
	}
	return &NoProviderError{Task: task, Reason: reason}
}

/*
Generate runs a generation-shaped task.

The chain is walked in order. A provider that errors is logged and the next one
is tried; the provenance records that a fallback answered and who was asked
first. When the request carries a schema, the reply is parsed and validated,
and a failure is retried (AI_JSON_RETRIES) with the validation error fed back
before the result is dropped — malformed output is never returned as if it were
a fact.
*/
func (c *Client) Generate(ctx context.Context, task Task, req GenerateRequest) (GenerateResult, error) {
	if !task.IsGeneration() {
		return GenerateResult{}, fmt.Errorf("ai: %q is not a generation task", task)
	}

	chain := c.chain(task)
	if len(chain) == 0 {
		return GenerateResult{}, c.noProvider(task)
	}

	run := c.runFor(ctx)
	var (
		tried    []string
		lastErr  error
		anyUsed  bool
		attempts = c.cfg.JSONRetries + 1
	)

	for i, a := range chain {
		gen, ok := a.provider.(Generator)
		if !ok {
			lastErr = fmt.Errorf("%w: %s cannot generate", ErrUnsupported, a.name)
			c.log.Warn("provider cannot serve task", "task", task, "provider", a.name)
			tried = append(tried, a.name)
			continue
		}
		anyUsed = true

		result, err := c.generateWith(ctx, run, task, gen, req, attempts, i > 0, tried)
		if err == nil {
			return result, nil
		}
		lastErr = err

		// A dropped structured output is the model's answer, not an outage.
		// Falling back would spend another provider on a prompt this one
		// already understood; the caller decides whether to ask again.
		if errors.Is(err, ErrInvalidOutput) || errors.Is(err, ErrPaidCapExceeded) {
			return GenerateResult{}, err
		}

		tried = append(tried, a.name)
		c.log.Warn("ai provider failed, falling back",
			"task", task, "provider", a.name, "error", err,
			"next", nextName(chain, i))
	}

	if !anyUsed {
		return GenerateResult{}, c.noProvider(task)
	}
	return GenerateResult{}, fmt.Errorf("ai: every provider for task %q failed: %w", task, lastErr)
}

func (c *Client) generateWith(
	ctx context.Context, run *Run, task Task, gen Generator,
	req GenerateRequest, maxAttempts int, isFallback bool, tried []string,
) (GenerateResult, error) {
	model := gen.GenerateModel(task)
	started := c.nowFunc()

	// The schema instruction is appended once, not per attempt, so a retry
	// does not stack three copies of it into the system prompt.
	call := req
	if req.Schema != nil {
		call.System = joinPrompt(req.System, req.Schema.Prompt())
	}

	var (
		lastValidation error
		totalUsage     Usage
	)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := run.reserve(gen.Name(), gen.Paid()); err != nil {
			return GenerateResult{}, err
		}

		text, usage, err := gen.Generate(ctx, task, call)
		totalUsage.InputTokens += usage.InputTokens
		totalUsage.OutputTokens += usage.OutputTokens
		run.record(usage)
		if err != nil {
			return GenerateResult{}, err // an outage: let the caller fall back
		}

		prov := Provenance{
			Task: task, Provider: gen.Name(), Model: model, Attempts: attempt,
			Fallback: isFallback, FellBackFrom: tried, Paid: gen.Paid(),
			Duration:     c.nowFunc().Sub(started),
			InputTokens:  totalUsage.InputTokens,
			OutputTokens: totalUsage.OutputTokens,
		}

		if req.Schema == nil {
			return GenerateResult{Text: text, Provenance: prov}, nil
		}

		decoded, err := extractJSON(text)
		if err == nil {
			err = req.Schema.Validate(decoded)
		}
		if err == nil {
			return GenerateResult{Text: text, JSON: decoded, Provenance: prov}, nil
		}

		lastValidation = err
		c.log.Warn("ai output failed schema validation",
			"task", task, "provider", gen.Name(), "model", model,
			"attempt", attempt, "of", maxAttempts, "error", err)

		// Feed the failure back: a model corrects a named path far more often
		// than it corrects "that was wrong".
		call.Messages = append(append([]Message{}, call.Messages...),
			Message{Role: "assistant", Content: text},
			Message{Role: "user", Content: "That response was rejected: " + err.Error() +
				". Return only the corrected JSON object."},
		)
	}

	return GenerateResult{}, &InvalidOutputError{
		Task: task, Provider: gen.Name(), Model: model,
		Attempts: maxAttempts, Last: lastValidation,
	}
}

// Embed runs the embed task through the configured provider chain.
func (c *Client) Embed(ctx context.Context, texts []string) (EmbedResult, error) {
	if len(texts) == 0 {
		return EmbedResult{}, nil
	}
	chain := c.chain(TaskEmbed)
	if len(chain) == 0 {
		return EmbedResult{}, c.noProvider(TaskEmbed)
	}

	run := c.runFor(ctx)
	var tried []string
	var lastErr error
	used := false

	for i, a := range chain {
		emb, ok := a.provider.(Embedder)
		if !ok {
			lastErr = fmt.Errorf("%w: %s cannot embed", ErrUnsupported, a.name)
			tried = append(tried, a.name)
			continue
		}
		used = true

		if err := run.reserve(a.name, emb.Paid()); err != nil {
			return EmbedResult{}, err
		}
		started := c.nowFunc()
		vectors, usage, err := emb.Embed(ctx, texts)
		run.record(usage)
		if err != nil {
			lastErr = err
			tried = append(tried, a.name)
			c.log.Warn("ai embedder failed, falling back",
				"provider", a.name, "error", err, "next", nextName(chain, i))
			continue
		}
		if len(vectors) != len(texts) {
			lastErr = fmt.Errorf("asked for %d embeddings, got %d", len(texts), len(vectors))
			tried = append(tried, a.name)
			continue
		}
		dim := 0
		if len(vectors) > 0 {
			dim = len(vectors[0])
		}
		return EmbedResult{
			Vectors: vectors, Dimensions: dim,
			Provenance: Provenance{
				Task: TaskEmbed, Provider: a.name, Model: emb.EmbedModel(), Attempts: 1,
				Fallback: i > 0, FellBackFrom: tried, Paid: emb.Paid(),
				Duration: c.nowFunc().Sub(started), InputTokens: usage.InputTokens,
			},
		}, nil
	}

	if !used {
		return EmbedResult{}, c.noProvider(TaskEmbed)
	}
	return EmbedResult{}, fmt.Errorf("ai: every embedder failed: %w", lastErr)
}

// EmbedOne is the single-input case, which is most of them.
func (c *Client) EmbedOne(ctx context.Context, text string) ([]float32, Provenance, error) {
	res, err := c.Embed(ctx, []string{text})
	if err != nil {
		return nil, Provenance{}, err
	}
	if len(res.Vectors) == 0 {
		return nil, res.Provenance, fmt.Errorf("ai: embedder returned nothing")
	}
	return res.Vectors[0], res.Provenance, nil
}

// Rerank scores documents against a query. topN <= 0 returns all of them.
func (c *Client) Rerank(ctx context.Context, query string, documents []string, topN int) (RerankResult, error) {
	if len(documents) == 0 {
		return RerankResult{}, nil
	}
	chain := c.chain(TaskRerank)
	if len(chain) == 0 {
		return RerankResult{}, c.noProvider(TaskRerank)
	}

	run := c.runFor(ctx)
	var tried []string
	var lastErr error
	used := false

	for i, a := range chain {
		rr, ok := a.provider.(Reranker)
		if !ok {
			lastErr = fmt.Errorf("%w: %s cannot rerank", ErrUnsupported, a.name)
			tried = append(tried, a.name)
			continue
		}
		used = true

		if err := run.reserve(a.name, rr.Paid()); err != nil {
			return RerankResult{}, err
		}
		started := c.nowFunc()
		hits, usage, err := rr.Rerank(ctx, query, documents, topN)
		run.record(usage)
		if err != nil {
			lastErr = err
			tried = append(tried, a.name)
			c.log.Warn("ai reranker failed, falling back",
				"provider", a.name, "error", err, "next", nextName(chain, i))
			continue
		}
		sort.SliceStable(hits, func(x, y int) bool { return hits[x].Score > hits[y].Score })
		if topN > 0 && len(hits) > topN {
			hits = hits[:topN]
		}
		return RerankResult{
			Hits: hits,
			Provenance: Provenance{
				Task: TaskRerank, Provider: a.name, Model: rr.RerankModel(), Attempts: 1,
				Fallback: i > 0, FellBackFrom: tried, Paid: rr.Paid(),
				Duration: c.nowFunc().Sub(started), InputTokens: usage.InputTokens,
			},
		}, nil
	}

	if !used {
		return RerankResult{}, c.noProvider(TaskRerank)
	}
	return RerankResult{}, fmt.Errorf("ai: every reranker failed: %w", lastErr)
}

func nextName(chain []attempt, i int) string {
	if i+1 < len(chain) {
		return chain[i+1].name
	}
	return "(none)"
}

func joinPrompt(parts ...string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += "\n\n"
		}
		out += p
	}
	return out
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}
