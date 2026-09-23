package leads

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

func scope(ctx context.Context, actions ...string) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	for _, a := range actions {
		if !authz.Can(s.Role, a) {
			return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
		}
	}
	return s, nil
}

// httpErr maps the core errors onto responses.
func httpErr(err error, fallback string) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return huma.Error404NotFound("lead not found")
	case errors.Is(err, ErrAlreadyConverted):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, ErrStatus), errors.Is(err, ErrSpam):
		return huma.Error422UnprocessableEntity(err.Error())
	}
	return huma.Error500InternalServerError(fallback)
}

// LeadList is the Leads page's payload.
type LeadList struct {
	Leads  []Lead     `json:"leads"`
	Counts LeadCounts `json:"counts" doc:"How many leads the workspace has in each status (unfiltered)."`
}

// LeadOut wraps one lead.
type LeadOut struct {
	Lead Lead `json:"lead"`
}

// Register mounts the public inquiry endpoint and the workspace's Leads API.
// `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	registerPublicInquiry(api, a)

	tags := []string{"Leads"}

	huma.Register(api, huma.Operation{
		OperationID: "w-leads", Method: http.MethodGet, Path: "/api/w/leads",
		Summary: "Leads from the workspace's inquiry forms, newest first, with a count per status", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Status string `query:"status" enum:"new,contacted,qualified,converted,spam," doc:"Only leads in this status."`
		Q      string `query:"q" maxLength:"200" doc:"Matches name, email, company and message."`
	}) (*body[LeadList], error) {
		s, err := scope(ctx, authz.LeadsRead)
		if err != nil {
			return nil, err
		}
		list, counts, err := List(ctx, a.SQLDB, s.WorkspaceID, Filter{Status: in.Status, Q: in.Q})
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list leads")
		}
		return out(LeadList{list, counts}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-lead", Method: http.MethodGet, Path: "/api/w/leads/{id}",
		Summary: "One lead: the message, its attribution, and what it converted into", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[LeadOut], error) {
		s, err := scope(ctx, authz.LeadsRead)
		if err != nil {
			return nil, err
		}
		l, err := Get(ctx, a.SQLDB, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, httpErr(err, "could not read the lead")
		}
		return out(LeadOut{*l}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-lead", Method: http.MethodPatch, Path: "/api/w/leads/{id}",
		Summary: "Change a lead's status (new, contacted, qualified, spam — converted is reached by converting)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Status string `json:"status" enum:"new,contacted,qualified,spam"`
		}
	}) (*body[LeadOut], error) {
		s, err := scope(ctx, authz.LeadsWrite)
		if err != nil {
			return nil, err
		}
		l, err := SetStatus(ctx, a.SQLDB, s.WorkspaceID, in.ID, in.Body.Status)
		if err != nil {
			return nil, httpErr(err, "could not update the lead")
		}
		return out(LeadOut{*l}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-convert-lead", Method: http.MethodPost, Path: "/api/w/leads/{id}/convert",
		Summary: "Convert a lead: the contact (deduped on email), the company from the email domain (not for free mail), " +
			"a deal on the pipeline and the message as its first activity", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[Conversion], error) {
		// Converting creates CRM rows, so it needs the right to write them as
		// well as the right to work leads.
		s, err := scope(ctx, authz.LeadsWrite, authz.ContactsWrite)
		if err != nil {
			return nil, err
		}
		c, err := Convert(ctx, a.SQLDB, s.WorkspaceID, in.ID, s.UserID)
		if err != nil {
			if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrAlreadyConverted) && !errors.Is(err, ErrSpam) {
				a.Log.Error("lead conversion failed", "workspace", s.WorkspaceID, "err", err)
			}
			return nil, httpErr(err, "could not convert the lead")
		}
		return out(*c), nil
	})
}
