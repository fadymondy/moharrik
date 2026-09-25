package contactcontext

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/fadymondy/moharrik/internal/ai"
	"github.com/fadymondy/moharrik/internal/app"
)

// Citation is one passage an answer/report leans on, in the shape the UI
// needs to render a clickable reference and jump to it.
type Citation struct {
	Kind           string `json:"kind"`
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id,omitempty"`
	When           string `json:"when,omitempty"`
	Snippet        string `json:"snippet"`
}

// Answer is ask_about_contact's result.
type Answer struct {
	Text       string     `json:"text"`
	Citations  []Citation `json:"citations"`
	NoProvider bool       `json:"no_provider" doc:"true when there is no configured AI provider: Text is the raw retrieved passages, not a generated answer"`
	Provider   string     `json:"provider,omitempty"`
	Model      string     `json:"model,omitempty"`
}

func toCitations(passages []Passage) []Citation {
	out := make([]Citation, 0, len(passages))
	for _, p := range passages {
		when := ""
		if !p.When.IsZero() {
			when = p.When.Format("2006-01-02 15:04")
		}
		out = append(out, Citation{Kind: p.Kind, ID: p.ID, ConversationID: p.ConversationID, When: when, Snippet: trimTo(p.Text, 240)})
	}
	return out
}

// AskAboutContact answers a free-text question grounded ONLY in this
// contact's retrieved context (Retrieve). MH-586 requires this to never fail
// just because no AI provider is configured: with none, it returns the raw
// retrieved passages plus an explicit "no model configured" note instead of
// an error — see Answer.NoProvider.
//
// The prompt below is deliberately built to forbid invented facts: every
// sentence the model writes must trace to one of the numbered passages, and
// it is told to say "I don't know" rather than guess.
func AskAboutContact(ctx context.Context, a *app.App, aiClient *ai.Client, workspaceID, contactID, question string) (*Answer, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, errors.New("question is required")
	}
	passages, err := Retrieve(ctx, a, workspaceID, contactID, question)
	if err != nil {
		return nil, err
	}
	brief, err := loadContactBrief(ctx, a, workspaceID, contactID)
	if err != nil {
		return nil, err
	}

	if aiClient == nil {
		return &Answer{
			Text:       rawPassagesFallback(brief, passages),
			Citations:  toCitations(passages),
			NoProvider: true,
		}, nil
	}

	prompt := buildAskPrompt(brief, question, passages)
	res, genErr := aiClient.Generate(ctx, ai.TaskDraft, ai.GenerateRequest{
		System: "You answer questions about a CRM contact using ONLY the numbered passages given to you. " +
			"Every claim must cite a passage number like [3]. If the passages do not answer the question, say so plainly " +
			"instead of guessing. Never invent a name, date, amount or promise that is not in a passage.",
		Messages:    []ai.Message{{Role: "user", Content: prompt}},
		MaxTokens:   700,
		Temperature: 0.1,
	})
	if genErr != nil {
		if errors.Is(genErr, ai.ErrNoProvider) {
			return &Answer{
				Text:       rawPassagesFallback(brief, passages),
				Citations:  toCitations(passages),
				NoProvider: true,
			}, nil
		}
		return nil, fmt.Errorf("contactcontext: ask_about_contact generation: %w", genErr)
	}
	return &Answer{
		Text:      res.Text,
		Citations: toCitations(passages),
		Provider:  res.Provenance.Provider,
		Model:     res.Provenance.Model,
	}, nil
}

func buildAskPrompt(brief ContactBrief, question string, passages []Passage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Contact: %s", brief.Name)
	if brief.Email != "" {
		fmt.Fprintf(&b, " <%s>", brief.Email)
	}
	b.WriteString("\n\nPassages:\n")
	if len(passages) == 0 {
		b.WriteString("(none retrieved — say plainly that there is nothing in this contact's context to answer from)\n")
	}
	for i, p := range passages {
		when := ""
		if !p.When.IsZero() {
			when = " (" + p.When.Format("2006-01-02") + ")"
		}
		fmt.Fprintf(&b, "[%d] (%s%s) %s\n", i+1, p.Kind, when, trimTo(p.Text, 500))
	}
	fmt.Fprintf(&b, "\nQuestion: %s\n", question)
	return b.String()
}

// rawPassagesFallback is what ask_about_contact and generate_contact_report
// return when no LLM is configured: never an error, always something useful.
func rawPassagesFallback(brief ContactBrief, passages []Passage) string {
	if len(passages) == 0 {
		return "No AI provider is configured, and nothing in " + brief.Name + "'s context matched this question. " +
			"(Add context with add_contact_context, or import their WhatsApp history, then try again.)"
	}
	var b strings.Builder
	b.WriteString("No AI provider is configured, so here are the most relevant passages found in ")
	b.WriteString(brief.Name)
	b.WriteString("'s context — read them yourself:\n\n")
	for i, p := range passages {
		when := ""
		if !p.When.IsZero() {
			when = " (" + p.When.Format("2006-01-02") + ")"
		}
		fmt.Fprintf(&b, "%d. [%s%s] %s\n", i+1, p.Kind, when, trimTo(p.Text, 300))
	}
	return b.String()
}

// aiClientOrNil is the one place that decides whether ask/report have a
// model to call. ai.NewFromEnv never errors just because no provider section
// is configured (internal/ai/client.go: "a configuration with no providers is
// valid"); it can fail on a genuinely malformed AI_* env var, which is logged
// and treated the same as "no provider" rather than failing the request.
// ResolveAIClient is the exported entry point Register (routes.go) uses at
// server startup to decide once whether this feature has a model to call —
// see aiClientOrNil's doc comment just below for what "invalid config" does.
func ResolveAIClient(log *slog.Logger) *ai.Client {
	return aiClientOrNil(log)
}

func aiClientOrNil(log *slog.Logger) *ai.Client {
	c, err := ai.NewFromEnv(log)
	if err != nil {
		if log != nil {
			log.Warn("contactcontext: AI config invalid, continuing with no provider", "err", err)
		}
		return nil
	}
	return c
}
