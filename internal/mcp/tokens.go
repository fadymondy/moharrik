package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// Token prefixes. Distinct, so neither server can be handed the other's token
// and so a leaked token is recognisable in a log or a secret scanner.
const (
	UserPrefix  = "mhr_"
	AdminPrefix = "mhra_"
)

// HashToken is the only form a token is stored in.
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// NewToken returns a plaintext token and its visible prefix (prefix + 8 chars).
func NewToken(prefix string) (plain, visible string) {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	plain = prefix + hex.EncodeToString(b)
	return plain, plain[:len(prefix)+8]
}

func parseScopes(raw string) []string {
	var list []string
	if json.Unmarshal([]byte(raw), &list) != nil {
		return nil
	}
	return list
}

// authUser resolves a user MCP token to the SAME tenant.Scope a browser session
// gets — workspace, user, current role — so the tools run under the same authz
// and the same RLS. The token's stored scopes are narrowed by the role.
func authUser(r *http.Request, a *app.App, token string) (context.Context, []string, bool) {
	if !strings.HasPrefix(token, UserPrefix) {
		return nil, nil, false
	}
	ctx := r.Context()
	var id, workspaceID, userID, rawScopes string
	var expires, revoked *time.Time
	err := a.SysDB.QueryRowContext(ctx, `
		SELECT id, workspace_id, user_id, scopes, expires_at, revoked_at
		FROM mcp_tokens WHERE token_hash = $1`, HashToken(token),
	).Scan(&id, &workspaceID, &userID, &rawScopes, &expires, &revoked)
	if err != nil || revoked != nil || (expires != nil && time.Now().After(*expires)) {
		return nil, nil, false
	}
	// Membership is re-checked on every call: a removed member's token dies with
	// the membership, a demoted one's scopes shrink.
	s, err := tenant.Resolve(ctx, a.SysDB, userID, workspaceID)
	if err != nil {
		return nil, nil, false
	}
	s.Via = "mcp"
	s.Scopes = Effective(s.Role, parseScopes(rawScopes))

	_, _ = a.SysDB.ExecContext(ctx, `UPDATE mcp_tokens SET last_used_at = now() WHERE id = $1`, id)
	return tenant.With(ctx, s), s.Scopes, true
}

// ---- token management endpoints (Settings → Developer → MCP tokens) -----

type tokenOut struct {
	ID string `json:"id"`
	// Who the token acts as (MH-396): an owner or admin sees every member's.
	UserID     string     `json:"user_id"`
	UserEmail  string     `json:"user_email"`
	Mine       bool       `json:"mine"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

type scopeOut struct {
	Scope       string `json:"scope"`
	Description string `json:"description"`
	MinRole     string `json:"min_role"`
	Allowed     bool   `json:"allowed"`
}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

// RegisterTokenRoutes mounts the user-token endpoints. `a` may be nil (OpenAPI).
func RegisterTokenRoutes(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "list-my-mcp-tokens", Method: http.MethodGet, Path: "/api/w/mcp_tokens",
		Summary: "MCP tokens in this workspace — your own, or every member's for an owner or admin — and the scopes you may grant",
		Tags:    []string{"MCP"},
	}, func(ctx context.Context, _ *wsHeader) (*struct {
		Body tokenList
	}, error) {
		s, ok := tenant.From(ctx)
		if !ok {
			return nil, huma.Error404NotFound("workspace not found")
		}
		list, err := ListTokens(ctx, a, s)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list tokens")
		}
		return &struct{ Body tokenList }{*list}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-my-mcp-token", Method: http.MethodPost, Path: "/api/w/mcp_tokens",
		DefaultStatus: http.StatusCreated, Summary: "Mint an MCP token (plaintext returned once)", Tags: []string{"MCP"},
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Name          string   `json:"name" minLength:"1" maxLength:"80"`
			Scopes        []string `json:"scopes" minItems:"1"`
			ExpiresInDays int      `json:"expires_in_days,omitempty" minimum:"0" maximum:"365" doc:"0 = never"`
		}
	}) (*struct {
		Body struct {
			Token     tokenOut `json:"token"`
			Plaintext string   `json:"plaintext"`
		}
	}, error) {
		s, ok := tenant.From(ctx)
		if !ok {
			return nil, huma.Error404NotFound("workspace not found")
		}
		if !authz.Can(s.Role, authz.TokensManageOwn) {
			return nil, huma.Error403Forbidden("your role cannot create MCP tokens")
		}
		name := strings.TrimSpace(in.Body.Name)
		if name == "" {
			return nil, huma.Error422UnprocessableEntity("a name is required")
		}
		seen := map[string]bool{}
		scopes := []string{}
		for _, sc := range in.Body.Scopes {
			sc = strings.TrimSpace(sc)
			if seen[sc] {
				continue
			}
			seen[sc] = true
			if _, known := userScope(sc); !known {
				return nil, huma.Error422UnprocessableEntity("unknown scope: " + sc)
			}
			if !RoleMayHold(s.Role, sc) {
				return nil, huma.Error422UnprocessableEntity("your role cannot grant " + sc)
			}
			scopes = append(scopes, sc)
		}
		if len(scopes) == 0 {
			return nil, huma.Error422UnprocessableEntity("choose at least one scope")
		}
		var expires *time.Time
		if in.Body.ExpiresInDays > 0 {
			t := time.Now().Add(time.Duration(in.Body.ExpiresInDays) * 24 * time.Hour).UTC()
			expires = &t
		}
		plain, visible := NewToken(UserPrefix)
		rawScopes, _ := json.Marshal(scopes)

		out := &struct {
			Body struct {
				Token     tokenOut `json:"token"`
				Plaintext string   `json:"plaintext"`
			}
		}{}
		t := &out.Body.Token
		err := a.SQLDB.QueryRowContext(ctx, `
			INSERT INTO mcp_tokens (workspace_id, user_id, name, token_hash, prefix, scopes, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id, name, prefix, expires_at, last_used_at, revoked_at, created_at`,
			s.WorkspaceID, s.UserID, name, HashToken(plain), visible, string(rawScopes), expires,
		).Scan(&t.ID, &t.Name, &t.Prefix, &t.ExpiresAt, &t.LastUsedAt, &t.RevokedAt, &t.CreatedAt)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not create the token")
		}
		t.Scopes = scopes
		t.UserID, t.UserEmail, t.Mine = s.UserID, s.Email, true
		out.Body.Plaintext = plain
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "revoke-mcp-token", Method: http.MethodDelete, Path: "/api/w/mcp_tokens/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Revoke an MCP token (your own; admins: any in the workspace)",
		Tags: []string{"MCP"},
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*struct{}, error) {
		s, ok := tenant.From(ctx)
		if !ok {
			return nil, huma.Error404NotFound("workspace not found")
		}
		anyToken := authz.Can(s.Role, authz.TokensRevokeAny)
		res, err := a.SQLDB.ExecContext(ctx, `
			UPDATE mcp_tokens SET revoked_at = now(), updated_at = now()
			WHERE workspace_id = $1 AND id = $2 AND revoked_at IS NULL AND (user_id = $3 OR $4)`,
			s.WorkspaceID, in.ID, s.UserID, anyToken)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not revoke the token")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("token not found")
		}
		return &struct{}{}, nil
	})
}

type tokenList struct {
	Tokens []tokenOut `json:"tokens"`
	Scopes []scopeOut `json:"scopes"`
	// All is true when the list is every member's tokens (owner/admin), false
	// when it is only the caller's own.
	All bool `json:"all"`
}

/*
ListTokens is the token list for Settings → MCP (MH-396).

An owner or admin holds TokensRevokeAny: they may revoke any token in the
workspace, so they must be able to SEE them all — who holds it, its name, its
scopes, when it was made, last used and expires. The list used to filter on
the caller's own user id, which left an admin able to revoke tokens they had
no way to find. A member still sees only their own.
*/
func ListTokens(ctx context.Context, a *app.App, s *tenant.Scope) (*tokenList, error) {
	all := authz.Can(s.Role, authz.TokensRevokeAny)
	rows, err := a.SQLDB.QueryContext(ctx, `
		SELECT id, user_id, name, prefix, scopes, expires_at, last_used_at, revoked_at, created_at
		FROM mcp_tokens WHERE workspace_id = $1 AND (user_id = $2 OR $3)
		ORDER BY revoked_at IS NOT NULL, created_at DESC`,
		s.WorkspaceID, s.UserID, all)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &tokenList{Tokens: []tokenOut{}, Scopes: []scopeOut{}, All: all}
	ids := map[string]bool{}
	for rows.Next() {
		var t tokenOut
		var raw string
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &t.Prefix, &raw, &t.ExpiresAt, &t.LastUsedAt, &t.RevokedAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.Scopes = parseScopes(raw)
		t.Mine = t.UserID == s.UserID
		ids[t.UserID] = true
		out.Tokens = append(out.Tokens, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Emails come from the owner pool (the app role has no grant on users),
	// for exactly the ids this workspace's tokens name — including a member
	// who has since left, whose tokens already stopped working with the
	// membership but still belong in the list.
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
		for i := range out.Tokens {
			out.Tokens[i].UserEmail = emails[out.Tokens[i].UserID]
		}
	}
	for _, d := range UserScopes {
		out.Scopes = append(out.Scopes, scopeOut{
			Scope: d.Scope, Description: d.Description, MinRole: string(d.MinRole),
			Allowed: authz.Role(s.Role).AtLeast(d.MinRole),
		})
	}
	return out, nil
}

// errNotFound is what tools return for anything outside the caller's reach.
var errNotFound = errors.New("not found")

var _ = sql.ErrNoRows
