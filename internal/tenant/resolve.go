package tenant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/togo-framework/auth"
)

// Header is where the web app (from its /w/{slug} route) names the workspace.
// MCP requests never use it — their workspace comes from the token.
const Header = "X-Workspace-ID"

// ErrNotMember covers "no such workspace" and "not a member" alike: the two are
// deliberately indistinguishable to the caller (404, never 403).
var ErrNotMember = errors.New("workspace not found")

// ErrSuspended is a workspace a platform admin has suspended (MH-1184). Its
// members get 403 with this message; its data is untouched.
var ErrSuspended = errors.New("this workspace is suspended. Contact support to restore access")

// ErrAccountSuspended is a member whose own account is suspended. Checked here
// as well as in the request guard so MCP tokens and OAuth grants die with it.
var ErrAccountSuspended = errors.New("your account is suspended. Contact support to restore access")

// Resolve finds ref (a workspace id or slug) among userID's active
// memberships. It runs on the owner pool with explicit predicates, because the
// question "which workspaces is this user in" is by definition cross-tenant.
func Resolve(ctx context.Context, sys *sql.DB, userID, ref string) (*Scope, error) {
	ref = strings.TrimSpace(ref)
	// A reference no workspace can have is not a question for the database:
	// bytes Postgres refuses (invalid UTF-8, NUL) failed the query, and the
	// failure answered 500 on every workspace route (MH-472). An id is 36
	// characters and a slug is short; anything this long names nothing.
	if ref == "" || userID == "" || len(ref) > 200 || !utf8.ValidString(ref) || strings.ContainsRune(ref, 0) {
		return nil, ErrNotMember
	}
	s := &Scope{UserID: userID}
	var suspended, accountSuspended sql.NullTime
	err := sys.QueryRowContext(ctx, `
		SELECT w.id, w.slug, w.name, m.role, u.email, w.suspended_at, p.suspended_at
		FROM workspaces w
		JOIN workspace_members m ON m.workspace_id = w.id
		JOIN users u ON u.id = m.user_id
		LEFT JOIN platform_users p ON p.user_id = m.user_id
		WHERE (w.id = $1 OR w.slug = $1) AND m.user_id = $2 AND m.status = 'active'`,
		ref, userID,
	).Scan(&s.WorkspaceID, &s.WorkspaceSlug, &s.WorkspaceName, &s.Role, &s.Email, &suspended, &accountSuspended)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotMember
	}
	if err != nil {
		return nil, err
	}
	if accountSuspended.Valid {
		return nil, ErrAccountSuspended
	}
	if suspended.Valid {
		return nil, ErrSuspended
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
			if errors.Is(err, ErrSuspended) || errors.Is(err, ErrAccountSuspended) {
				writeErr(w, http.StatusForbidden, err.Error())
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
