package research

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

var tags = []string{"Research"}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

// CompanySuggestionList is GET /api/w/company-suggestions. A named type, so the
// OpenAPI export names its schema (an anonymous struct was published as
// "BodyStruct { Suggestions … }Body").
type CompanySuggestionList struct {
	Suggestions []CompanySuggestion `json:"suggestions"`
	Total       int                 `json:"total"`
}

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

func scope(ctx context.Context, action string) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, action) {
		return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
	}
	return s, nil
}

// RunList is the research page's list: runs plus what the deployment can do.
type ResearchRunList struct {
	Runs         []ResearchRun   `json:"runs"`
	CanSearch    bool            `json:"can_search"`
	CanRead      bool            `json:"can_read"`
	CanSummarise bool            `json:"can_summarise"`
	Budget       *ResearchBudget `json:"budget"`
}

// RunDetail is one run with its sources, evidence and the companies it named.
type ResearchRunDetail struct {
	ResearchRun ResearchRun         `json:"run"`
	Sources     []ResearchSource    `json:"sources"`
	Evidence    []Evidence          `json:"evidence"`
	Companies   []CompanySuggestion `json:"companies"`
}

// StartBody starts a run.
type ResearchStartBody struct {
	Topic string   `json:"topic" minLength:"3" maxLength:"500" doc:"The question, in your own words. In URL mode, what to look for in the pages."`
	Mode  string   `json:"mode,omitempty" enum:"search,urls" doc:"search (default) asks the search engine; urls reads the pages given."`
	URLs  []string `json:"urls,omitempty" maxItems:"10" doc:"URL mode: the pages to read."`
}

func startErr(err error) error {
	switch {
	case errors.Is(err, ErrBusy):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, ErrNoSearch), errors.Is(err, ErrNoReader):
		// 501: the request was fine and the deployment cannot do it.
		return huma.Error501NotImplemented(err.Error())
	case errors.Is(err, ErrOverBudget):
		return huma.Error429TooManyRequests(err.Error())
	}
	return huma.Error422UnprocessableEntity(err.Error())
}

// ParseDay reads a YYYY-MM-DD or RFC 3339 filter date; end=true is the day's last instant.
func ParseDay(s string, end bool) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, errors.New("dates are YYYY-MM-DD or RFC 3339")
	}
	if end {
		t = t.Add(24*time.Hour - time.Nanosecond)
	}
	return &t, nil
}

// Register mounts the research endpoints (MH-227, MH-388).
func Register(api huma.API, a *app.App, engine *Engine) {
	huma.Register(api, huma.Operation{
		OperationID: "w-research-runs", Method: http.MethodGet, Path: "/api/w/research",
		Summary: "Past research runs, newest first, with what this deployment can do and today's budget", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Limit int `query:"limit" minimum:"1" maximum:"100" doc:"Default 25."`
	}) (*body[ResearchRunList], error) {
		s, err := scope(ctx, authz.ResearchRead)
		if err != nil {
			return nil, err
		}
		runs, err := List(ctx, a, s.WorkspaceID, in.Limit)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the research runs")
		}
		if runs == nil {
			runs = []ResearchRun{}
		}
		budget, err := GetBudget(ctx, a.SQLDB, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the research budget")
		}
		caps := engine.Caps()
		return out(ResearchRunList{Runs: runs, CanSearch: caps.CanSearch, CanRead: caps.CanRead, CanSummarise: caps.CanSummarise, Budget: budget}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-research-run", Method: http.MethodGet, Path: "/api/w/research/{id}",
		Summary: "One research run: its stages and cost, the sources, the evidence and the companies it named", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID          string `path:"id"`
		WithContent bool   `query:"content" doc:"Include the fetched page text of each source."`
	}) (*body[ResearchRunDetail], error) {
		s, err := scope(ctx, authz.ResearchRead)
		if err != nil {
			return nil, err
		}
		run, sources, err := Get(ctx, a, s.WorkspaceID, in.ID, in.WithContent)
		if err != nil {
			return nil, huma.Error404NotFound(err.Error())
		}
		if sources == nil {
			sources = []ResearchSource{}
		}
		ev, err := FindEvidence(ctx, a.SQLDB, s.WorkspaceID, EvidenceQuery{RunID: in.ID, Limit: 100})
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the run's evidence")
		}
		companies, err := runCompanies(ctx, a, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the companies the run found")
		}
		return out(ResearchRunDetail{ResearchRun: *run, Sources: sources, Evidence: ev.Evidence, Companies: companies}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-research-start", Method: http.MethodPost, Path: "/api/w/research",
		Summary:     "Research a question (search the web) or a list of pages (URL mode): read, extract evidence, summarise",
		Description: "501 when this deployment cannot do the mode asked for (no search engine, or no page reader for URL mode).",
		Tags:        tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body ResearchStartBody
	}) (*body[struct {
		RunID string `json:"run_id"`
	}], error) {
		s, err := scope(ctx, authz.ResearchRun)
		if err != nil {
			return nil, err
		}
		runID, err := engine.StartRun(ctx, a, s.WorkspaceID, s.UserID, Request{Topic: in.Body.Topic, Mode: in.Body.Mode, URLs: in.Body.URLs})
		if err != nil {
			return nil, startErr(err)
		}
		return out(struct {
			RunID string `json:"run_id"`
		}{runID}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-research-cancel", Method: http.MethodPost, Path: "/api/w/research/{id}/cancel",
		Summary: "Stop a research run that is still going", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[struct {
		OK bool `json:"ok"`
	}], error) {
		s, err := scope(ctx, authz.ResearchRun)
		if err != nil {
			return nil, err
		}
		if err := Cancel(ctx, a, s.WorkspaceID, in.ID); err != nil {
			return nil, huma.Error409Conflict(err.Error())
		}
		return out(struct {
			OK bool `json:"ok"`
		}{true}), nil
	})

	// ── evidence ─────────────────────────────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID: "w-evidence", Method: http.MethodGet, Path: "/api/w/evidence",
		Summary: "Search the evidence every research run collected: by text, company, signal, source domain, run and date", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Q       string `query:"q" maxLength:"200" doc:"Words in the quote, claim, page title or company."`
		Company string `query:"company" maxLength:"200" doc:"A company's name or domain."`
		Signal  string `query:"signal" enum:",hiring,funding,launch,expansion,partnership,leadership,technology,regulatory"`
		Domain  string `query:"domain" maxLength:"253" doc:"The source site, e.g. techcrunch.com."`
		RunID   string `query:"run" doc:"Only this run's evidence."`
		Since   string `query:"since" doc:"YYYY-MM-DD: published or captured on or after."`
		Until   string `query:"until" doc:"YYYY-MM-DD: captured on or before."`
		Limit   int    `query:"limit" minimum:"1" maximum:"100" doc:"Default 25."`
		Offset  int    `query:"offset" minimum:"0"`
	}) (*body[EvidencePage], error) {
		s, err := scope(ctx, authz.ResearchRead)
		if err != nil {
			return nil, err
		}
		since, err := ParseDay(in.Since, false)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		until, err := ParseDay(in.Until, true)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		page, err := FindEvidence(ctx, a.SQLDB, s.WorkspaceID, EvidenceQuery{Q: in.Q, Company: in.Company, SignalType: in.Signal,
			Domain: in.Domain, RunID: in.RunID, Since: since, Until: until, Limit: in.Limit, Offset: in.Offset})
		if err != nil {
			return nil, huma.Error500InternalServerError("could not search the evidence")
		}
		return out(*page), nil
	})

	// ── company suggestions ──────────────────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID: "w-company-suggestions", Method: http.MethodGet, Path: "/api/w/company-suggestions",
		Summary: "Companies research found, waiting for review (or accepted, dismissed, already in the CRM)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Status string `query:"status" enum:",suggested,accepted,dismissed,linked" doc:"Default suggested."`
		Limit  int    `query:"limit" minimum:"1" maximum:"100"`
	}) (*body[CompanySuggestionList], error) {
		s, err := scope(ctx, authz.ResearchRead)
		if err != nil {
			return nil, err
		}
		list, total, err := ListSuggestions(ctx, a.SQLDB, s.WorkspaceID, in.Status, in.Limit)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the suggestions")
		}
		return out(CompanySuggestionList{list, total}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-company-suggestion-accept", Method: http.MethodPost, Path: "/api/w/company-suggestions/{id}/accept",
		Summary: "Add a suggested company to the CRM (or link the one already there), with its evidence as a timeline note", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body SuggestionAcceptInput
	}) (*body[SuggestionAccepted], error) {
		s, err := scope(ctx, authz.ContactsWrite)
		if err != nil {
			return nil, err
		}
		res, err := Accept(ctx, a.SQLDB, s.WorkspaceID, s.UserID, in.ID, in.Body)
		if err != nil {
			return nil, suggestionErr(err)
		}
		return out(*res), nil
	})

	for _, op := range []struct{ id, path, summary string }{
		{"w-company-suggestion-dismiss", "dismiss", "Dismiss a suggested company; research will not suggest it again"},
		{"w-company-suggestion-restore", "restore", "Put a dismissed company back among the suggestions"},
	} {
		op := op
		huma.Register(api, huma.Operation{
			OperationID: op.id, Method: http.MethodPost, Path: "/api/w/company-suggestions/{id}/" + op.path,
			Summary: op.summary, Tags: tags,
		}, func(ctx context.Context, in *struct {
			wsHeader
			ID string `path:"id"`
		}) (*body[CompanySuggestion], error) {
			s, err := scope(ctx, authz.ContactsWrite)
			if err != nil {
				return nil, err
			}
			f := Dismiss
			if op.path == "restore" {
				f = Restore
			}
			res, err := f(ctx, a.SQLDB, s.WorkspaceID, s.UserID, in.ID)
			if err != nil {
				return nil, suggestionErr(err)
			}
			return out(*res), nil
		})
	}

	// ── schedules and budget ─────────────────────────────────────────────

	huma.Register(api, huma.Operation{
		OperationID: "w-research-schedules", Method: http.MethodGet, Path: "/api/w/research-schedules",
		Summary: "Research that runs on its own, and the daily budget it spends", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[struct {
		Schedules []ResearchSchedule `json:"schedules"`
		Budget    *ResearchBudget    `json:"budget"`
	}], error) {
		s, err := scope(ctx, authz.ResearchRead)
		if err != nil {
			return nil, err
		}
		list, err := ListSchedules(ctx, a.SQLDB, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the schedules")
		}
		b, err := GetBudget(ctx, a.SQLDB, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the budget")
		}
		return out(struct {
			Schedules []ResearchSchedule `json:"schedules"`
			Budget    *ResearchBudget    `json:"budget"`
		}{list, b}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-research-schedule-create", Method: http.MethodPost, Path: "/api/w/research-schedules",
		Summary: "Schedule research: a question or a list of pages, daily or weekly", Tags: tags, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body ResearchScheduleInput
	}) (*body[ResearchSchedule], error) {
		s, err := scope(ctx, authz.ResearchRun)
		if err != nil {
			return nil, err
		}
		sc, err := CreateSchedule(ctx, a.SQLDB, s.WorkspaceID, s.UserID, in.Body)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return out(*sc), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-research-schedule-update", Method: http.MethodPatch, Path: "/api/w/research-schedules/{id}",
		Summary: "Change a research schedule, or pause and resume it", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body ResearchScheduleInput
	}) (*body[ResearchSchedule], error) {
		s, err := scope(ctx, authz.ResearchRun)
		if err != nil {
			return nil, err
		}
		if _, err := getSchedule(ctx, a.SQLDB, s.WorkspaceID, in.ID); err != nil {
			return nil, huma.Error404NotFound(err.Error())
		}
		sc, err := UpdateSchedule(ctx, a.SQLDB, s.WorkspaceID, in.ID, in.Body)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return out(*sc), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-research-schedule-delete", Method: http.MethodDelete, Path: "/api/w/research-schedules/{id}",
		Summary: "Delete a research schedule (its past runs stay)", Tags: tags, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*struct{}, error) {
		s, err := scope(ctx, authz.ResearchRun)
		if err != nil {
			return nil, err
		}
		if err := DeleteSchedule(ctx, a.SQLDB, s.WorkspaceID, in.ID); err != nil {
			return nil, huma.Error404NotFound(err.Error())
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-research-budget", Method: http.MethodPut, Path: "/api/w/research-schedules/budget",
		Summary: "Set how many automatic research runs (schedules and discovery) the workspace may start per UTC day", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			DailyRunBudget int `json:"daily_run_budget" minimum:"0" maximum:"50"`
		}
	}) (*body[ResearchBudget], error) {
		s, err := scope(ctx, authz.ResearchRun)
		if err != nil {
			return nil, err
		}
		b, err := SetBudget(ctx, a.SQLDB, s.WorkspaceID, in.Body.DailyRunBudget)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return out(*b), nil
	})
}

func suggestionErr(err error) error {
	var se huma.StatusError
	switch {
	case errors.Is(err, ErrNoSuggestion):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, ErrAlreadyReview):
		return huma.Error409Conflict(err.Error())
	case errors.As(err, &se):
		return err
	}
	return huma.Error422UnprocessableEntity(err.Error())
}

// runCompanies are the suggestions a run's evidence names, whatever their status.
func runCompanies(ctx context.Context, a *app.App, ws, runID string) ([]CompanySuggestion, error) {
	rows, err := a.SQLDB.QueryContext(ctx, `SELECT `+suggestionColumns+` `+suggestionFrom+`
		WHERE s.workspace_id = $1 AND s.company_key IN (
			SELECT DISTINCT company_key FROM research_evidence WHERE workspace_id = $1 AND run_id = $2 AND company_key <> '')
		ORDER BY s.strongest DESC, s.name LIMIT 50`, ws, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CompanySuggestion{}
	for rows.Next() {
		s, err := scanSuggestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Summary1Line renders a run as one line for a notification or an MCP answer.
func (r ResearchRun) Summary1Line() string {
	when := ""
	if r.FinishedAt != nil {
		when = " (" + r.FinishedAt.Format(time.RFC3339) + ")"
	}
	return strings.TrimSpace(r.Topic) + ": " + r.Status + when
}
