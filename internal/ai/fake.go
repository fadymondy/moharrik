package ai

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"sync"
)

/*
Fake is the provider tests use. It makes every behaviour this package promises
assertable without a network: scripted replies, scripted failures, deterministic
vectors, and a call log.

It lives in the package rather than in a _test.go file on purpose — FM-101,
FM-104, FM-105 and FM-107 all need it to test their own pipelines, and a test
helper that only one package can import would be copied instead.
*/
type Fake struct {
	name string

	// Cost marks the fake as a paid provider, so paid-call accounting and the
	// cap can be exercised without spending anything.
	Cost bool

	// GenerateModelName and EmbedModelName appear in provenance.
	GenerateModelName string
	EmbedModelName    string
	RerankModelName   string

	// Dim is the length of the vectors Embed returns.
	Dim int

	// Err, when set, is returned by every call — a provider outage.
	Err error

	// Replies are returned in order; the last one repeats once exhausted. An
	// empty Replies returns a description of the request, which is enough for
	// a test that only cares about routing.
	Replies []string

	mu    sync.Mutex
	calls []FakeCall
	reply int
}

// FakeCall is one recorded call, so a test can assert which provider was asked
// for what — the routing and fallback assertions.
type FakeCall struct {
	Kind    string // "generate" | "embed" | "rerank"
	Task    Task
	System  string
	Message string
	Inputs  []string
}

// NewFake builds a free, always-succeeding fake.
func NewFake(name string) *Fake {
	return &Fake{name: name, GenerateModelName: name + "-model", EmbedModelName: name + "-embed", RerankModelName: name + "-rerank", Dim: 8}
}

func (f *Fake) Name() string { return f.name }
func (f *Fake) Paid() bool   { return f.Cost }

func (f *Fake) GenerateModel(Task) string { return f.GenerateModelName }
func (f *Fake) EmbedModel() string        { return f.EmbedModelName }
func (f *Fake) RerankModel() string       { return f.RerankModelName }

// Calls returns a copy of the call log.
func (f *Fake) Calls() []FakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FakeCall{}, f.calls...)
}

// CallCount is the log's length, the assertion most tests actually want.
func (f *Fake) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *Fake) Generate(ctx context.Context, task Task, req GenerateRequest) (string, Usage, error) {
	f.mu.Lock()
	last := ""
	if len(req.Messages) > 0 {
		last = req.Messages[len(req.Messages)-1].Content
	}
	f.calls = append(f.calls, FakeCall{Kind: "generate", Task: task, System: req.System, Message: last})

	if f.Err != nil {
		f.mu.Unlock()
		return "", Usage{}, fmt.Errorf("%s: %w", f.name, f.Err)
	}
	if len(f.Replies) == 0 {
		f.mu.Unlock()
		return fmt.Sprintf("%s handled %s", f.name, task), Usage{InputTokens: 1, OutputTokens: 1}, nil
	}
	i := f.reply
	if i >= len(f.Replies) {
		i = len(f.Replies) - 1
	}
	f.reply++
	out := f.Replies[i]
	f.mu.Unlock()
	return out, Usage{InputTokens: len(last), OutputTokens: len(out)}, nil
}

func (f *Fake) Embed(ctx context.Context, texts []string) ([][]float32, Usage, error) {
	f.mu.Lock()
	f.calls = append(f.calls, FakeCall{Kind: "embed", Task: TaskEmbed, Inputs: append([]string{}, texts...)})
	err, dim := f.Err, f.Dim
	f.mu.Unlock()
	if err != nil {
		return nil, Usage{}, fmt.Errorf("%s: %w", f.name, err)
	}

	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = deterministicVector(t, dim)
	}
	return out, Usage{InputTokens: len(texts)}, nil
}

func (f *Fake) Rerank(ctx context.Context, query string, documents []string, topN int) ([]RerankHit, Usage, error) {
	f.mu.Lock()
	f.calls = append(f.calls, FakeCall{Kind: "rerank", Task: TaskRerank, Message: query, Inputs: append([]string{}, documents...)})
	err := f.Err
	f.mu.Unlock()
	if err != nil {
		return nil, Usage{}, fmt.Errorf("%s: %w", f.name, err)
	}

	// Scored by shared words: deterministic, explainable in a failing test,
	// and it puts the obviously relevant document first, which is what a
	// reranker assertion is checking.
	terms := strings.Fields(strings.ToLower(query))
	hits := make([]RerankHit, 0, len(documents))
	for i, doc := range documents {
		lower := strings.ToLower(doc)
		score := 0.0
		for _, term := range terms {
			if strings.Contains(lower, term) {
				score++
			}
		}
		hits = append(hits, RerankHit{Index: i, Score: score})
	}
	return hits, Usage{InputTokens: len(documents)}, nil
}

// deterministicVector gives the same text the same unit vector every time, so
// a similarity assertion in a test is stable.
func deterministicVector(text string, dim int) []float32 {
	if dim <= 0 {
		dim = 8
	}
	out := make([]float32, dim)
	var sum float64
	for i := range out {
		h := fnv.New32a()
		fmt.Fprintf(h, "%d:%s", i, text)
		v := float64(h.Sum32()%2000)/1000 - 1 // [-1, 1)
		out[i] = float32(v)
		sum += v * v
	}
	if norm := math.Sqrt(sum); norm > 0 {
		for i := range out {
			out[i] = float32(float64(out[i]) / norm)
		}
	}
	return out
}
