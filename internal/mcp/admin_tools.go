package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/workspaces"
)

/*
AdminTools is the platform admin MCP registry (/api/admin/mcp).

Workspaces and users: inspection and membership management. Moharrik files its
own problem reports to Mahaam (internal/feedback), so there are no issue tools.

All of these run on the owner pool (they are cross-tenant by definition), so
every query names its workspace or user explicitly.
*/
func AdminTools() []Tool {
	roleEnum := map[string]any{"type": "string", "enum": []string{"owner", "admin", "manager", "member", "client"}}
	return []Tool{
		// ---- workspaces ----------------------------------------------------
		{
			Name:        "list_workspaces",
			Description: "List workspaces with their member counts. Optional name/slug search.",
			Scope:       "admin:workspaces:read",
			InputSchema: schema(nil, map[string]any{"q": prop("string", "Name or slug contains"), "limit": prop("integer", "Default 50")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				rows, err := a.SysDB.QueryContext(ctx, `
					SELECT w.slug, w.name, w.plan, w.created_at,
					       (SELECT count(*) FROM workspace_members m WHERE m.workspace_id = w.id)
					FROM workspaces w
					WHERE ($1 = '' OR w.name ILIKE '%' || $1 || '%' OR w.slug ILIKE '%' || $1 || '%')
					ORDER BY w.created_at DESC LIMIT $2`, str(args, "q"), intArg(args, "limit", 50))
				if err != nil {
					return "", err
				}
				defer rows.Close()
				var b strings.Builder
				n := 0
				for rows.Next() {
					var slug, name, plan string
					var created time.Time
					var members int
					if err := rows.Scan(&slug, &name, &plan, &created, &members); err != nil {
						return "", err
					}
					n++
					fmt.Fprintf(&b, "%-24s %-30s plan=%s members=%d created=%s\n", slug, name, plan, members, created.Format("2006-01-02"))
				}
				if n == 0 {
					return "No workspaces matched.", nil
				}
				return fmt.Sprintf("%d workspace(s):\n\n%s", n, b.String()), nil
			},
		},
		{
			Name:        "get_workspace",
			Description: "One workspace (slug or id) with its members and pending invitations.",
			Scope:       "admin:workspaces:read",
			InputSchema: schema([]string{"workspace"}, map[string]any{"workspace": prop("string", "Slug or id")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				w, err := adminWorkspace(ctx, a.SysDB, str(decodeArgs(raw), "workspace"))
				if err != nil {
					return "", err
				}
				members, err := workspaces.Members(ctx, a.SysDB, w.id)
				if err != nil {
					return "", err
				}
				var pending int
				_ = a.SysDB.QueryRowContext(ctx,
					`SELECT count(*) FROM invitations WHERE workspace_id = $1 AND accepted_at IS NULL AND expires_at > now()`, w.id).Scan(&pending)
				var b strings.Builder
				fmt.Fprintf(&b, "%s — %s (id=%s, plan=%s)\n%d pending invitation(s)\n\n%d member(s):\n", w.slug, w.name, w.id, w.plan, pending, len(members))
				for _, m := range members {
					fmt.Fprintf(&b, "  %-8s %s (%s)\n", m.Role, m.Email, m.Status)
				}
				return b.String(), nil
			},
		},
		{
			Name:        "update_workspace",
			Description: "Rename a workspace or change its plan.",
			Scope:       "admin:workspaces:write",
			InputSchema: schema([]string{"workspace"}, map[string]any{
				"workspace": prop("string", "Slug or id"), "name": prop("string", ""), "plan": prop("string", ""),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				w, err := adminWorkspace(ctx, a.SysDB, str(args, "workspace"))
				if err != nil {
					return "", err
				}
				name, plan := firstNonEmpty(str(args, "name"), w.name), firstNonEmpty(str(args, "plan"), w.plan)
				if _, err := a.SysDB.ExecContext(ctx,
					`UPDATE workspaces SET name = $2, plan = $3, updated_at = now() WHERE id = $1`, w.id, name, plan); err != nil {
					return "", err
				}
				return fmt.Sprintf("Updated %s: name=%q plan=%s.", w.slug, name, plan), nil
			},
		},
		{
			Name:        "add_member",
			Description: "Add an existing user (by email) to a workspace with a role, bypassing the invitation flow.",
			Scope:       "admin:workspaces:write",
			InputSchema: schema([]string{"workspace", "email", "role"}, map[string]any{
				"workspace": prop("string", "Slug or id"), "email": prop("string", ""), "role": roleEnum,
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				role := str(args, "role")
				if !authz.Valid(role) {
					return "", fmt.Errorf("unknown role %q", role)
				}
				w, err := adminWorkspace(ctx, a.SysDB, str(args, "workspace"))
				if err != nil {
					return "", err
				}
				uid, err := userIDByEmail(ctx, a.SysDB, str(args, "email"))
				if err != nil {
					return "", err
				}
				res, err := a.SysDB.ExecContext(ctx, `
					INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, $3, 'active')
					ON CONFLICT (workspace_id, user_id) DO NOTHING`, w.id, uid, role)
				if err != nil {
					return "", err
				}
				if n, _ := res.RowsAffected(); n == 0 {
					return "", errors.New("already a member — use set_member_role")
				}
				return fmt.Sprintf("Added %s to %s as %s.", str(args, "email"), w.slug, role), nil
			},
		},
		{
			Name:        "set_member_role",
			Description: "Change a member's role or suspend/reactivate them. Refuses to leave a workspace without an active owner.",
			Scope:       "admin:workspaces:write",
			InputSchema: schema([]string{"workspace", "email"}, map[string]any{
				"workspace": prop("string", "Slug or id"), "email": prop("string", ""), "role": roleEnum,
				"status": map[string]any{"type": "string", "enum": []string{"active", "suspended"}},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				w, err := adminWorkspace(ctx, a.SysDB, str(args, "workspace"))
				if err != nil {
					return "", err
				}
				uid, err := userIDByEmail(ctx, a.SysDB, str(args, "email"))
				if err != nil {
					return "", err
				}
				var role, status string
				if err := a.SysDB.QueryRowContext(ctx,
					`SELECT role, status FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`, w.id, uid,
				).Scan(&role, &status); err != nil {
					return "", errors.New("not a member of that workspace")
				}
				newRole, newStatus := firstNonEmpty(str(args, "role"), role), firstNonEmpty(str(args, "status"), status)
				if !authz.Valid(newRole) || (newStatus != "active" && newStatus != "suspended") {
					return "", errors.New("invalid role or status")
				}
				if role == "owner" && status == "active" && (newRole != "owner" || newStatus != "active") {
					if lastOwner(ctx, a.SysDB, w.id) {
						return "", errors.New("that is the last active owner — promote another owner first")
					}
				}
				if _, err := a.SysDB.ExecContext(ctx, `
					UPDATE workspace_members SET role = $3, status = $4, updated_at = now()
					WHERE workspace_id = $1 AND user_id = $2`, w.id, uid, newRole, newStatus); err != nil {
					return "", err
				}
				return fmt.Sprintf("%s in %s is now %s (%s).", str(args, "email"), w.slug, newRole, newStatus), nil
			},
		},
		{
			Name:        "remove_member",
			Description: "Remove a member from a workspace. Their MCP tokens there stop working at once. Refuses to remove the last active owner.",
			Scope:       "admin:workspaces:write",
			InputSchema: schema([]string{"workspace", "email"}, map[string]any{
				"workspace": prop("string", "Slug or id"), "email": prop("string", ""),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				w, err := adminWorkspace(ctx, a.SysDB, str(args, "workspace"))
				if err != nil {
					return "", err
				}
				uid, err := userIDByEmail(ctx, a.SysDB, str(args, "email"))
				if err != nil {
					return "", err
				}
				var role string
				if err := a.SysDB.QueryRowContext(ctx,
					`SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2 AND status = 'active'`, w.id, uid,
				).Scan(&role); err == nil && role == "owner" && lastOwner(ctx, a.SysDB, w.id) {
					return "", errors.New("that is the last active owner — promote another owner first")
				}
				res, err := a.SysDB.ExecContext(ctx, `DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`, w.id, uid)
				if err != nil {
					return "", err
				}
				if n, _ := res.RowsAffected(); n == 0 {
					return "", errors.New("not a member of that workspace")
				}
				return fmt.Sprintf("Removed %s from %s.", str(args, "email"), w.slug), nil
			},
		},

		// ---- users ---------------------------------------------------------
		{
			Name:        "list_users",
			Description: "List user accounts with their platform roles and workspace counts. Optional email search.",
			Scope:       "admin:users:read",
			InputSchema: schema(nil, map[string]any{"q": prop("string", "Email contains"), "limit": prop("integer", "Default 50")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				rows, err := a.SysDB.QueryContext(ctx, `
					SELECT u.email, u.roles, u.created_at,
					       (SELECT count(*) FROM workspace_members m WHERE m.user_id = u.id)
					FROM users u WHERE ($1 = '' OR u.email ILIKE '%' || $1 || '%')
					ORDER BY u.created_at DESC LIMIT $2`, str(args, "q"), intArg(args, "limit", 50))
				if err != nil {
					return "", err
				}
				defer rows.Close()
				var b strings.Builder
				n := 0
				for rows.Next() {
					var email, roles, created string
					var count int
					if err := rows.Scan(&email, &roles, &created, &count); err != nil {
						return "", err
					}
					n++
					fmt.Fprintf(&b, "%-40s roles=%-10s workspaces=%d created=%s\n", email, firstNonEmpty(roles, "-"), count, created)
				}
				if n == 0 {
					return "No users matched.", nil
				}
				return fmt.Sprintf("%d user(s):\n\n%s", n, b.String()), nil
			},
		},
		{
			Name:        "get_user",
			Description: "One user (email or id) with every workspace membership and their MCP token counts.",
			Scope:       "admin:users:read",
			InputSchema: schema([]string{"user"}, map[string]any{"user": prop("string", "Email or id")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				ref := str(decodeArgs(raw), "user")
				var id, email, roles, created string
				if err := a.SysDB.QueryRowContext(ctx,
					`SELECT id, email, roles, created_at FROM users WHERE id = $1 OR lower(email) = lower($1)`, ref,
				).Scan(&id, &email, &roles, &created); err != nil {
					return "", errors.New("user not found")
				}
				list, err := workspaces.MyWorkspaces(ctx, a.SysDB, id)
				if err != nil {
					return "", err
				}
				var tokens int
				_ = a.SysDB.QueryRowContext(ctx,
					`SELECT count(*) FROM mcp_tokens WHERE user_id = $1 AND revoked_at IS NULL`, id).Scan(&tokens)
				var b strings.Builder
				fmt.Fprintf(&b, "%s (id=%s)\nplatform roles: %s\ncreated: %s\nlive user MCP tokens: %d\n\n%d workspace(s):\n",
					email, id, firstNonEmpty(roles, "-"), created, tokens, len(list))
				for _, w := range list {
					fmt.Fprintf(&b, "  %-8s %s (%s)\n", w.Role, w.Name, w.Slug)
				}
				return b.String(), nil
			},
		},
	}
}

type wsRow struct{ id, slug, name, plan string }

func adminWorkspace(ctx context.Context, db *sql.DB, ref string) (*wsRow, error) {
	w := &wsRow{}
	err := db.QueryRowContext(ctx, `SELECT id, slug, name, plan FROM workspaces WHERE id = $1 OR slug = $1`,
		strings.TrimSpace(ref)).Scan(&w.id, &w.slug, &w.name, &w.plan)
	if err != nil {
		return nil, errors.New("workspace not found")
	}
	return w, nil
}

func userIDByEmail(ctx context.Context, db *sql.DB, email string) (string, error) {
	var id string
	if err := db.QueryRowContext(ctx, `SELECT id FROM users WHERE lower(email) = lower($1)`, strings.TrimSpace(email)).Scan(&id); err != nil {
		return "", errors.New("no user with that email")
	}
	return id, nil
}

func lastOwner(ctx context.Context, db *sql.DB, workspaceID string) bool {
	var owners int
	_ = db.QueryRowContext(ctx,
		`SELECT count(*) FROM workspace_members WHERE workspace_id = $1 AND role = 'owner' AND status = 'active'`, workspaceID).Scan(&owners)
	return owners <= 1
}
