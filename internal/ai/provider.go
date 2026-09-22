package ai

import "context"

/*
Providers implement one or more of three narrow interfaces.

They are deliberately separate: TEI can embed and rerank but cannot write a
sentence, and a chat endpoint can draft but has no reranker. A single fat
interface would force every provider to stub methods it can never serve, and
the Client would have no way to tell "not implemented" from "broken".

The Client type-asserts, so a new provider implements only what it can do.
*/

// Provider is what every provider has in common: a configured name, used in
// task routing, in logs, and in the provenance stored beside a result.
type Provider interface {
	// Name is the configured provider name, e.g. "qwen-local".
	Name() string

	// Paid reports whether calls to this provider cost money. The Client uses
	// it to count and cap spend per run, and to keep the local-only path free.
	Paid() bool
}

// Generator serves the generation-shaped tasks.
//
// Implementations must NOT validate the schema themselves — they may use it as
// a provider-native structured-output hint, but validation, retry and drop are
// the Client's job so every provider behaves identically.
type Generator interface {
	Provider

	// GenerateModel is the model name for this task, recorded as provenance.
	GenerateModel(task Task) string

	// Generate returns raw text. Token counts are best-effort: zero means the
	// endpoint did not report usage, not that nothing was spent.
	Generate(ctx context.Context, task Task, req GenerateRequest) (text string, usage Usage, err error)
}

// Embedder serves the embed task.
type Embedder interface {
	Provider

	// EmbedModel is recorded on every stored vector so a model change can be
	// detected rather than silently mixing incompatible vectors.
	EmbedModel() string

	// Embed returns one vector per input, in input order.
	Embed(ctx context.Context, texts []string) ([][]float32, Usage, error)
}

// Reranker serves the rerank task.
type Reranker interface {
	Provider

	// RerankModel is recorded as provenance.
	RerankModel() string

	// Rerank returns hits ordered best-first, each carrying the index of the
	// document it refers to. topN <= 0 means "all of them".
	Rerank(ctx context.Context, query string, documents []string, topN int) ([]RerankHit, Usage, error)
}

// Usage is what a provider reports about a call. Everything is optional; the
// Client stores what it gets.
type Usage struct {
	InputTokens  int
	OutputTokens int
}
