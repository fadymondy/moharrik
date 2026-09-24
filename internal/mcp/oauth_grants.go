package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// GrantOut is one connected app (an OAuth grant) in Settings → MCP (MH-391).
type GrantOut struct {
	ID         string     `json:"id"`
	ClientID   string     `json:"client_id"`
	ClientName string     `json:"client_name"`
	ClientURI  string     `json:"client_uri"`
	UserID     string     `json:"user_id"`
	UserEmail  string     `json:"user_email"`
	Mine       bool       `json:"mine"`
	Scopes     []string   `json:"scopes"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

type grantList struct {
	Grants []GrantOut `json:"grants"`
	// All is true when the list is every member's connections (owner/admin).
	All bool `json:"all"`
	// The URL to add as a connector in claude.ai or ChatGPT.
	ServerURL string `json:"server_url"`
}

// ListGrants is the workspace's connected apps: a member's own, or every
// member's for an owner or admin (who may revoke any), like the token list.
// On the app pool: RLS keeps it to this workspace.
func ListGrants(ctx context.Context, a *app.App, s *tenant.Scope) (*grantList, error) {
	all := authz.Can(s.Role, authz.TokensRevokeAny)
	rows, err := a.SQLDB.QueryContext(ctx, `
		SELECT id, client_id, client_name, client_uri, user_id, scopes, last_used_at, revoked_at, created_at
		FROM oauth_grants
		WHERE workspace_id = $1 AND activated_at IS NOT NULL AND (user_id = $2 OR $3)
		ORDER BY revoked_at IS NOT NULL, created_at DESC LIMIT 500`, s.WorkspaceID, s.UserID, all)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// The URL Settings shows and hands to claude.ai/ChatGPT: the dedicated
	// host once one is configured, else the legacy resource (MH-516).
	out := &grantList{Grants: []GrantOut{}, All: all, ServerURL: MCPServerURL()}
	ids := map[string]bool{}
	for rows.Next() {
		var g GrantOut
		var scopes string
		if err := rows.Scan(&g.ID, &g.ClientID, &g.ClientName, &g.ClientURI, &g.UserID, &scopes, &g.LastUsedAt, &g.RevokedAt, &g.CreatedAt); err != nil {
			return nil, err
		}
		g.Scopes = strings.Fields(scopes)
		if g.Scopes == nil {
			g.Scopes = []string{}
		}
		g.Mine = g.UserID == s.UserID
		ids[g.UserID] = true
		out.Grants = append(out.Grants, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		list := make([]string, 0, len(ids))
		for id := range ids {
			list = append(list, id)
		}
		raw, _ := json.Marshal(list)
		emails := map[string]string{}
		urows, err := a.SysDB.QueryContext(ctx, `SELECT id, email FROM users WHERE id IN (SELECT jsonb_array_elements_text($1::jsonb))`, string(raw))
		if err != nil {
			return nil, err
		}
		for urows.Next() {
			var id, email string
			if urows.Scan(&id, &email) == nil {
				emails[id] = email
			}
		}
		urows.Close()
		for i := range out.Grants {
			out.Grants[i].UserEmail = emails[out.Grants[i].UserID]
		}
	}
	return out, nil
}

// RegisterGrantRoutes mounts the connected-apps endpoints. `a` may be nil (OpenAPI).
func RegisterGrantRoutes(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "list-oauth-grants", Method: http.MethodGet, Path: "/api/w/oauth_grants",
		Summary: "Apps connected to this workspace over OAuth (claude.ai, ChatGPT…): your own, or every member's for an owner or admin",
		Tags:    []string{"MCP"},
	}, func(ctx context.Context, _ *wsHeader) (*struct{ Body grantList }, error) {
		s, ok := tenant.From(ctx)
		if !ok {
			return nil, huma.Error404NotFound("workspace not found")
		}
		list, err := ListGrants(ctx, a, s)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list connected apps")
		}
		return &struct{ Body grantList }{*list}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "revoke-oauth-grant", Method: http.MethodDelete, Path: "/api/w/oauth_grants/{id}",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Disconnect an app: its tokens stop working at once (your own; admins: any in the workspace)",
		Tags:          []string{"MCP"},
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*struct{}, error) {
		s, ok := tenant.From(ctx)
		if !ok {
			return nil, huma.Error404NotFound("workspace not found")
		}
		anyGrant := authz.Can(s.Role, authz.TokensRevokeAny)
		res, err := a.SQLDB.ExecContext(ctx, `
			UPDATE oauth_grants SET revoked_at = now(), updated_at = now()
			WHERE workspace_id = $1 AND id = $2 AND revoked_at IS NULL AND (user_id = $3 OR $4)`,
			s.WorkspaceID, in.ID, s.UserID, anyGrant)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not disconnect the app")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("connection not found")
		}
		// The tokens are dead with the grant (authOAuth checks revoked_at);
		// deleting them is tidiness, on the owner pool (a platform table).
		_, _ = a.SysDB.ExecContext(context.WithoutCancel(ctx), `DELETE FROM oauth_tokens WHERE grant_id = $1`, in.ID)
		return &struct{}{}, nil
	})
}
