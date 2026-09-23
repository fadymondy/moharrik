// Package tenant resolves which workspace a request acts in and carries that
// decision — {workspace, user, role} — on the context, for the browser session
// and MCP tokens alike. The RLS connection wrapper (rlsdb.go) reads the same
// context value, so authorization and the database backstop cannot disagree.
package tenant

import "context"

// Scope is the resolved tenant context of a request.
type Scope struct {
	WorkspaceID   string
	WorkspaceSlug string
	WorkspaceName string
	UserID        string
	Email         string
	Role          string // owner | admin | manager | member | client
	// Via is how the caller authenticated: "session", "mcp" (a pasted token),
	// "oauth" (an OAuth-connected MCP client, MH-391) or "system".
	Via string
	// TokenID and TokenName name the credential for "mcp" (mcp_tokens.id and
	// its name) and "oauth" (oauth_grants.id and the client's name) — who the
	// audit log records (MH-392).
	TokenID   string
	TokenName string
	// Scopes are the MCP scopes in effect (token scopes ∩ what the role allows).
	// Empty for a browser session.
	Scopes []string
}

type scopeKey struct{}

// With returns ctx carrying s.
func With(ctx context.Context, s *Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, s)
}

// From returns the scope on ctx, if any.
func From(ctx context.Context) (*Scope, bool) {
	s, ok := ctx.Value(scopeKey{}).(*Scope)
	return s, ok && s != nil
}

// WorkspaceID returns the workspace on ctx, or "" (which RLS treats as none).
func WorkspaceID(ctx context.Context) string {
	if s, ok := From(ctx); ok {
		return s.WorkspaceID
	}
	return ""
}

// WithWorkspace scopes ctx to a workspace without a user — for system jobs
// that must act inside one tenant through the RLS pool.
//
// Whoever was acting on ctx stays the actor (audit.go): a handler that
// re-scopes its context for a helper still records the member, token or
// client who asked, not "the system".
func WithWorkspace(ctx context.Context, workspaceID string) context.Context {
	if a, ok := ActorFrom(ctx); ok {
		ctx = WithActor(ctx, a)
	}
	return With(ctx, &Scope{WorkspaceID: workspaceID, Via: "system"})
}
