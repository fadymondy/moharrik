package audit

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/paging"
	"github.com/fadymondy/moharrik/internal/tenant"
)

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

func httpErr(err error) error {
	var tooLarge *paging.TooLargeError
	switch {
	case errors.Is(err, tenant.ErrNotMember):
		return huma.Error404NotFound("workspace not found")
	case errors.Is(err, ErrForbidden):
		return huma.Error403Forbidden(err.Error())
	case errors.Is(err, paging.ErrBadCursor), errors.As(err, &tooLarge):
		return huma.Error422UnprocessableEntity(err.Error())
	}
	return huma.Error500InternalServerError("could not read the audit log")
}

// Register mounts the audit log's routes (Settings → Audit log). `a` may be nil
// (the OpenAPI export).
func Register(api huma.API, a *app.App) {
	tags := []string{"Audit"}

	huma.Register(api, huma.Operation{
		OperationID: "w-audit-log", Method: http.MethodGet, Path: "/api/w/audit",
		Summary: "The workspace's audit log, newest first (owners and admins)",
		Description: "Every write by a member, an MCP token, an OAuth client or an agent: who, what, when, from where, " +
			"and a before/after diff with secrets redacted. Follow `next_cursor` for older entries.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Actor      string    `query:"actor" maxLength:"200" doc:"An actor id (a user, token, OAuth grant or browser id)"`
		ActorKind  string    `query:"actor_kind" maxLength:"20" doc:"member | token | oauth | agent | admin"`
		EntityType string    `query:"entity_type" maxLength:"80"`
		EntityID   string    `query:"entity_id" maxLength:"200"`
		Action     string    `query:"action" maxLength:"120" doc:"create | update | delete, or an operation name"`
		From       time.Time `query:"from" doc:"On or after (RFC 3339)"`
		To         time.Time `query:"to" doc:"Before (RFC 3339)"`
		Cursor     string    `query:"cursor"`
		Limit      int       `query:"limit" minimum:"0" maximum:"200" doc:"AuditPage size (default 50, at most 200)"`
	}) (*struct{ Body AuditPage }, error) {
		f := Filter{Actor: in.Actor, ActorKind: in.ActorKind, EntityType: in.EntityType, EntityID: in.EntityID,
			Action: in.Action, Cursor: in.Cursor, Limit: in.Limit}
		if !in.From.IsZero() {
			f.From = &in.From
		}
		if !in.To.IsZero() {
			f.To = &in.To
		}
		page, err := List(ctx, a.SQLDB, f)
		if err != nil {
			return nil, httpErr(err)
		}
		return &struct{ Body AuditPage }{*page}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-audit-facets", Method: http.MethodGet, Path: "/api/w/audit/facets",
		Summary: "Who and what appears in the audit log (the page's filters), and the retention window",
		Tags:    tags,
	}, func(ctx context.Context, _ *wsHeader) (*struct{ Body AuditFacets }, error) {
		f, err := GetFacets(ctx, a.SQLDB)
		if err != nil {
			return nil, httpErr(err)
		}
		return &struct{ Body AuditFacets }{*f}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-audit-retention", Method: http.MethodPut, Path: "/api/w/audit/retention",
		Summary: "How many days the audit log is kept (30 to 3650; default 365)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Days int `json:"retention_days" minimum:"30" maximum:"3650"`
		}
	}) (*struct {
		Body struct {
			Days int `json:"retention_days"`
		}
	}, error) {
		days, err := SetRetention(ctx, a.SQLDB, in.Body.Days)
		if err != nil {
			if errors.Is(err, ErrForbidden) || errors.Is(err, tenant.ErrNotMember) {
				return nil, httpErr(err)
			}
			return nil, huma.Error500InternalServerError("could not save the retention window")
		}
		out := &struct {
			Body struct {
				Days int `json:"retention_days"`
			}
		}{}
		out.Body.Days = days
		return out, nil
	})
}
