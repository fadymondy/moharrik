/*
Package ai is the pluggable AI layer for the Opportunity Radar (FM-103).

## One interface per task, providers chosen by configuration

The pipeline never names a vendor. It asks for a *task* — extract, classify,
normalise, summarise, score, draft (generation-shaped), or embed and rerank
(vector-shaped) — and this package resolves that task to a provider using the
environment:

	AI_TASK_EXTRACT=qwen-local
	AI_TASK_DRAFT=claude
	AI_TASK_DEFAULT=qwen-local      # anything left unset lands here

Provider definitions are configuration too (base URL, model, key env var,
timeout, whether calls are paid). Adding a provider means implementing one of
the interfaces in provider.go and adding config — nothing else. No vendor SDK
is imported anywhere in this repo; every provider here speaks plain net/http.

## Local-first, and honest when nothing is there

A full run must be possible with zero paid calls, so paid providers are opt-in
per task, counted, capped per run, and attributable. And because production
today has *no* generation model reachable at all (docs/opportunity-radar,
addendum: TEI on LXC 109 is loopback-bound, CaBrain has no LLM), the layer must
degrade honestly: a task with no provider behind it returns ErrNoProvider,
which a caller can surface in the UI. It never panics and never returns empty
text that would be stored as if a model had said it.

## Never store malformed output as fact

Generation tasks may carry a JSON schema. Output is parsed and validated; a
failure is retried once (configurable) with the validation error fed back, and
then dropped with ErrInvalidOutput. A field that could not be produced is
missing, not wrong.

## Provenance

Every result carries which provider and which model produced it, how many
attempts it took, and whether the configured provider answered or a fallback
did — so a claim can be traced and re-run when a model changes, and a fallback
is never mistaken for the configured path. That is the rule internal/brain
already follows with embed_model and Recall.Mode; it is not reinvented here so
much as generalised.

## Relationship to internal/brain

internal/brain/embed.go keeps working exactly as it did — notes recall is not
rerouted through here. The OpenAI-compatible provider below covers the same
endpoint shape and adds what brain cannot do (an Authorization header, per-task
routing, provenance), so new work uses this package and FM-26 stays untouched.
*/
package ai

import (
	"errors"
	"fmt"
	"time"
)

// Task is one unit of AI work. The pipeline asks for a task; configuration
// decides which provider serves it.
type Task string

const (
	// Generation-shaped tasks.
	TaskExtract   Task = "extract"
	TaskClassify  Task = "classify"
	TaskNormalise Task = "normalise"
	TaskSummarise Task = "summarise"
	TaskScore     Task = "score"
	TaskDraft     Task = "draft"

	// Vector-shaped tasks.
	TaskEmbed  Task = "embed"
	TaskRerank Task = "rerank"
)

// GenerationTasks and VectorTasks exist so config parsing and the admin UI can
// enumerate tasks without a second list drifting from the constants above.
var (
	GenerationTasks = []Task{TaskExtract, TaskClassify, TaskNormalise, TaskSummarise, TaskScore, TaskDraft}
	VectorTasks     = []Task{TaskEmbed, TaskRerank}
)

// AllTasks is every task, generation first.
func AllTasks() []Task {
	out := make([]Task, 0, len(GenerationTasks)+len(VectorTasks))
	out = append(out, GenerationTasks...)
	return append(out, VectorTasks...)
}

// IsGeneration reports whether a task needs a text-generating provider.
func (t Task) IsGeneration() bool {
	for _, g := range GenerationTasks {
		if g == t {
			return true
		}
	}
	return false
}

func (t Task) String() string { return string(t) }

// Errors callers are expected to branch on.
var (
	// ErrNoProvider means the task has no provider behind it. This is a normal
	// state in an unconfigured environment, not a bug: surface it.
	ErrNoProvider = errors.New("ai: no provider configured")

	// ErrInvalidOutput means the model produced output that failed schema
	// validation after every allowed attempt. The result is dropped.
	ErrInvalidOutput = errors.New("ai: model output failed schema validation")

	// ErrPaidCapExceeded means this run has already spent its allowance of
	// paid calls. Local providers remain usable.
	ErrPaidCapExceeded = errors.New("ai: paid call cap exceeded for this run")

	// ErrUnsupported means the provider exists but cannot serve this task
	// shape (asking a reranker to draft, for example).
	ErrUnsupported = errors.New("ai: provider does not support this task")
)

// NoProviderError names the task, so the message a user sees says which half
// of the system is missing rather than "no provider configured".
type NoProviderError struct {
	Task   Task
	Reason string
}

func (e *NoProviderError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("ai: no provider configured for task %q: %s", e.Task, e.Reason)
	}
	return fmt.Sprintf("ai: no provider configured for task %q", e.Task)
}

func (e *NoProviderError) Unwrap() error { return ErrNoProvider }

// InvalidOutputError carries the last validation failure, which is worth
// logging: it usually names the field the model got wrong.
type InvalidOutputError struct {
	Task     Task
	Provider string
	Model    string
	Attempts int
	Last     error
}

func (e *InvalidOutputError) Error() string {
	return fmt.Sprintf(
		"ai: %s/%s produced invalid output for task %q after %d attempt(s): %v",
		e.Provider, e.Model, e.Task, e.Attempts, e.Last,
	)
}

func (e *InvalidOutputError) Unwrap() error { return ErrInvalidOutput }

/*
Provenance travels with every result.

Provider and Model are what a caller persists next to a generated field.
Fallback and FellBackFrom record that the configured provider did not answer —
kept separate from Provider so "who answered" and "who should have answered"
are never conflated.
*/
type Provenance struct {
	Task         Task          `json:"task"`
	Provider     string        `json:"provider"`
	Model        string        `json:"model"`
	Attempts     int           `json:"attempts"`
	Fallback     bool          `json:"fallback"`
	FellBackFrom []string      `json:"fell_back_from,omitempty"`
	Paid         bool          `json:"paid"`
	Duration     time.Duration `json:"duration_ms"`
	InputTokens  int           `json:"input_tokens,omitempty"`
	OutputTokens int           `json:"output_tokens,omitempty"`
}

// Message is one turn of a generation request. Role is "system", "user" or
// "assistant" — the three every provider here understands.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// GenerateRequest is task-shaped, not vendor-shaped.
//
// Schema, when set, is a JSON Schema subset (see schema.go). Its presence is
// what turns on structured-output validation, the retry, and the drop.
type GenerateRequest struct {
	System      string
	Messages    []Message
	Schema      *Schema
	MaxTokens   int
	Temperature float64
}

// GenerateResult is the answer plus the provenance a caller must persist.
type GenerateResult struct {
	Text       string
	JSON       map[string]any
	Provenance Provenance
}

// EmbedResult keeps vectors and provenance together for the same reason:
// mixing vectors from two models silently is the failure mode internal/brain
// already guards against by storing embed_model on every row.
type EmbedResult struct {
	Vectors    [][]float32
	Dimensions int
	Provenance Provenance
}

// RerankHit is one scored document, identified by its index in the input so a
// caller never has to match on text.
type RerankHit struct {
	Index int     `json:"index"`
	Score float64 `json:"score"`
}

// RerankResult is ordered best-first.
type RerankResult struct {
	Hits       []RerankHit
	Provenance Provenance
}
