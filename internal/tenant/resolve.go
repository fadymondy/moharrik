package tenant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/togo-framework/auth"
)

// Header is where the web app (from its /w/{slug} route) names the workspace.
// MCP requests never use it — their workspace comes from the token.
const Header = "X-Workspace-ID"

// ErrNotMember covers "no such workspace" and "not a member" alike: the two are
// deliberately indistinguishable to the caller (404, never 403).
var ErrNotMember = errors.New("workspace not found")

// Resolve finds ref (a workspace id or slug) among userID's active
// memberships. It runs on the owner pool with explicit predicates, because the
// question "which workspaces is this user in" is by definition cross-tenant.
func Resolve(ctx context.Context, sys *sql.DB, userID, ref string) (*Scope, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || userID == "" {
		return nil, ErrNotMember
	}
	s := &Scope{UserID: userID}
	err := sys.QueryRowContext(ctx, `
		SELECT w.id, w.slug, w.name, m.role, u.email
		FROM workspaces w
		JOIN workspace_members m ON m.workspace_id = w.id
		JOIN users u ON u.id = m.user_id
		WHERE (w.id = $1 OR w.slug = $1) AND m.user_id = $2 AND m.status = 'active'`,
		ref, userID,
	).Scan(&s.WorkspaceID, &s.WorkspaceSlug, &s.WorkspaceName, &s.Role, &s.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotMember
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Require resolves the X-Workspace-ID header for the signed-in identity and
// puts the Scope on the context. It must run inside auth.Middleware. A missing
// header, an unknown workspace and a workspace the caller is not in all answer
// 404 — existence of a tenant is not revealed to non-members.
func Require(sys *sql.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := auth.IdentityFrom(r.Context())
			if !ok || id == nil {
				writeErr(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			s, err := Resolve(r.Context(), sys, id.ID, r.Header.Get(Header))
			if errors.Is(err, ErrNotMember) {
				writeErr(w, http.StatusNotFound, "workspace not found")
				return
			}
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "could not resolve the workspace")
				return
			}
			s.Via = "session"
			next.ServeHTTP(w, r.WithContext(With(r.Context(), s)))
		})
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
