package mcp

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/opportunities"
	"github.com/fadymondy/moharrik/internal/research"
)

/*
AgentProviderTools: the Opportunity Finder with the CALLING agent as the
provider ("bring your own agent"). The workspace needs no model key and no
search service: the agent asks for work (opportunity_tasks), does the web
research with its own model and tools, and hands back structured results
(submit_opportunities, submit_research), then closes the task
(complete_task, fail_task). Moharrik validates, sanitises, dedupes against
the CRM, scores against the ICP and stores everything with provenance — and
sends nothing: an opener is a draft.

The results land where server-side research puts its own, so the read tools
(find_opportunities, get_opportunity, get_research, find_evidence,
research_overview, prospect_digest) answer from them unchanged.
*/
func AgentProviderTools(engine *research.Engine) []Tool {
	agentProp := map[string]any{"type": "object", "description": "Who you are: your product/agent name and model, recorded on everything you submit.",
		"properties": map[string]any{
			"name":  map[string]any{"type": "string", "description": "e.g. Claude, Codex, Cursor."},
			"model": map[string]any{"type": "string", "description": "e.g. claude-opus-5-5, gpt-5-codex."},
		}}
	type agentArg struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	}
	return []Tool{
		taskTool("agent_tasks",
			"Start here when Moharrik has work for YOUR model — this workspace has no model key, so you are its AI. Returns tasks "+
				"leased to you for 30 minutes, each with everything needed to do it without asking. summary: Moharrik already searched "+
				"and read the web for a research run; the brief carries those pages (numbered sources) and the evidence pulled from "+
				"them — answer the question from them with submit_research(task_id, answer, insights), then complete_task. research: "+
				"answer a question from the web yourself. discovery: find companies that match the ICP (submit_opportunities). Only "+
				"waiting work is returned (create_if_empty, default false, queues the ICP's next discovery query when nothing waits).",
			false, agentProp),
		taskTool("opportunity_tasks",
			"Start here to run the Opportunity Finder (or queued research) with YOUR OWN model and web tools — the workspace "+
				"needs no API key. Returns tasks leased to you for 30 minutes, each with everything needed to do it without asking: the "+
				"ICP (offer, industries, sizes, regions, keywords, exclusions, signal weights), the search queries to run, what each signal "+
				"type means, dedupe hints (CRM companies and companies already found — skip them), and the exact output schema. With nothing "+
				"waiting, the ICP's next discovery query is queued for you (create_if_empty, default true). Then: research, "+
				"submit_opportunities / submit_research, complete_task. Same queue as agent_tasks.",
			true, agentProp),
		submitOpportunitiesTool("submit_opportunities", agentProp),
		submitOpportunitiesTool("submit_prospects", agentProp),
		{
			Name: "submit_research",
			Description: "Hand back research for a task: your short answer (cite sources as [1], [2] — for a summary task, the " +
				"source numbers in its brief; otherwise the order of findings), up to 10 one-line insights, and the " +
				"findings behind it — each a public URL you read, a sentence copied exactly from it, a one-line claim, the company it is " +
				"about and the signal it reports. A finding with no quote records the page as a source only. Stored as the task's " +
				"research run (author_kind=agent); companies named become suggestions, and for a discovery task, scored prospects when " +
				"you complete_task. Up to 60 findings per call.",
			Scope: "research:run",
			InputSchema: schema([]string{"task_id"}, map[string]any{
				"task_id": prop("string", "The task id from agent_tasks / opportunity_tasks."),
				"agent":   agentProp,
				"answer":  prop("string", "Your answer to the task's question, citing the sources as [1], [2]. Required for a summary task."),
				"insights": map[string]any{"type": "array", "maxItems": 10, "items": map[string]any{"type": "string"},
					"description": "Optional: up to 10 one-line takeaways, each citing a source; stored under the answer."},
				"findings": map[string]any{"type": "array", "maxItems": opportunities.MaxFindingsPerCall, "items": opportunities.FindingSchema(),
					"description": "The pages you read and what each says."},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					TaskID   string                  `json:"task_id"`
					Agent    agentArg                `json:"agent"`
					Answer   string                  `json:"answer"`
					Insights []string                `json:"insights"`
					Findings []opportunities.Finding `json:"findings"`
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				id, err := required("task_id", in.TaskID)
				if err != nil {
					return "", err
				}
				res, err := opportunities.SubmitResearch(ctx, a.SQLDB, s, opportunities.Claimant(s, in.Agent.Name, in.Agent.Model), id, in.Answer, in.Insights, in.Findings)
				if err != nil {
					return "", err
				}
				return jsonResult(res)
			},
		},
		{
			Name: "complete_task",
			Description: "Finish a task you hold, so it is not handed out again. Its research run settles like a server run: findings are " +
				"announced, and a discovery task's companies become scored prospects (find_opportunities). Nothing is sent to anyone.",
			Scope: "research:run",
			InputSchema: schema([]string{"task_id"}, map[string]any{
				"task_id": prop("string", "The task id from agent_tasks / opportunity_tasks."),
				"agent":   agentProp,
				"note":    prop("string", "Optional: what you did, in a sentence (e.g. 12 queries, 30 pages, 6 candidates)."),
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				return closeTask(ctx, a, args, "done")
			},
		},
		{
			Name: "fail_task",
			Description: "Give up a task you hold, with the reason (no web access, nothing found, blocked). It is not handed out again; " +
				"anything already submitted for it is kept.",
			Scope: "research:run",
			InputSchema: schema([]string{"task_id", "error"}, map[string]any{
				"task_id": prop("string", "The task id from agent_tasks / opportunity_tasks."),
				"agent":   agentProp,
				"error":   prop("string", "Why, in a sentence."),
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				return closeTask(ctx, a, args, "failed")
			},
		},
		{
			Name: "summarise_research",
			Description: "Summarise a research run yourself: for a finished run Moharrik collected but could not summarise (no model key — " +
				"\"no model is configured, so the pages were collected but not summarised\"). Queues a summary task and returns it with its " +
				"brief: the pages (numbered sources) and the evidence pulled from them. Answer with submit_research(task_id, answer, " +
				"insights), then complete_task. Asking twice returns the same open task.",
			Scope: "research:run",
			InputSchema: schema([]string{"run_id"}, map[string]any{
				"run_id": prop("string", "The run, from list_research or get_research."),
				"agent":  agentProp,
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					RunID string   `json:"run_id"`
					Agent agentArg `json:"agent"`
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				id, err := required("run_id", in.RunID)
				if err != nil {
					return "", err
				}
				if _, err := research.QueueSummary(ctx, a.SQLDB, s.WorkspaceID, id, s.UserID); err != nil {
					return "", err
				}
				c := opportunities.Claimant(s, in.Agent.Name, in.Agent.Model)
				briefs, note, err := opportunities.TakeTasks(ctx, a.SQLDB, s, c, "summary", 5, false)
				if err != nil {
					return "", err
				}
				for _, b := range briefs {
					if b.Task.RunID == id {
						return jsonResult(map[string]any{"task": b, "lease_minutes": int(research.AgentLease.Minutes())})
					}
				}
				return jsonResult(map[string]any{"note": "The summary task is queued but another agent holds it. " + note})
			},
		},
		{
			Name: "set_research_provider",
			Description: "Who does this workspace's research thinking: auto (the server when it has search AND a model key, else your AI " +
				"assistant over MCP), server, or agent (always your assistant: the server still collects pages when it can, and you " +
				"summarise them). Returns the provider and the tasks waiting.",
			Scope: "research:run",
			InputSchema: schema(nil, map[string]any{
				"mode": map[string]any{"type": "string", "enum": []string{"auto", "server", "agent"}, "description": "Leave out to only read it."},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					Mode string `json:"mode"`
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				var p *research.Provider
				if in.Mode == "" {
					p, err = engine.GetProvider(ctx, a.SQLDB, s.WorkspaceID)
				} else {
					p, err = engine.SetProvider(ctx, a.SQLDB, s.WorkspaceID, in.Mode)
				}
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"provider": p, "prompt": opportunities.AgentPrompt(s.WorkspaceName)})
			},
		},
	}
}

func closeTask(ctx context.Context, a *app.App, args json.RawMessage, status string) (string, error) {
	s, err := need(ctx)
	if err != nil {
		return "", err
	}
	var in struct {
		TaskID string `json:"task_id"`
		Agent  struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"agent"`
		Note  string `json:"note"`
		Error string `json:"error"`
	}
	if err := bind(args, &in); err != nil {
		return "", err
	}
	id, err := required("task_id", in.TaskID)
	if err != nil {
		return "", err
	}
	note := in.Note
	if status == "failed" {
		if note, err = required("error", in.Error); err != nil {
			return "", err
		}
	}
	t, err := opportunities.CloseTask(ctx, a, s, opportunities.Claimant(s, in.Agent.Name, in.Agent.Model), id, status, note)
	if err != nil {
		return "", err
	}
	next := "Read the results with find_opportunities (prospects) or get_research(run_id)."
	if status == "failed" {
		next = "The task is closed. Ask for another with agent_tasks."
	}
	return jsonResult(map[string]any{"task": t, "next": next})
}

// taskTool is agent_tasks / opportunity_tasks: one queue, two defaults.
func taskTool(name, description string, createDefault bool, agentProp map[string]any) Tool {
	return Tool{
		Name:        name,
		Description: description,
		Scope:       "research:run",
		InputSchema: schema(nil, map[string]any{
			"agent":           agentProp,
			"kind":            map[string]any{"type": "string", "enum": []string{"discovery", "research", "summary"}, "description": "Only this kind of task. Default any."},
			"limit":           prop("integer", "How many tasks to take at once, 1 to 5. Default 1."),
			"create_if_empty": prop("boolean", "Queue the ICP's next discovery query when nothing is waiting."),
		}),
		Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
			s, err := need(ctx)
			if err != nil {
				return "", err
			}
			var in struct {
				Agent struct {
					Name  string `json:"name"`
					Model string `json:"model"`
				} `json:"agent"`
				Kind   string `json:"kind"`
				Limit  int    `json:"limit"`
				Create *bool  `json:"create_if_empty"`
			}
			if err := bind(args, &in); err != nil {
				return "", err
			}
			if in.Kind != "" && in.Kind != "discovery" && in.Kind != "research" && in.Kind != "summary" {
				return "", errors.New("kind is discovery, research or summary")
			}
			create := createDefault
			if in.Create != nil {
				create = *in.Create
			}
			c := opportunities.Claimant(s, in.Agent.Name, in.Agent.Model)
			tasks, note, err := opportunities.TakeTasks(ctx, a.SQLDB, s, c, in.Kind, in.Limit, create)
			if err != nil {
				return "", err
			}
			return jsonResult(map[string]any{"count": len(tasks), "tasks": tasks, "note": note,
				"lease_minutes": int(research.AgentLease.Minutes()), "agent": map[string]string{"name": c.Agent, "model": c.Model}})
		},
	}
}

// submitOpportunitiesTool is submit_opportunities and its alias submit_prospects.
func submitOpportunitiesTool(name string, agentProp map[string]any) Tool {
	type agentArg struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	}
	alias := ""
	if name != "submit_opportunities" {
		alias = " (Same as submit_opportunities.)"
	}
	return Tool{
		Name: name,
		Description: "Hand back the companies you found for a task: company + domain, the person to talk to (name, role and contact " +
			"channels only if a public page publishes them), signal_type, why_now, a suggested opener, and evidence — at least one " +
			"public URL per candidate with a sentence copied exactly from that page. Moharrik strips markup, refuses non-public URLs, " +
			"dedupes against the CRM (by domain/name) and contacts/leads (by email), scores each against the ICP, and stores it as a " +
			"prospect with provenance (author_kind=agent, your agent and model). The opener is stored as a DRAFT and never sent. " +
			"Up to 25 candidates per call; call again for more, then complete_task." + alias,
		Scope: "research:run",
		InputSchema: schema([]string{"task_id", "candidates"}, map[string]any{
			"task_id": prop("string", "The task id from agent_tasks / opportunity_tasks."),
			"agent":   agentProp,
			"candidates": map[string]any{"type": "array", "maxItems": opportunities.MaxCandidatesPerCall, "items": opportunities.CandidateSchema(),
				"description": "The companies you found, best first."},
		}),
		Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
			s, err := need(ctx)
			if err != nil {
				return "", err
			}
			var in struct {
				TaskID     string                    `json:"task_id"`
				Agent      agentArg                  `json:"agent"`
				Candidates []opportunities.Candidate `json:"candidates"`
			}
			if err := bind(args, &in); err != nil {
				return "", err
			}
			id, err := required("task_id", in.TaskID)
			if err != nil {
				return "", err
			}
			res, err := opportunities.SubmitOpportunities(ctx, a.SQLDB, s, opportunities.Claimant(s, in.Agent.Name, in.Agent.Model), id, in.Candidates)
			if err != nil {
				return "", err
			}
			return jsonResult(res)
		},
	}
}
