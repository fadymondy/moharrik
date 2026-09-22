package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/research"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
ResearchTools: ask a question of the open web, and read back what was found
(MH-227).

A model calling this gets the same guardrails a person does, because it goes
through the same research.Engine: one run at a time per workspace, the run
deadline, and the source cap.

The reason this tool exists at all is worth stating: a model already has its
own idea of the world, and it is out of date and unsourced. This gives it
pages fetched just now, each with a URL — so an answer it builds on them can
be checked. THE SOURCES ARE ALWAYS RETURNED, even when the summary is empty,
because an answer with no way to verify it is the thing this is meant to
replace.

start_research does not block. Research takes minutes — a search, several
browser renders and a model call — and a tool that waited would time out and
leave the run going anyway. It returns the id; get_research reads the result.
*/
func ResearchTools(engine *research.Engine) []Tool {
	return []Tool{
		{
			Name: "start_research",
			Description: "Search the web for a question, read the top results in a real browser, and summarise them with their sources. " +
				"Returns a run id immediately — research takes minutes. Read it back with get_research.",
			Scope: "research:run",
			InputSchema: schema([]string{"topic"}, map[string]any{
				"topic": prop("string", "The question, in plain words. This is what gets searched."),
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					Topic string `json:"topic"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", errors.New("could not read the arguments")
				}

				runID, err := engine.Start(ctx, a, s.WorkspaceID, s.UserID, in.Topic)
				if err != nil {
					// The engine's own sentence, which already explains itself:
					// "this workspace is already running some research",
					// "search is not configured for this deployment".
					return "", err
				}
				return fmt.Sprintf("Research started. Run id: %s\nRead it back with get_research once it has finished.", runID), nil
			},
		},
		{
			Name:        "list_research",
			Description: "Past research runs in this workspace, newest first, with their status and how many sources each found and read.",
			Scope:       "research:read",
			InputSchema: schema(nil, map[string]any{
				"limit": prop("integer", "How many to return. Default 10."),
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					Limit int `json:"limit"`
				}
				_ = json.Unmarshal(args, &in)
				if in.Limit <= 0 {
					in.Limit = 10
				}

				runs, err := research.List(ctx, a, s.WorkspaceID, in.Limit)
				if err != nil {
					return "", err
				}
				if len(runs) == 0 {
					return "No research has been run in this workspace yet.", nil
				}

				var b strings.Builder
				for _, r := range runs {
					fmt.Fprintf(&b, "- [%s] %s — %s, %d found / %d read",
						r.ID, r.Topic, r.Status, r.FoundCount, r.ReadCount)
					if r.Error != "" {
						fmt.Fprintf(&b, " (%s)", r.Error)
					}
					b.WriteString("\n")
				}
				return b.String(), nil
			},
		},
		{
			Name: "get_research",
			Description: "One research run: its answer and every source behind it, with URLs. " +
				"Sources are listed even when they could not be read, so a thin answer is visibly thin.",
			Scope: "research:read",
			InputSchema: schema([]string{"run_id"}, map[string]any{
				"run_id":    prop("string", "The id returned by start_research."),
				"full_text": prop("boolean", "Include the fetched page text of each source. Large — off by default."),
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					RunID    string `json:"run_id"`
					FullText bool   `json:"full_text"`
				}
				if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.RunID) == "" {
					return "", errors.New("a run id is required")
				}

				run, sources, err := research.Get(ctx, a, s.WorkspaceID, in.RunID, in.FullText)
				if err != nil {
					return "", err
				}

				var b strings.Builder
				fmt.Fprintf(&b, "Question: %s\nStatus: %s", run.Topic, run.Status)
				if run.Status == "queued" || run.Status == "running" {
					b.WriteString(" — still going, ask again shortly.\n")
					return b.String(), nil
				}
				b.WriteString("\n")

				if run.Error != "" {
					fmt.Fprintf(&b, "Note: %s\n", run.Error)
				}
				if run.Summary != "" {
					fmt.Fprintf(&b, "\n%s\n", run.Summary)
					if run.Model != "" {
						fmt.Fprintf(&b, "\n(summarised by %s)\n", run.Model)
					}
				} else {
					b.WriteString("\nNo summary was written — the sources below are what was found.\n")
				}

				fmt.Fprintf(&b, "\nSources (%d found, %d read):\n", run.FoundCount, run.ReadCount)
				for _, src := range sources {
					fmt.Fprintf(&b, "[%d] %s\n    %s\n", src.Position+1, src.Title, src.URL)
					if !src.Fetched {
						fmt.Fprintf(&b, "    not read: %s\n", src.Error)
						if src.Snippet != "" {
							fmt.Fprintf(&b, "    snippet: %s\n", src.Snippet)
						}
					}
					if in.FullText && src.Content != "" {
						fmt.Fprintf(&b, "\n%s\n\n", src.Content)
					}
				}
				return b.String(), nil
			},
		},
	}
}
