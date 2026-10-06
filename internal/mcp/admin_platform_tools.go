package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fadymondy/moharrik/internal/admin"
	"github.com/fadymondy/moharrik/internal/app"
)

// The superadmin panel's actions over the admin MCP (MH-1184). They call the same internal/admin
// functions as /api/admin/*, so a change made here lands in admin_audit with via=mcp.

func adminActor(ctx context.Context) admin.Actor {
	c := callerFrom(ctx)
	return admin.Actor{ID: c.UserID, Email: c.Email, Via: "mcp"}
}

func adminJSON(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	return string(b), err
}

func adminPlatformTools() []Tool {
	userRef := prop("string", "User id or email")
	wsRef := prop("string", "Workspace slug or id")
	reason := prop("string", "Optional; shown to the account or workspace")
	sv := func(a *app.App) *admin.Service { return &admin.Service{DB: a.SysDB} }
	userLine := func(d *admin.AdminUserDetail, verb string) string {
		return fmt.Sprintf("%s %s (status=%s, platform admin=%t).", verb, d.Email, d.Status, d.IsAdmin)
	}
	wsLine := func(d *admin.AdminWorkspaceDetail, verb string) string {
		return fmt.Sprintf("%s %s (status=%s).", verb, d.Slug, d.Status)
	}
	return []Tool{
		{
			Name:        "platform_stats",
			Description: "Platform analytics: users (total, new, active 7/30 days, admins, suspended), workspaces, record counts, member roles, 30-day daily series and the most active workspaces. JSON.",
			Scope:       "admin:platform:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, err := sv(a).Stats(ctx)
				if err != nil {
					return "", err
				}
				return adminJSON(s)
			},
		},
		{
			Name:        "admin_audit",
			Description: "What platform admins did (panel and admin MCP), newest first. JSON.",
			Scope:       "admin:platform:read",
			InputSchema: schema(nil, map[string]any{
				"q": prop("string", "Matches the admin, action or target"), "target": prop("string", "A user or workspace id"),
				"limit": prop("integer", "Default 50"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				out, err := sv(a).Audit(ctx, admin.AuditFilter{Q: str(args, "q"), Target: str(args, "target"), Limit: intArg(args, "limit", 50)})
				if err != nil {
					return "", err
				}
				if len(out) == 0 {
					return "No admin actions matched.", nil
				}
				return adminJSON(out)
			},
		},
		{
			Name:        "list_permissions",
			Description: "The workspace role × action matrix: for every action, the lowest role that may perform it.",
			Scope:       "admin:platform:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				p := admin.PermissionMatrix()
				var b strings.Builder
				fmt.Fprintf(&b, "Roles, highest first: %s\n\n", strings.Join(p.Roles, " > "))
				for _, x := range p.Actions {
					fmt.Fprintf(&b, "%-28s %s\n", x.Action, firstNonEmpty(x.MinRole, "(no role)"))
				}
				return b.String(), nil
			},
		},
		{
			Name:        "suspend_user",
			Description: "Suspend an account: sign-in is refused, every call answers 403 and its MCP tokens stop working. Data is kept.",
			Scope:       "admin:users:write",
			InputSchema: schema([]string{"user"}, map[string]any{"user": userRef, "reason": reason}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				d, err := sv(a).SuspendUser(ctx, adminActor(ctx), str(args, "user"), str(args, "reason"))
				if err != nil {
					return "", err
				}
				return userLine(d, "Suspended"), nil
			},
		},
		{
			Name:        "reactivate_user",
			Description: "Lift an account's suspension.",
			Scope:       "admin:users:write",
			InputSchema: schema([]string{"user"}, map[string]any{"user": userRef}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				d, err := sv(a).ReactivateUser(ctx, adminActor(ctx), str(decodeArgs(raw), "user"))
				if err != nil {
					return "", err
				}
				return userLine(d, "Reactivated"), nil
			},
		},
		{
			Name:        "sign_out_user",
			Description: "End every browser session an account has; they must sign in again.",
			Scope:       "admin:users:write",
			InputSchema: schema([]string{"user"}, map[string]any{"user": userRef}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				d, err := sv(a).SignOut(ctx, adminActor(ctx), str(decodeArgs(raw), "user"))
				if err != nil {
					return "", err
				}
				return userLine(d, "Signed out"), nil
			},
		},
		{
			Name:        "set_platform_admin",
			Description: "Grant or revoke platform admin. You cannot revoke your own.",
			Scope:       "admin:users:write",
			InputSchema: schema([]string{"user", "admin"}, map[string]any{"user": userRef, "admin": prop("boolean", "true to grant, false to revoke")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				on, ok := args["admin"].(bool)
				if !ok {
					return "", fmt.Errorf("admin must be true or false")
				}
				d, err := sv(a).SetAdmin(ctx, adminActor(ctx), str(args, "user"), on)
				if err != nil {
					return "", err
				}
				return userLine(d, "Updated"), nil
			},
		},
		{
			Name:        "suspend_workspace",
			Description: "Suspend a workspace: its members get 403 and its tokens stop working. Data is kept.",
			Scope:       "admin:workspaces:write",
			InputSchema: schema([]string{"workspace"}, map[string]any{"workspace": wsRef, "reason": reason}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				d, err := sv(a).SuspendWorkspace(ctx, adminActor(ctx), str(args, "workspace"), str(args, "reason"))
				if err != nil {
					return "", err
				}
				return wsLine(d, "Suspended"), nil
			},
		},
		{
			Name:        "reactivate_workspace",
			Description: "Lift a workspace's suspension.",
			Scope:       "admin:workspaces:write",
			InputSchema: schema([]string{"workspace"}, map[string]any{"workspace": wsRef}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				d, err := sv(a).ReactivateWorkspace(ctx, adminActor(ctx), str(decodeArgs(raw), "workspace"))
				if err != nil {
					return "", err
				}
				return wsLine(d, "Reactivated"), nil
			},
		},
		{
			Name:        "delete_workspace",
			Description: "Delete a workspace and EVERYTHING in it. Irreversible. confirm must equal the workspace's slug.",
			Scope:       "admin:workspaces:write",
			InputSchema: schema([]string{"workspace", "confirm"}, map[string]any{"workspace": wsRef, "confirm": prop("string", "The workspace's slug, typed again")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				if err := sv(a).DeleteWorkspace(ctx, adminActor(ctx), str(args, "workspace"), str(args, "confirm")); err != nil {
					return "", err
				}
				return "Deleted " + str(args, "confirm") + ".", nil
			},
		},
	}
}
