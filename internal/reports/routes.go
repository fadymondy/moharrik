package reports

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

type body[T any] struct{ Body T }

// may is whether the caller may read an area. A browser session goes by the
// role. An MCP token is narrower than its member: it reads an area only if it
// was also granted that area's scope — a token minted to read the CRM does not
// read leads or customers' messages because its member could (the rule
// internal/crm's canReadInbox set for MH-393).
func may(s *tenant.Scope, action string) bool {
	if !authz.Can(s.Role, action) {
		return false
	}
	if s.Via == "mcp" {
		for _, sc := range s.Scopes {
			if sc == action {
				return true
			}
		}
		return false
	}
	return true
}

// CRMFor is the report for the caller's workspace, with the sections the
// caller may read. REST and MCP both answer from it. Errors are huma status
// errors.
func CRMFor(ctx context.Context, db *sql.DB, r Range, now time.Time) (*CRMReport, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, authz.ContactsRead) {
		return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
	}
	rep, err := BuildCRM(ctx, db, s.WorkspaceID, Access{Leads: may(s, authz.LeadsRead), Inbox: may(s, authz.InboxRead)}, r, now)
	switch {
	case errors.Is(err, ErrBadRange):
		return nil, huma.Error422UnprocessableEntity(strings.TrimPrefix(err.Error(), ErrBadRange.Error()+": "))
	case errors.Is(err, ErrNotFound):
		return nil, huma.Error404NotFound("workspace not found")
	case err != nil:
		return nil, huma.Error500InternalServerError("could not build the report")
	}
	return rep, nil
}

// Register mounts GET /api/w/reports/crm. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-report-crm", Method: http.MethodGet, Path: "/api/w/reports/crm",
		Summary: "CRM report: pipeline value, win rate and deal size, stage conversion, won/lost over time, lead sources and inbox response time",
		Description: "Over a range of days in the workspace's time zone (default: the last 90 days, at most 731). Money is in the " +
			"workspace's currency only; deals in another currency are counted separately, never converted. lead_sources is null " +
			"without leads:read and inbox without inbox:read.",
		Tags: []string{"Reports"},
	}, func(ctx context.Context, in *struct {
		WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
		From        string `query:"from" doc:"First day, YYYY-MM-DD (default: 89 days before to)"`
		To          string `query:"to" doc:"Last day, inclusive, YYYY-MM-DD (default: today)"`
		Days        int    `query:"days" minimum:"0" maximum:"731" doc:"Without from: the window of this many days ending on to (default 90)"`
	}) (*body[CRMReport], error) {
		rep, err := CRMFor(ctx, a.SQLDB, Range{From: in.From, To: in.To, Days: in.Days}, time.Now())
		if err != nil {
			return nil, err
		}
		return &body[CRMReport]{*rep}, nil
	})
}
