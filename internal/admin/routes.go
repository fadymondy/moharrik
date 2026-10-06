package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/clientip"
	"github.com/fadymondy/moharrik/internal/platform"
)

type body[T any] struct{ Body T }

type AdminUserPath struct {
	ID string `path:"id" doc:"User id or email"`
}

type AdminWorkspacePath struct {
	ID string `path:"id" doc:"Workspace id or slug"`
}

type AdminMemberPath struct {
	ID   string `path:"id" doc:"Workspace id or slug"`
	User string `path:"user" doc:"User id or email"`
}

type AdminReasonBody struct {
	Reason string `json:"reason,omitempty" maxLength:"500" doc:"Shown to the account or workspace"`
}

var tags = []string{"Platform admin"}

// Register mounts GET /api/me/platform and the /api/admin/* panel API. The guard's admin tier
// already answers 404 to anyone who is not a signed-in platform admin; each handler checks again,
// from the database rather than the session's claims. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	sv := func() *Service { return &Service{DB: a.SysDB} }

	// actor is the platform admin making the request, or 404.
	actor := func(ctx context.Context) (Actor, error) {
		id, ok := auth.IdentityFrom(ctx)
		if !ok || id == nil {
			return Actor{}, huma.Error404NotFound("not found")
		}
		u, err := platform.Load(ctx, a.SysDB, id.ID)
		if err != nil || !u.Admin || u.Suspended() {
			return Actor{}, huma.Error404NotFound("not found")
		}
		return Actor{ID: u.ID, Email: u.Email, IP: clientip.From(ctx), Via: "rest"}, nil
	}

	// fail hands huma errors through and logs the rest as a 500.
	fail := func(op string, err error) error {
		var se huma.StatusError
		if errors.As(err, &se) {
			return err
		}
		a.Log.Error("admin "+op+" failed", "err", err)
		return huma.Error500InternalServerError("could not " + op)
	}

	huma.Register(api, huma.Operation{
		OperationID: "me-platform", Method: http.MethodGet, Path: "/api/me/platform",
		Summary: "Whether the signed-in account is a platform admin (shows the admin panel link)", Tags: []string{"Me"},
	}, func(ctx context.Context, _ *struct{}) (*body[struct {
		IsAdmin bool `json:"is_admin"`
	}], error) {
		out := &body[struct {
			IsAdmin bool `json:"is_admin"`
		}]{}
		if id, ok := auth.IdentityFrom(ctx); ok && id != nil {
			if u, err := platform.Load(ctx, a.SysDB, id.ID); err == nil {
				out.Body.IsAdmin = u.Admin && !u.Suspended()
			}
		}
		return out, nil
	})

	// ── Analytics ──
	huma.Register(api, huma.Operation{
		OperationID: "admin-stats", Method: http.MethodGet, Path: "/api/admin/stats",
		Summary: "Platform overview: users, workspaces, records and 30-day series", Tags: tags,
	}, func(ctx context.Context, _ *struct{}) (*body[PlatformStats], error) {
		if _, err := actor(ctx); err != nil {
			return nil, err
		}
		s, err := sv().Stats(ctx)
		if err != nil {
			return nil, fail("read the stats", err)
		}
		return &body[PlatformStats]{*s}, nil
	})

	// ── Users ──
	huma.Register(api, huma.Operation{
		OperationID: "admin-users", Method: http.MethodGet, Path: "/api/admin/users",
		Summary: "Every account, newest first", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Q      string `query:"q" doc:"Matches the email"`
		Status string `query:"status" enum:"active,suspended,admin," doc:"Filter"`
		Limit  int    `query:"limit" minimum:"0" maximum:"1000"`
	}) (*body[[]AdminUser], error) {
		if _, err := actor(ctx); err != nil {
			return nil, err
		}
		out, err := sv().ListUsers(ctx, UserFilter{Q: in.Q, Status: in.Status, Limit: in.Limit})
		if err != nil {
			return nil, fail("list the users", err)
		}
		return &body[[]AdminUser]{out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-user", Method: http.MethodGet, Path: "/api/admin/users/{id}",
		Summary: "One account with its workspaces", Tags: tags,
	}, func(ctx context.Context, in *AdminUserPath) (*body[AdminUserDetail], error) {
		if _, err := actor(ctx); err != nil {
			return nil, err
		}
		d, err := sv().GetUser(ctx, in.ID)
		if err != nil {
			return nil, fail("read the user", err)
		}
		return &body[AdminUserDetail]{*d}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-user-suspend", Method: http.MethodPost, Path: "/api/admin/users/{id}/suspend",
		Summary: "Suspend an account: sign-in is refused and every call answers 403", Tags: tags,
	}, func(ctx context.Context, in *struct {
		AdminUserPath
		Body AdminReasonBody
	}) (*body[AdminUserDetail], error) {
		ac, err := actor(ctx)
		if err != nil {
			return nil, err
		}
		d, err := sv().SuspendUser(ctx, ac, in.ID, in.Body.Reason)
		if err != nil {
			return nil, fail("suspend the user", err)
		}
		return &body[AdminUserDetail]{*d}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-user-reactivate", Method: http.MethodPost, Path: "/api/admin/users/{id}/reactivate",
		Summary: "Lift an account's suspension", Tags: tags,
	}, func(ctx context.Context, in *AdminUserPath) (*body[AdminUserDetail], error) {
		ac, err := actor(ctx)
		if err != nil {
			return nil, err
		}
		d, err := sv().ReactivateUser(ctx, ac, in.ID)
		if err != nil {
			return nil, fail("reactivate the user", err)
		}
		return &body[AdminUserDetail]{*d}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-user-sign-out", Method: http.MethodPost, Path: "/api/admin/users/{id}/sign-out",
		Summary: "End every session the account has", Tags: tags,
	}, func(ctx context.Context, in *AdminUserPath) (*body[AdminUserDetail], error) {
		ac, err := actor(ctx)
		if err != nil {
			return nil, err
		}
		d, err := sv().SignOut(ctx, ac, in.ID)
		if err != nil {
			return nil, fail("sign the user out", err)
		}
		return &body[AdminUserDetail]{*d}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-user-set-admin", Method: http.MethodPut, Path: "/api/admin/users/{id}/admin",
		Summary: "Grant or revoke platform admin", Tags: tags,
	}, func(ctx context.Context, in *struct {
		AdminUserPath
		Body struct {
			Admin bool `json:"admin"`
		}
	}) (*body[AdminUserDetail], error) {
		ac, err := actor(ctx)
		if err != nil {
			return nil, err
		}
		d, err := sv().SetAdmin(ctx, ac, in.ID, in.Body.Admin)
		if err != nil {
			return nil, fail("change platform admin", err)
		}
		return &body[AdminUserDetail]{*d}, nil
	})

	// ── Workspaces ──
	huma.Register(api, huma.Operation{
		OperationID: "admin-workspaces", Method: http.MethodGet, Path: "/api/admin/workspaces",
		Summary: "Every workspace with its owner, members and activity", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Q      string `query:"q" doc:"Matches the name or slug"`
		Status string `query:"status" enum:"active,suspended," doc:"Filter"`
		Limit  int    `query:"limit" minimum:"0" maximum:"1000"`
	}) (*body[[]AdminWorkspace], error) {
		if _, err := actor(ctx); err != nil {
			return nil, err
		}
		out, err := sv().ListWorkspaces(ctx, WorkspaceFilter{Q: in.Q, Status: in.Status, Limit: in.Limit})
		if err != nil {
			return nil, fail("list the workspaces", err)
		}
		return &body[[]AdminWorkspace]{out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-workspace", Method: http.MethodGet, Path: "/api/admin/workspaces/{id}",
		Summary: "One workspace with its members and record counts", Tags: tags,
	}, func(ctx context.Context, in *AdminWorkspacePath) (*body[AdminWorkspaceDetail], error) {
		if _, err := actor(ctx); err != nil {
			return nil, err
		}
		d, err := sv().GetWorkspace(ctx, in.ID)
		if err != nil {
			return nil, fail("read the workspace", err)
		}
		return &body[AdminWorkspaceDetail]{*d}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-workspace-suspend", Method: http.MethodPost, Path: "/api/admin/workspaces/{id}/suspend",
		Summary: "Suspend a workspace: its members get 403, its data is kept", Tags: tags,
	}, func(ctx context.Context, in *struct {
		AdminWorkspacePath
		Body AdminReasonBody
	}) (*body[AdminWorkspaceDetail], error) {
		ac, err := actor(ctx)
		if err != nil {
			return nil, err
		}
		d, err := sv().SuspendWorkspace(ctx, ac, in.ID, in.Body.Reason)
		if err != nil {
			return nil, fail("suspend the workspace", err)
		}
		return &body[AdminWorkspaceDetail]{*d}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-workspace-reactivate", Method: http.MethodPost, Path: "/api/admin/workspaces/{id}/reactivate",
		Summary: "Lift a workspace's suspension", Tags: tags,
	}, func(ctx context.Context, in *AdminWorkspacePath) (*body[AdminWorkspaceDetail], error) {
		ac, err := actor(ctx)
		if err != nil {
			return nil, err
		}
		d, err := sv().ReactivateWorkspace(ctx, ac, in.ID)
		if err != nil {
			return nil, fail("reactivate the workspace", err)
		}
		return &body[AdminWorkspaceDetail]{*d}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-workspace-plan", Method: http.MethodPut, Path: "/api/admin/workspaces/{id}/plan",
		Summary: "Set a workspace's plan label", Tags: tags,
	}, func(ctx context.Context, in *struct {
		AdminWorkspacePath
		Body struct {
			Plan string `json:"plan" minLength:"1" maxLength:"40"`
		}
	}) (*body[AdminWorkspaceDetail], error) {
		ac, err := actor(ctx)
		if err != nil {
			return nil, err
		}
		d, err := sv().SetPlan(ctx, ac, in.ID, in.Body.Plan)
		if err != nil {
			return nil, fail("change the plan", err)
		}
		return &body[AdminWorkspaceDetail]{*d}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-workspace-delete", Method: http.MethodDelete, Path: "/api/admin/workspaces/{id}",
		Summary: "Delete a workspace and everything in it. confirm must be its slug", Tags: tags,
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		AdminWorkspacePath
		Confirm string `query:"confirm" required:"true" doc:"The workspace's slug"`
	}) (*struct{}, error) {
		ac, err := actor(ctx)
		if err != nil {
			return nil, err
		}
		if err := sv().DeleteWorkspace(ctx, ac, in.ID, in.Confirm); err != nil {
			return nil, fail("delete the workspace", err)
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-workspace-member-add", Method: http.MethodPost, Path: "/api/admin/workspaces/{id}/members",
		Summary: "Add an existing account to a workspace", Tags: tags,
	}, func(ctx context.Context, in *struct {
		AdminWorkspacePath
		Body struct {
			User string `json:"user" minLength:"1" doc:"User id or email"`
			Role string `json:"role" enum:"owner,admin,manager,member,client"`
		}
	}) (*body[AdminWorkspaceDetail], error) {
		ac, err := actor(ctx)
		if err != nil {
			return nil, err
		}
		d, err := sv().AddMember(ctx, ac, in.ID, in.Body.User, in.Body.Role)
		if err != nil {
			return nil, fail("add the member", err)
		}
		return &body[AdminWorkspaceDetail]{*d}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-workspace-member-role", Method: http.MethodPut, Path: "/api/admin/workspaces/{id}/members/{user}",
		Summary: "Change a member's role", Tags: tags,
	}, func(ctx context.Context, in *struct {
		AdminMemberPath
		Body struct {
			Role string `json:"role" enum:"owner,admin,manager,member,client"`
		}
	}) (*body[AdminWorkspaceDetail], error) {
		ac, err := actor(ctx)
		if err != nil {
			return nil, err
		}
		d, err := sv().SetMemberRole(ctx, ac, in.ID, in.User, in.Body.Role)
		if err != nil {
			return nil, fail("change the role", err)
		}
		return &body[AdminWorkspaceDetail]{*d}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-workspace-member-remove", Method: http.MethodDelete, Path: "/api/admin/workspaces/{id}/members/{user}",
		Summary: "Remove a member from a workspace", Tags: tags,
	}, func(ctx context.Context, in *AdminMemberPath) (*body[AdminWorkspaceDetail], error) {
		ac, err := actor(ctx)
		if err != nil {
			return nil, err
		}
		d, err := sv().RemoveMember(ctx, ac, in.ID, in.User)
		if err != nil {
			return nil, fail("remove the member", err)
		}
		return &body[AdminWorkspaceDetail]{*d}, nil
	})

	// ── PlatformPermissions + audit ──
	huma.Register(api, huma.Operation{
		OperationID: "admin-permissions", Method: http.MethodGet, Path: "/api/admin/permissions",
		Summary: "The workspace role × action matrix", Tags: tags,
	}, func(ctx context.Context, _ *struct{}) (*body[PlatformPermissions], error) {
		if _, err := actor(ctx); err != nil {
			return nil, err
		}
		return &body[PlatformPermissions]{PermissionMatrix()}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "admin-audit", Method: http.MethodGet, Path: "/api/admin/audit",
		Summary: "What platform admins did, newest first", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Q      string `query:"q" doc:"Matches the admin, action or target"`
		Target string `query:"target" doc:"A user or workspace id"`
		Limit  int    `query:"limit" minimum:"0" maximum:"1000"`
	}) (*body[[]AdminAuditEntry], error) {
		if _, err := actor(ctx); err != nil {
			return nil, err
		}
		out, err := sv().Audit(ctx, AuditFilter{Q: in.Q, Target: in.Target, Limit: in.Limit})
		if err != nil {
			return nil, fail("read the audit log", err)
		}
		return &body[[]AdminAuditEntry]{out}, nil
	})
}
