package ai

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// Every test here uses the fake provider. Nothing in this file opens a socket,
// which is the point: CI has no models and must never wait for one.

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// env builds a Getenv from a map, so configuration is exercised without
// os.Setenv and tests can run in parallel.
func env(pairs map[string]string) Getenv {
	return func(k string) string { return pairs[k] }
}

func newTestClient(t *testing.T, vars map[string]string, providers ...Provider) *Client {
	t.Helper()
	cfg, err := loadConfigFrom(env(vars))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	c, err := New(cfg, quietLogger())
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return c.WithProviders(providers...)
}

// --- routing ---------------------------------------------------------------

func TestTaskRoutesToConfiguredProvider(t *testing.T) {
	local := NewFake("qwen-local")
	claude := NewFake("claude")
	claude.Cost = true

	// The acceptance criterion: which provider drafts is configuration.
	c := newTestClient(t, map[string]string{
		"AI_PROVIDERS":                "qwen-local,claude",
		"AI_PROVIDER_QWEN_LOCAL_KIND": "fake",
		"AI_PROVIDER_CLAUDE_KIND":     "fake",
		"AI_TASK_DEFAULT":             "qwen-local",
		"AI_TASK_DRAFT":               "claude",
		"AI_MAX_PAID_CALLS_PER_RUN":   "5",
	}, local, claude)

	if _, err := c.Generate(context.Background(), TaskExtract, GenerateRequest{}); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := c.Generate(context.Background(), TaskDraft, GenerateRequest{}); err != nil {
		t.Fatalf("draft: %v", err)
	}

	if got := local.CallCount(); got != 1 {
		t.Errorf("qwen-local served %d calls, want 1 (the unmapped task via AI_TASK_DEFAULT)", got)
	}
	if got := claude.CallCount(); got != 1 {
		t.Errorf("claude served %d calls, want 1 (the draft task)", got)
	}
	if c.Calls(t, claude, TaskDraft) != 1 {
		t.Errorf("draft did not reach the provider AI_TASK_DRAFT names")
	}
}

// Calls counts a fake's calls for one task; a helper because the assertion
// "the right provider got the right task" recurs.
func (c *Client) Calls(t *testing.T, f *Fake, task Task) int {
	t.Helper()
	n := 0
	for _, call := range f.Calls() {
		if call.Task == task {
			n++
		}
	}
	return n
}

// Switching draft from Claude to a local model must be a config change with no
// code edit and no test edit — so the same body runs twice with one var moved.
func TestSwitchingDraftProviderIsConfigOnly(t *testing.T) {
	for _, draftProvider := range []string{"claude", "qwen-local"} {
		t.Run(draftProvider, func(t *testing.T) {
			local := NewFake("qwen-local")
			claude := NewFake("claude")
			claude.Cost = true

			c := newTestClient(t, map[string]string{
				"AI_PROVIDERS":                "qwen-local,claude",
				"AI_PROVIDER_QWEN_LOCAL_KIND": "fake",
				"AI_PROVIDER_CLAUDE_KIND":     "fake",
				"AI_TASK_DEFAULT":             "qwen-local",
				"AI_TASK_DRAFT":               draftProvider,
				"AI_MAX_PAID_CALLS_PER_RUN":   "5",
			}, local, claude)

			res, err := c.Generate(context.Background(), TaskDraft, GenerateRequest{})
			if err != nil {
				t.Fatalf("draft: %v", err)
			}
			if res.Provenance.Provider != draftProvider {
				t.Errorf("provenance says %q, want %q", res.Provenance.Provider, draftProvider)
			}
		})
	}
}

// --- fallback --------------------------------------------------------------

func TestFallbackOnProviderOutage(t *testing.T) {
	down := NewFake("primary")
	down.Err = errors.New("connection refused")
	up := NewFake("secondary")

	c := newTestClient(t, map[string]string{
		"AI_PROVIDERS":               "primary,secondary",
		"AI_PROVIDER_PRIMARY_KIND":   "fake",
		"AI_PROVIDER_SECONDARY_KIND": "fake",
		"AI_TASK_DEFAULT":            "primary",
		"AI_FALLBACK":                "secondary",
	}, down, up)

	res, err := c.Generate(context.Background(), TaskSummarise, GenerateRequest{})
	if err != nil {
		t.Fatalf("an outage on the primary must not fail the run: %v", err)
	}
	if res.Provenance.Provider != "secondary" {
		t.Errorf("answered by %q, want secondary", res.Provenance.Provider)
	}
	if !res.Provenance.Fallback {
		t.Error("provenance does not record that this was a fallback")
	}
	if len(res.Provenance.FellBackFrom) != 1 || res.Provenance.FellBackFrom[0] != "primary" {
		t.Errorf("FellBackFrom = %v, want [primary]", res.Provenance.FellBackFrom)
	}
	if down.CallCount() != 1 {
		t.Errorf("the primary was called %d times, want 1", down.CallCount())
	}
}

func TestEveryProviderDownIsAnError(t *testing.T) {
	a := NewFake("a")
	a.Err = errors.New("down")
	b := NewFake("b")
	b.Err = errors.New("also down")

	c := newTestClient(t, map[string]string{
		"AI_PROVIDERS":       "a,b",
		"AI_PROVIDER_A_KIND": "fake",
		"AI_PROVIDER_B_KIND": "fake",
		"AI_TASK_DEFAULT":    "a",
		"AI_FALLBACK":        "b",
	}, a, b)

	if _, err := c.Generate(context.Background(), TaskScore, GenerateRequest{}); err == nil {
		t.Fatal("expected an error when every provider is down")
	}
}

// --- structured output: validate, retry, drop ------------------------------

func opportunitySchema() *Schema {
	return &Schema{
		Name: "opportunity",
		Type: TypeObject,
		Properties: map[string]*Schema{
			"company":    {Type: TypeString, MinLength: 1},
			"need":       {Type: TypeString},
			"confidence": {Type: TypeString, Enum: []string{"low", "medium", "high"}},
		},
		Required: []string{"company", "need", "confidence"},
	}
}

func TestValidJSONIsReturnedDecoded(t *testing.T) {
	f := NewFake("local")
	f.Replies = []string{"```json\n{\"company\":\"Acme\",\"need\":\"Laravel\",\"confidence\":\"high\"}\n```"}

	c := newTestClient(t, map[string]string{
		"AI_PROVIDERS":           "local",
		"AI_PROVIDER_LOCAL_KIND": "fake",
		"AI_TASK_DEFAULT":        "local",
	}, f)

	res, err := c.Generate(context.Background(), TaskExtract, GenerateRequest{Schema: opportunitySchema()})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if res.JSON["company"] != "Acme" {
		t.Errorf("company = %v, want Acme (a fenced reply must still parse)", res.JSON["company"])
	}
	if res.Provenance.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", res.Provenance.Attempts)
	}
}

func TestMalformedOutputIsRetriedThenAccepted(t *testing.T) {
	f := NewFake("local")
	f.Replies = []string{
		`{"company":"Acme","need":"Laravel"}`, // confidence missing
		`{"company":"Acme","need":"Laravel","confidence":"high"}`,
	}

	c := newTestClient(t, map[string]string{
		"AI_PROVIDERS":           "local",
		"AI_PROVIDER_LOCAL_KIND": "fake",
		"AI_TASK_DEFAULT":        "local",
		"AI_JSON_RETRIES":        "1",
	}, f)

	res, err := c.Generate(context.Background(), TaskExtract, GenerateRequest{Schema: opportunitySchema()})
	if err != nil {
		t.Fatalf("the retry should have recovered: %v", err)
	}
	if res.Provenance.Attempts != 2 {
		t.Errorf("attempts = %d, want 2", res.Provenance.Attempts)
	}

	// The retry must tell the model what was wrong, or it is just a reroll.
	calls := f.Calls()
	if len(calls) != 2 || !strings.Contains(calls[1].Message, "confidence") {
		t.Errorf("the retry did not feed the validation error back: %+v", calls)
	}
}

func TestMalformedOutputIsDroppedNotStored(t *testing.T) {
	f := NewFake("local")
	f.Replies = []string{`{"company":"Acme","need":"Laravel","confidence":"probably"}`}

	c := newTestClient(t, map[string]string{
		"AI_PROVIDERS":           "local",
		"AI_PROVIDER_LOCAL_KIND": "fake",
		"AI_TASK_DEFAULT":        "local",
		"AI_JSON_RETRIES":        "1",
	}, f)

	res, err := c.Generate(context.Background(), TaskExtract, GenerateRequest{Schema: opportunitySchema()})
	if !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("err = %v, want ErrInvalidOutput", err)
	}
	if res.JSON != nil || res.Text != "" {
		t.Error("a dropped result must carry no payload — it must never be stored as fact")
	}
	if f.CallCount() != 2 {
		t.Errorf("attempts = %d, want 2 (one plus AI_JSON_RETRIES)", f.CallCount())
	}

	var invalid *InvalidOutputError
	if !errors.As(err, &invalid) || invalid.Provider != "local" {
		t.Errorf("the error does not name the provider that failed: %v", err)
	}
}

func TestInvalidOutputDoesNotBurnTheFallback(t *testing.T) {
	bad := NewFake("local")
	bad.Replies = []string{`{"company":"Acme"}`}
	paid := NewFake("claude")
	paid.Cost = true

	c := newTestClient(t, map[string]string{
		"AI_PROVIDERS":            "local,claude",
		"AI_PROVIDER_LOCAL_KIND":  "fake",
		"AI_PROVIDER_CLAUDE_KIND": "fake",
		"AI_TASK_DEFAULT":         "local",
		"AI_FALLBACK":             "claude",
		"AI_JSON_RETRIES":         "0",
	}, bad, paid)

	if _, err := c.Generate(context.Background(), TaskExtract, GenerateRequest{Schema: opportunitySchema()}); !errors.Is(err, ErrInvalidOutput) {
		t.Fatalf("err = %v, want ErrInvalidOutput", err)
	}
	if paid.CallCount() != 0 {
		t.Error("a schema failure is an answer, not an outage — it must not spend the paid fallback")
	}
}

// --- paid-call accounting --------------------------------------------------

func TestPaidCallsAreCountedAndAttributable(t *testing.T) {
	local := NewFake("qwen-local")
	paid := NewFake("claude")
	paid.Cost = true

	c := newTestClient(t, map[string]string{
		"AI_PROVIDERS":                "qwen-local,claude",
		"AI_PROVIDER_QWEN_LOCAL_KIND": "fake",
		"AI_PROVIDER_CLAUDE_KIND":     "fake",
		"AI_TASK_DEFAULT":             "qwen-local",
		"AI_TASK_DRAFT":               "claude",
		"AI_MAX_PAID_CALLS_PER_RUN":   "10",
	}, local, paid)

	run := NewRun("run-42", 10)
	ctx := WithRun(context.Background(), run)

	for range 3 {
		if _, err := c.Generate(ctx, TaskClassify, GenerateRequest{}); err != nil {
			t.Fatalf("classify: %v", err)
		}
	}
	if _, err := c.Generate(ctx, TaskDraft, GenerateRequest{}); err != nil {
		t.Fatalf("draft: %v", err)
	}

	stats := run.Stats()
	if stats.PaidCalls != 1 {
		t.Errorf("paid calls = %d, want 1", stats.PaidCalls)
	}
	if stats.FreeCalls != 3 {
		t.Errorf("free calls = %d, want 3", stats.FreeCalls)
	}
	if stats.ByProvider["claude"] != 1 || stats.ByProvider["qwen-local"] != 3 {
		t.Errorf("spend is not attributable per provider: %+v", stats.ByProvider)
	}
	if stats.RunID != "run-42" {
		t.Errorf("run id = %q, want run-42", stats.RunID)
	}
}

func TestPaidCapIsEnforced(t *testing.T) {
	paid := NewFake("claude")
	paid.Cost = true

	c := newTestClient(t, map[string]string{
		"AI_PROVIDERS":            "claude",
		"AI_PROVIDER_CLAUDE_KIND": "fake",
		"AI_TASK_DEFAULT":         "claude",
	}, paid)

	run := NewRun("capped", 1)
	ctx := WithRun(context.Background(), run)

	if _, err := c.Generate(ctx, TaskDraft, GenerateRequest{}); err != nil {
		t.Fatalf("the first paid call is within the cap: %v", err)
	}
	if _, err := c.Generate(ctx, TaskDraft, GenerateRequest{}); !errors.Is(err, ErrPaidCapExceeded) {
		t.Fatalf("err = %v, want ErrPaidCapExceeded", err)
	}
	if paid.CallCount() != 1 {
		t.Errorf("the capped call still reached the provider (%d calls)", paid.CallCount())
	}
}

// The local-only path: a run with cap 0 must complete every task and spend
// nothing. This is the epic's "a full run costs nothing" criterion in miniature.
func TestLocalOnlyRunSpendsNothing(t *testing.T) {
	local := NewFake("qwen-local")
	tei := NewFake("tei-local")

	c := newTestClient(t, map[string]string{
		"AI_PROVIDERS":                "qwen-local,tei-local",
		"AI_PROVIDER_QWEN_LOCAL_KIND": "fake",
		"AI_PROVIDER_TEI_LOCAL_KIND":  "fake",
		"AI_TASK_DEFAULT":             "qwen-local",
		"AI_TASK_EMBED":               "tei-local",
		"AI_TASK_RERANK":              "tei-local",
	}, local, tei)

	run := NewRun("free-run", 0) // no paid calls permitted at all
	ctx := WithRun(context.Background(), run)

	for _, task := range GenerationTasks {
		if _, err := c.Generate(ctx, task, GenerateRequest{}); err != nil {
			t.Fatalf("%s: %v", task, err)
		}
	}
	if _, err := c.Embed(ctx, []string{"a company hiring Laravel engineers"}); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if _, err := c.Rerank(ctx, "laravel", []string{"a python shop", "laravel consultancy"}, 0); err != nil {
		t.Fatalf("rerank: %v", err)
	}

	if paid := run.PaidCalls(); paid != 0 {
		t.Errorf("a local-only run made %d paid calls, want 0", paid)
	}
}

// --- no provider configured ------------------------------------------------

func TestNoProviderConfiguredIsTypedNotEmpty(t *testing.T) {
	c := newTestClient(t, map[string]string{})

	res, err := c.Generate(context.Background(), TaskDraft, GenerateRequest{})
	if !errors.Is(err, ErrNoProvider) {
		t.Fatalf("err = %v, want ErrNoProvider", err)
	}
	if res.Text != "" {
		t.Error("an unconfigured layer must not return text")
	}

	var np *NoProviderError
	if !errors.As(err, &np) || np.Task != TaskDraft {
		t.Errorf("the error does not name the task: %v", err)
	}
	if !strings.Contains(err.Error(), "AI_PROVIDERS") {
		t.Errorf("the error should say what to set: %v", err)
	}

	if _, err := c.Embed(context.Background(), []string{"x"}); !errors.Is(err, ErrNoProvider) {
		t.Errorf("embed err = %v, want ErrNoProvider", err)
	}
	if _, err := c.Rerank(context.Background(), "q", []string{"d"}, 0); !errors.Is(err, ErrNoProvider) {
		t.Errorf("rerank err = %v, want ErrNoProvider", err)
	}
	if _, ok := c.ProviderFor(TaskDraft); ok {
		t.Error("ProviderFor claims a provider exists when none does")
	}
}

// A provider that cannot serve a task shape must be reported, not silently
// treated as an outage that empties the chain.
func TestProviderThatCannotServeTheTask(t *testing.T) {
	only := &rerankOnly{NewFake("tei-local")}
	c := newTestClient(t, map[string]string{
		"AI_PROVIDERS":               "tei-local",
		"AI_PROVIDER_TEI_LOCAL_KIND": "fake",
		"AI_TASK_DEFAULT":            "tei-local",
	}, only)

	if _, err := c.Generate(context.Background(), TaskDraft, GenerateRequest{}); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("err = %v, want ErrNoProvider (nothing in the chain can generate)", err)
	}
}

// rerankOnly hides the fake's Generate method, modelling TEI: it can rerank
// and embed, and can never write a sentence.
type rerankOnly struct{ f *Fake }

func (r *rerankOnly) Name() string        { return r.f.Name() }
func (r *rerankOnly) Paid() bool          { return r.f.Paid() }
func (r *rerankOnly) RerankModel() string { return r.f.RerankModel() }
func (r *rerankOnly) Rerank(ctx context.Context, q string, docs []string, n int) ([]RerankHit, Usage, error) {
	return r.f.Rerank(ctx, q, docs, n)
}

// --- embed and rerank ------------------------------------------------------

func TestEmbedRecordsProvenanceAndDimensions(t *testing.T) {
	tei := NewFake("tei-local")
	tei.EmbedModelName = "BAAI/bge-m3"
	tei.Dim = 16

	c := newTestClient(t, map[string]string{
		"AI_PROVIDERS":               "tei-local",
		"AI_PROVIDER_TEI_LOCAL_KIND": "fake",
		"AI_TASK_EMBED":              "tei-local",
	}, tei)

	res, err := c.Embed(context.Background(), []string{"one", "two"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(res.Vectors) != 2 || res.Dimensions != 16 {
		t.Fatalf("got %d vectors of %d dims, want 2 of 16", len(res.Vectors), res.Dimensions)
	}
	if res.Provenance.Model != "BAAI/bge-m3" || res.Provenance.Provider != "tei-local" {
		t.Errorf("provenance = %s/%s, want tei-local/BAAI/bge-m3", res.Provenance.Provider, res.Provenance.Model)
	}
}

func TestRerankOrdersBestFirstAndTrims(t *testing.T) {
	tei := NewFake("tei-local")
	c := newTestClient(t, map[string]string{
		"AI_PROVIDERS":               "tei-local",
		"AI_PROVIDER_TEI_LOCAL_KIND": "fake",
		"AI_TASK_RERANK":             "tei-local",
	}, tei)

	docs := []string{"a python agency", "a laravel consultancy hiring", "a design studio"}
	res, err := c.Rerank(context.Background(), "laravel hiring", docs, 2)
	if err != nil {
		t.Fatalf("rerank: %v", err)
	}
	if len(res.Hits) != 2 {
		t.Fatalf("got %d hits, want topN=2", len(res.Hits))
	}
	if res.Hits[0].Index != 1 {
		t.Errorf("best hit is index %d, want 1", res.Hits[0].Index)
	}
}

// --- configuration ---------------------------------------------------------

func TestConfigRejectsTaskPointingAtUnknownProvider(t *testing.T) {
	_, err := loadConfigFrom(env(map[string]string{
		"AI_PROVIDERS":           "local",
		"AI_PROVIDER_LOCAL_KIND": "fake",
		"AI_TASK_DRAFT":          "claude",
	}))
	if err == nil || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("err = %v, want a complaint naming claude", err)
	}
}

func TestConfigRejectsProviderWithoutKind(t *testing.T) {
	if _, err := loadConfigFrom(env(map[string]string{"AI_PROVIDERS": "mystery"})); err == nil {
		t.Fatal("a provider with no KIND must be a startup error, not a runtime surprise")
	}
}

func TestChainOrder(t *testing.T) {
	cfg, err := loadConfigFrom(env(map[string]string{
		"AI_PROVIDERS":       "a,b,c",
		"AI_PROVIDER_A_KIND": "fake",
		"AI_PROVIDER_B_KIND": "fake",
		"AI_PROVIDER_C_KIND": "fake",
		"AI_TASK_DEFAULT":    "a",
		"AI_TASK_DRAFT":      "b",
		"AI_FALLBACK":        "a,c",
		"AI_FALLBACK_DRAFT":  "c,a",
	}))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if got := strings.Join(cfg.Chain(TaskDraft), ","); got != "b,c,a" {
		t.Errorf("draft chain = %q, want b,c,a", got)
	}
	// "a" is both the default and first in the global fallback; it must not be
	// asked twice for one call.
	if got := strings.Join(cfg.Chain(TaskSummarise), ","); got != "a,c" {
		t.Errorf("summarise chain = %q, want a,c", got)
	}
}

func TestLegacyEmbeddingURLIsBridged(t *testing.T) {
	cfg, err := loadConfigFrom(env(map[string]string{
		"EMBEDDING_URL":   "http://127.0.0.1:1234/v1",
		"EMBEDDING_MODEL": "nomic",
	}))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if len(cfg.Providers) != 1 || cfg.Providers[0].Name != "embedding-url" {
		t.Fatalf("FM-26's embedder was not bridged: %+v", cfg.Providers)
	}
	if cfg.Chain(TaskEmbed)[0] != "embedding-url" {
		t.Errorf("embed does not route to the bridged provider: %v", cfg.Chain(TaskEmbed))
	}
	if cfg.Chain(TaskDraft) != nil {
		t.Errorf("the bridge must not claim to serve generation: %v", cfg.Chain(TaskDraft))
	}
}

func TestEnvToken(t *testing.T) {
	if got := envToken("qwen-local"); got != "QWEN_LOCAL" {
		t.Errorf("envToken = %q, want QWEN_LOCAL", got)
	}
}
