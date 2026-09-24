package icp

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// ProfileOut is the profile as the settings page and the MCP tools read it.
type ProfileOut struct {
	Profile    Profile    `json:"profile"`
	Configured bool       `json:"configured" doc:"False until the profile is saved once: the defaults are in use."`
	UpdatedAt  *time.Time `json:"updated_at"`
	UpdatedBy  *string    `json:"updated_by"`
	// The vocabularies, so a client never hard-codes them.
	Signals  []string `json:"signals" doc:"Every signal, in reading order."`
	Sizes    []string `json:"sizes" doc:"The company sizes the sizes criterion takes."`
	CanEdit  bool     `json:"can_edit" doc:"Whether the caller may change the profile (managers and up)."`
	Scored   int64    `json:"scored" doc:"Contacts with a score."`
	Contacts int64    `json:"contacts" doc:"Contacts in the workspace."`
}

func caller(ctx context.Context, action string) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, action) {
		return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
	}
	return s, nil
}

// ProfileFor reads the caller's workspace profile (REST and MCP).
func ProfileFor(ctx context.Context, db *sql.DB) (*ProfileOut, error) {
	s, err := caller(ctx, authz.ContactsRead)
	if err != nil {
		return nil, err
	}
	if err := Rescore(ctx, db, s.WorkspaceID); err != nil {
		slog.ErrorContext(ctx, "icp rescore", "workspace", s.WorkspaceID, "err", err)
	}
	sp, err := LoadProfile(ctx, db, s.WorkspaceID)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the profile")
	}
	out := &ProfileOut{Profile: sp.Profile, Configured: sp.Configured, UpdatedAt: sp.UpdatedAt, UpdatedBy: sp.UpdatedBy,
		Signals: Signals, Sizes: []string{"1-10", "11-50", "51-200", "201-1000", "1000+"}, CanEdit: authz.Can(s.Role, authz.PipelineManage)}
	if err := db.QueryRowContext(ctx, `SELECT count(sc.score), count(*) FROM contacts c LEFT JOIN icp_scores sc ON sc.workspace_id = c.workspace_id AND sc.contact_id = c.id WHERE c.workspace_id = $1`,
		s.WorkspaceID).Scan(&out.Scored, &out.Contacts); err != nil {
		return nil, huma.Error500InternalServerError("could not read the profile")
	}
	return out, nil
}

// SaveFor replaces the caller's workspace profile and rescores every contact
// and company by it before answering. Managers and up (pipeline:manage: the
// same people who shape the pipeline shape how the CRM ranks).
func SaveFor(ctx context.Context, db *sql.DB, p Profile) (*ProfileOut, error) {
	s, err := caller(ctx, authz.PipelineManage)
	if err != nil {
		return nil, err
	}
	n, err := p.Normalize()
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	if err := SaveProfile(ctx, db, s.WorkspaceID, s.UserID, n); err != nil {
		slog.ErrorContext(ctx, "save icp profile", "workspace", s.WorkspaceID, "err", err)
		return nil, huma.Error500InternalServerError("could not save the profile")
	}
	return ProfileFor(ctx, db)
}

type body[T any] struct{ Body T }

// Register mounts the profile routes. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	tags := []string{"CRM"}
	type ws struct {
		WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
	}
	huma.Register(api, huma.Operation{
		OperationID: "w-icp", Method: http.MethodGet, Path: "/api/w/icp",
		Summary: "The workspace's ideal-customer profile: the criteria and weights every contact and company is scored against", Tags: tags,
	}, func(ctx context.Context, _ *ws) (*body[ProfileOut], error) {
		out, err := ProfileFor(ctx, a.SQLDB)
		if err != nil {
			return nil, err
		}
		return &body[ProfileOut]{*out}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "w-put-icp", Method: http.MethodPut, Path: "/api/w/icp",
		Summary:     "Replace the ideal-customer profile and rescore every contact and company by it (managers and up)",
		Description: "Weights are -10 to 10: a negative weight is a penalty. A criterion with no values, or a weight of 0, is not scored.",
		Tags:        tags,
	}, func(ctx context.Context, in *struct {
		ws
		Body Profile
	}) (*body[ProfileOut], error) {
		out, err := SaveFor(ctx, a.SQLDB, in.Body)
		if err != nil {
			return nil, err
		}
		return &body[ProfileOut]{*out}, nil
	})
}
