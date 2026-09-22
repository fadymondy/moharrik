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

// Register mounts the research endpoints (MH-227).
func Register(api huma.API, a *app.App, engine *Engine) {
	huma.Register(api, huma.Operation{
		OperationID: "w-research-runs", Method: http.MethodGet, Path: "/api/w/research",
		Summary: "Past research runs, newest first", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Limit int `query:"limit" minimum:"1" maximum:"100" doc:"Default 25."`
	}) (*body[struct {
		Runs []ResearchRun `json:"runs"`
		// What this deployment can actually do, so the UI can say "no reader
		// is configured" instead of showing runs that never fetch anything.
		CanSearch    bool `json:"can_search"`
		CanRead      bool `json:"can_read"`
		CanSummarise bool `json:"can_summarise"`
	}], error) {
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
		return out(struct {
			Runs         []ResearchRun `json:"runs"`
			CanSearch    bool          `json:"can_search"`
			CanRead      bool          `json:"can_read"`
			CanSummarise bool          `json:"can_summarise"`
		}{runs, engine.Search != nil, engine.Read != nil, engine.AI != nil}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-research-run", Method: http.MethodGet, Path: "/api/w/research/{id}",
		Summary: "One research run, with the sources behind its answer", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
		// The fetched page text, which is large. Off by default: the answer
		// and the links are what a page needs.
		WithContent bool `query:"content" doc:"Include the fetched page text of each source."`
	}) (*body[struct {
		ResearchRun ResearchRun      `json:"run"`
		Sources     []ResearchSource `json:"sources"`
	}], error) {
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
		return out(struct {
			ResearchRun ResearchRun      `json:"run"`
			Sources     []ResearchSource `json:"sources"`
		}{*run, sources}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-research-start", Method: http.MethodPost, Path: "/api/w/research",
		Summary: "Ask a question: search the web, read the results, and summarise them", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Topic string `json:"topic" minLength:"3" maxLength:"500" doc:"The question, in your own words."`
		}
	}) (*body[struct {
		RunID string `json:"run_id"`
	}], error) {
		s, err := scope(ctx, authz.ResearchRun)
		if err != nil {
			return nil, err
		}

		runID, err := engine.Start(ctx, a, s.WorkspaceID, s.UserID, in.Body.Topic)
		switch {
		case errors.Is(err, ErrBusy):
			return nil, huma.Error409Conflict(err.Error())
		case errors.Is(err, ErrNoSearch):
			// 501, not 500: the request was fine and the deployment cannot do
			// it. A 500 would send someone looking for a bug.
			return nil, huma.Error501NotImplemented(err.Error())
		case err != nil:
			return nil, huma.Error422UnprocessableEntity(err.Error())
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
}

// Summary renders a run as one line for a notification or an MCP answer.
func (r ResearchRun) Summary1Line() string {
	when := ""
	if r.FinishedAt != nil {
		when = " (" + r.FinishedAt.Format(time.RFC3339) + ")"
	}
	return strings.TrimSpace(r.Topic) + ": " + r.Status + when
}
