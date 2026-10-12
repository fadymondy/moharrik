package prospects

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/research"
	"github.com/fadymondy/moharrik/internal/tenant"
)

var tags = []string{"Prospects"}

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

// ProspectProfileOut is the profile with what the deployment can do and today's budget.
type ProspectProfileOut struct {
	Profile      *ProspectProfile         `json:"profile"`
	CanSearch    bool                     `json:"can_search"`
	CanSummarise bool                     `json:"can_summarise" doc:"false: drafts are templates, not written by a model."`
	Budget       *research.ResearchBudget `json:"budget"`
	Provider     *research.Provider       `json:"provider" doc:"Who does the research: the server, or the caller's AI agent over MCP."`
}

// ProspectList is a page of prospects with the counts per status.
type ProspectList struct {
	Prospects []Prospect     `json:"prospects"`
	Counts    map[string]int `json:"counts"`
}

func errFor(err error) error {
	var se huma.StatusError
	switch {
	case errors.Is(err, ErrNoProspect):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, ErrDecided), errors.Is(err, research.ErrBusy):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, research.ErrNoSearch), errors.Is(err, research.ErrNoReader):
		return huma.Error501NotImplemented(err.Error())
	case errors.As(err, &se):
		return err
	}
	return huma.Error422UnprocessableEntity(err.Error())
}

// Register mounts the prospect endpoints (MH-397).
func Register(api huma.API, a *app.App, engine *research.Engine) {
	profileOut := func(ctx context.Context, ws string) (*body[ProspectProfileOut], error) {
		p, err := GetProfile(ctx, a.SQLDB, ws)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the discovery profile")
		}
		b, err := research.GetBudget(ctx, a.SQLDB, ws)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the research budget")
		}
		prov, err := engine.GetProvider(ctx, a.SQLDB, ws)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the research provider")
		}
		caps := engine.Caps()
		return out(ProspectProfileOut{Profile: p, CanSearch: caps.CanSearch, CanSummarise: caps.CanSummarise, Budget: b, Provider: prov}), nil
	}

	huma.Register(api, huma.Operation{
		OperationID: "w-prospect-profile", Method: http.MethodGet, Path: "/api/w/prospect-profile",
		Summary: "The ideal customer discovery looks for, what it will search, and whether it can", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[ProspectProfileOut], error) {
		s, err := scope(ctx, authz.ResearchRead)
		if err != nil {
			return nil, err
		}
		return profileOut(ctx, s.WorkspaceID)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-prospect-profile-save", Method: http.MethodPut, Path: "/api/w/prospect-profile",
		Summary: "Change the ideal customer (and turn scheduled discovery on or off); waiting prospects are rescored", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body ProspectProfileInput
	}) (*body[ProspectProfileOut], error) {
		s, err := scope(ctx, authz.ResearchRun)
		if err != nil {
			return nil, err
		}
		if _, err := SaveProfile(ctx, a.SQLDB, s.WorkspaceID, s.UserID, in.Body); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return profileOut(ctx, s.WorkspaceID)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-prospect-discover", Method: http.MethodPost, Path: "/api/w/prospect-profile/discover",
		Summary:     "Run one discovery pass now: the profile's next query, as a research run",
		Description: "Does not spend the daily budget (a person asked). When the workspace's research is done by its AI assistant " +
			"(provider agent), the pass is queued as a task for the assistant (opportunity_tasks over MCP) and task_id is set instead of run_id. " +
			"409 when research is already going.",
		Tags:        tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[DiscoverResult], error) {
		s, err := scope(ctx, authz.ResearchRun)
		if err != nil {
			return nil, err
		}
		res, err := DiscoverNow(ctx, a, engine, s.WorkspaceID, s.UserID)
		if err != nil {
			return nil, errFor(err)
		}
		return out(*res), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-prospects", Method: http.MethodGet, Path: "/api/w/prospects",
		Summary: "Prospects discovery found, best first, with every point of the score explained", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Status   string  `query:"status" enum:",new,accepted,rejected,all" doc:"Default new."`
		Q        string  `query:"q" maxLength:"120"`
		MinScore float64 `query:"min_score" minimum:"0" maximum:"100"`
		Limit    int     `query:"limit" minimum:"1" maximum:"100"`
	}) (*body[ProspectList], error) {
		s, err := scope(ctx, authz.ResearchRead)
		if err != nil {
			return nil, err
		}
		list, counts, err := List(ctx, a.SQLDB, s.WorkspaceID, Query{Status: in.Status, Q: in.Q, MinScore: in.MinScore, Limit: in.Limit})
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the prospects")
		}
		return out(ProspectList{Prospects: list, Counts: counts}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-prospects-digest", Method: http.MethodGet, Path: "/api/w/prospects/digest",
		Summary: "Prospects found since the last digest (read-only; POST marks them seen)", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[ProspectDigest], error) {
		s, err := scope(ctx, authz.ResearchRead)
		if err != nil {
			return nil, err
		}
		d, err := TakeDigest(ctx, a.SQLDB, s.WorkspaceID, false)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the digest")
		}
		return out(*d), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-prospects-digest-mark", Method: http.MethodPost, Path: "/api/w/prospects/digest",
		Summary: "Take the digest: the prospects found since the last one, and move the mark to now", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[ProspectDigest], error) {
		s, err := scope(ctx, authz.ResearchRead)
		if err != nil {
			return nil, err
		}
		d, err := TakeDigest(ctx, a.SQLDB, s.WorkspaceID, true)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not take the digest")
		}
		return out(*d), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-prospect", Method: http.MethodGet, Path: "/api/w/prospects/{id}",
		Summary: "One prospect: its score and reasons, the person the pages name, its signal timeline and its drafts", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[ProspectDetail], error) {
		s, err := scope(ctx, authz.ResearchRead)
		if err != nil {
			return nil, err
		}
		d, err := Get(ctx, a.SQLDB, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, errFor(err)
		}
		return out(*d), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-prospect-accept", Method: http.MethodPost, Path: "/api/w/prospects/{id}/accept",
		Summary: "Accept: add the company, the contact (given an address) and a deal to the CRM", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body ProspectAcceptInput
	}) (*body[ProspectAccepted], error) {
		s, err := scope(ctx, authz.ContactsWrite)
		if err != nil {
			return nil, err
		}
		res, err := Accept(ctx, a.SQLDB, s.WorkspaceID, s.UserID, in.ID, in.Body)
		if err != nil {
			return nil, errFor(err)
		}
		return out(*res), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-prospect-reject", Method: http.MethodPost, Path: "/api/w/prospects/{id}/reject",
		Summary: "Reject: similar prospects are ranked lower from now on, and the reason says so", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Reason string `json:"reason,omitempty" maxLength:"500"`
		}
	}) (*body[Prospect], error) {
		s, err := scope(ctx, authz.ContactsWrite)
		if err != nil {
			return nil, err
		}
		p, err := Reject(ctx, a.SQLDB, s.WorkspaceID, s.UserID, in.ID, in.Body.Reason)
		if err != nil {
			return nil, errFor(err)
		}
		return out(*p), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-prospect-draft", Method: http.MethodPost, Path: "/api/w/prospects/{id}/draft",
		Summary:     "Draft outreach to this prospect, in English or Arabic. A draft only: Moharrik never sends it",
		Description: "Written by the model's draft task when one is configured; otherwise a template built from the strongest evidence, marked generator=template.",
		Tags:        tags, DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Locale string `json:"locale" enum:"en,ar"`
		}
	}) (*body[ProspectDraft], error) {
		s, err := scope(ctx, authz.ContactsWrite)
		if err != nil {
			return nil, err
		}
		d, err := MakeDraft(ctx, a.SQLDB, engine.AI, s.WorkspaceID, s.UserID, in.ID, in.Body.Locale)
		if err != nil {
			return nil, errFor(err)
		}
		return out(*d), nil
	})
}
