package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/research"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
ResearchTools: ask a question of the open web (or read a list of pages), and
read back what was found — the answer, the sources, and the EVIDENCE (MH-227,
MH-388).

A model calling this gets the same guardrails a person does, because it goes
through the same research.Engine: one run at a time per workspace, the run
deadline, the source cap, and — for schedules — the daily budget.

start_research does not block. It returns the id; get_research reads the
result. find_evidence searches what every run collected; the company-suggestion
tools are the step from evidence to the CRM, and never skip the review.
*/
func ResearchTools(engine *research.Engine) []Tool {
	signalEnum := append([]string{""}, research.SignalTypes...)
	return []Tool{
		{
			Name: "start_research",
			Description: "Research a question: search the web, read the top results in a real browser, extract evidence (quotes, " +
				"the companies they name, the signal they report) and summarise when a model is configured. Or pass mode=urls " +
				"with a list of pages to read those instead of searching. Returns a run id immediately — read it back with get_research.",
			Scope: "research:run",
			InputSchema: schema([]string{"topic"}, map[string]any{
				"topic": prop("string", "The question, in plain words. In URL mode, what to look for in the pages."),
				"mode":  map[string]any{"type": "string", "enum": []string{"search", "urls"}, "description": "search (default) or urls."},
				"urls":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "URL mode: up to 10 pages to read."},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				if !authz.Can(s.Role, authz.ResearchRun) {
					return "", errors.New("your role cannot start research in this workspace")
				}
				var in struct {
					Topic string   `json:"topic"`
					Mode  string   `json:"mode"`
					URLs  []string `json:"urls"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", errors.New("could not read the arguments")
				}
				if engine == nil {
					return "", research.ErrNoSearch
				}
				runID, err := engine.StartRun(ctx, a, s.WorkspaceID, s.UserID, research.Request{Topic: in.Topic, Mode: in.Mode, URLs: in.URLs})
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"ok": true, "run_id": runID, "message": "Read it back with get_research once it has finished."})
			},
		},
		{
			Name:        "list_research",
			Description: "Past research runs in this workspace, newest first, with their status, how many sources each found and read, and how much evidence each kept.",
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
				return jsonResult(map[string]any{"count": len(runs), "items": runs})
			},
		},
		{
			Name: "get_research",
			Description: "One research run: its stages and what it spent, its answer, every source behind it with URLs, and the " +
				"evidence it extracted. Sources are listed even when they could not be read, so a thin answer is visibly thin.",
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

				if run.Status == "queued" || run.Status == "running" {
					return jsonResult(map[string]any{
						"run": run, "message": "Still going — ask again shortly.",
					})
				}

				var evidence []research.Evidence
				var evidenceTotal int
				ev, err := research.FindEvidence(ctx, a.SQLDB, s.WorkspaceID, research.EvidenceQuery{RunID: run.ID, Limit: 30})
				if err == nil && ev != nil {
					evidence = ev.Evidence
					evidenceTotal = ev.Total
				}
				return jsonResult(map[string]any{
					"run": run, "sources": sources, "evidence": evidence, "evidence_total": evidenceTotal,
					"message": "evidence_total may exceed evidence — find_evidence searches evidence across every run.",
				})
			},
		},
		{
			Name: "find_evidence",
			Description: "Search the evidence every research run in this workspace collected, across runs: verbatim quotes (and, " +
				"when a model extracted them, one-line claims) with their source URL, the company named, the signal reported, " +
				"and dates. Filter by text, company (name or domain), signal type, source domain, run and date. Newest first, " +
				"a page at a time: total is how many match the filter; pass next_cursor back as cursor for more.",
			Scope: "research:read",
			InputSchema: schema(nil, map[string]any{
				"q":           prop("string", "Words in the quote, claim, page title or company name."),
				"company":     prop("string", "A company's name or domain."),
				"signal_type": map[string]any{"type": "string", "enum": signalEnum},
				"domain":      prop("string", "The source site, e.g. techcrunch.com."),
				"run_id":      prop("string", "Only this run's evidence."),
				"since":       prop("string", "YYYY-MM-DD — published or captured on or after."),
				"until":       prop("string", "YYYY-MM-DD — captured on or before."),
				"limit":       prop("integer", "Page size, default 25, at most 100."),
				"cursor":      prop("string", "next_cursor from the previous answer"),
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					Q          string `json:"q"`
					Company    string `json:"company"`
					SignalType string `json:"signal_type"`
					Domain     string `json:"domain"`
					RunID      string `json:"run_id"`
					Since      string `json:"since"`
					Until      string `json:"until"`
					Limit      int    `json:"limit"`
					Cursor     string `json:"cursor"`
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				since, err := research.ParseDay(in.Since, false)
				if err != nil {
					return "", err
				}
				until, err := research.ParseDay(in.Until, true)
				if err != nil {
					return "", err
				}
				page, err := research.FindEvidencePage(ctx, a.SQLDB, s.WorkspaceID, research.EvidenceQuery{Q: in.Q, Company: in.Company,
					SignalType: in.SignalType, Domain: in.Domain, RunID: in.RunID, Since: since, Until: until, Limit: in.Limit, Cursor: in.Cursor})
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"total": page.Total, "count": len(page.Evidence), "items": page.Evidence, "next_cursor": page.NextCursor})
			},
		},
		{
			Name: "list_company_suggestions",
			Description: "Companies research found in its evidence, waiting for a person to review them — never added to the CRM on their own. " +
				"Each carries its strongest evidence. status: suggested (default), accepted, dismissed, linked (already in the CRM). " +
				"Strongest first, a page at a time: pass next_cursor back as cursor for more.",
			Scope: "research:read",
			InputSchema: schema(nil, map[string]any{
				"status": map[string]any{"type": "string", "enum": []string{"suggested", "accepted", "dismissed", "linked"}},
				"limit":  prop("integer", "Page size, default 25, at most 100."),
				"cursor": prop("string", "next_cursor from the previous answer"),
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					Status string `json:"status"`
					Limit  int    `json:"limit"`
					Cursor string `json:"cursor"`
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				page, err := research.ListSuggestionsPage(ctx, a.SQLDB, s.WorkspaceID, in.Status, in.Cursor, in.Limit)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"total": page.Total, "count": len(page.Suggestions), "items": page.Suggestions, "next_cursor": page.NextCursor})
			},
		},
		{
			Name: "accept_company_suggestion",
			Description: "Add a company research suggested to the CRM — or link the CRM company that already has its domain or name — " +
				"and put its evidence on the company's timeline as a note. name/domain correct what research read.",
			Scope: "contacts:write",
			InputSchema: schema([]string{"id"}, map[string]any{
				"id":     prop("string", "The suggestion id (list_company_suggestions)."),
				"name":   prop("string", "Optional: the company's name, if research read it wrong."),
				"domain": prop("string", "Optional: its website domain."),
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					ID     string `json:"id"`
					Name   string `json:"name"`
					Domain string `json:"domain"`
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				res, err := research.Accept(ctx, a.SQLDB, s.WorkspaceID, s.UserID, id, research.SuggestionAcceptInput{Name: in.Name, Domain: in.Domain})
				if err != nil {
					return "", err
				}
				return jsonResult(res)
			},
		},
		{
			Name:        "dismiss_company_suggestion",
			Description: "Say no to a company research suggested. It is remembered: later runs that find it again keep it dismissed.",
			Scope:       "contacts:write",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "The suggestion id.")}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					ID string `json:"id"`
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				if !authz.Can(s.Role, authz.ContactsWrite) {
					return "", errors.New("your role cannot change the CRM in this workspace")
				}
				res, err := research.Dismiss(ctx, a.SQLDB, s.WorkspaceID, s.UserID, id)
				if err != nil {
					return "", err
				}
				return jsonResult(res)
			},
		},
		{
			Name:        "list_research_schedules",
			Description: "Research that runs on its own (daily or weekly), with each schedule's last outcome, and today's automatic-run budget.",
			Scope:       "research:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				list, err := research.ListSchedules(ctx, a.SQLDB, s.WorkspaceID)
				if err != nil {
					return "", err
				}
				b, err := research.GetBudget(ctx, a.SQLDB, s.WorkspaceID)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"schedules": list, "budget": b})
			},
		},
		{
			Name: "save_research_schedule",
			Description: "Create a research schedule (no id) or change one (with id): a question (mode search) or a list of pages " +
				"(mode urls), run daily or weekly. Every scheduled run spends one of the workspace's daily research budget.",
			Scope: "research:run",
			InputSchema: schema(nil, map[string]any{
				"id":      prop("string", "Omit to create."),
				"name":    prop("string", "What to call it."),
				"topic":   prop("string", "The question."),
				"mode":    map[string]any{"type": "string", "enum": []string{"search", "urls"}},
				"urls":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"cadence": map[string]any{"type": "string", "enum": []string{"daily", "weekly"}},
				"enabled": prop("boolean", "false pauses it."),
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				if !authz.Can(s.Role, authz.ResearchRun) {
					return "", errors.New("your role cannot schedule research in this workspace")
				}
				var in struct {
					ID string `json:"id"`
					research.ResearchScheduleInput
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				var sc *research.ResearchSchedule
				if strings.TrimSpace(in.ID) == "" {
					sc, err = research.CreateSchedule(ctx, a.SQLDB, s.WorkspaceID, s.UserID, in.ResearchScheduleInput)
				} else {
					sc, err = research.UpdateSchedule(ctx, a.SQLDB, s.WorkspaceID, in.ID, in.ResearchScheduleInput)
				}
				if err != nil {
					return "", err
				}
				return jsonResult(sc)
			},
		},
		{
			Name:        "delete_research_schedule",
			Description: "Delete a research schedule. Its past runs and their evidence stay.",
			Scope:       "research:run",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "The schedule id.")}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				if !authz.Can(s.Role, authz.ResearchRun) {
					return "", errors.New("your role cannot change research schedules in this workspace")
				}
				var in struct {
					ID string `json:"id"`
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				if err := research.DeleteSchedule(ctx, a.SQLDB, s.WorkspaceID, id); err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"ok": true, "deleted": true, "id": id})
			},
		},
		{
			Name:        "set_research_budget",
			Description: "How many automatic research runs (schedules, and prospect discovery) the workspace may start per UTC day, 0 to 50. A person's own runs are not counted.",
			Scope:       "research:run",
			InputSchema: schema([]string{"daily_run_budget"}, map[string]any{"daily_run_budget": prop("integer", "0 to 50. 0 stops automatic research.")}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				if !authz.Can(s.Role, authz.ResearchRun) {
					return "", errors.New("your role cannot change the research budget in this workspace")
				}
				var in struct {
					N *int `json:"daily_run_budget"`
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				if in.N == nil {
					return "", errors.New("daily_run_budget is required")
				}
				b, err := research.SetBudget(ctx, a.SQLDB, s.WorkspaceID, *in.N)
				if err != nil {
					return "", err
				}
				return jsonResult(b)
			},
		},
	}
}
