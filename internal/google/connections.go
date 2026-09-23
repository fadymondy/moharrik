package google

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// Connection is a connected Google account as the page reads it. It has no
// token, sealed or otherwise: the query below does not select one.
type Connection struct {
	ID       string    `json:"id"`
	Email    string    `json:"email"`
	Products []Product `json:"products"`
	Scopes   []string  `json:"scopes"`
	// Revenue: YouTube monetary reporting was granted.
	Revenue bool `json:"revenue"`
	// "" or "reauth".
	Status           string    `json:"status"`
	AuthError        string    `json:"auth_error"`
	ConnectedBy      string    `json:"connected_by"`
	ConnectedByEmail string    `json:"connected_by_email"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// List is the workspace's connected accounts, oldest first.
func List(ctx context.Context, a *app.App, workspaceID string) ([]Connection, error) {
	if err := ready(a); err != nil {
		return nil, err
	}
	rows, err := a.SQLDB.QueryContext(tenant.WithWorkspace(ctx, workspaceID), `
		SELECT id, email, granted_scopes, status, auth_error, connected_by, created_at, updated_at
		FROM google_connections WHERE workspace_id = $1
		ORDER BY created_at, id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Connection{}
	users := []string{}
	for rows.Next() {
		var c Connection
		var scopes pgtext.Array
		if err := rows.Scan(&c.ID, &c.Email, &scopes, &c.Status, &c.AuthError, &c.ConnectedBy, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Scopes = sortedUnique(scopes)
		c.Products = ProductsOf(c.Scopes)
		c.Revenue = hasAll(c.Scopes, ScopeYTMonetary)
		out = append(out, c)
		users = append(users, c.ConnectedBy)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(users) > 0 {
		emails := memberEmails(ctx, a, workspaceID, users)
		for i := range out {
			out[i].ConnectedByEmail = emails[out[i].ConnectedBy]
		}
	}
	return out, nil
}

// memberEmails names who connected each account. users is not granted to the
// app role, so this reads the owner pool, restricted to members of this
// workspace: a user id from another tenant resolves to nothing.
func memberEmails(ctx context.Context, a *app.App, workspaceID string, ids []string) map[string]string {
	out := map[string]string{}
	rows, err := a.SysDB.QueryContext(ctx, `
		SELECT u.id, u.email FROM users u
		JOIN workspace_members m ON m.user_id = u.id AND m.workspace_id = $1
		WHERE u.id = ANY($2::text[])`, workspaceID, pgtext.Literal(ids))
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, email string
		if rows.Scan(&id, &email) == nil {
			out[id] = email
		}
	}
	return out
}

// Disconnected reports what disconnect did.
type Disconnected struct {
	// Revoked: Google confirmed the grant is gone.
	Revoked bool `json:"revoked"`
	// Shared: the same Google account is still connected elsewhere in
	// Moharrik, so the grant was NOT revoked at Google (revoking is per
	// account and client, and would disconnect that one too). Nothing about
	// where is said.
	Shared      bool   `json:"shared"`
	RevokeError string `json:"revoke_error,omitempty"`
}

// Disconnect revokes the grant at Google, then deletes the connection here
// whatever Google answered: a grant this workspace no longer wants must not
// stay usable from Moharrik because Google was briefly unreachable.
func Disconnect(ctx context.Context, a *app.App, workspaceID, id string) (*Disconnected, error) {
	if err := ready(a); err != nil {
		return nil, err
	}
	wctx := tenant.WithWorkspace(ctx, workspaceID)
	var sealed, sub string
	err := a.SQLDB.QueryRowContext(wctx, `
		SELECT refresh_token_sealed, google_sub FROM google_connections
		WHERE workspace_id = $1 AND id = $2`, workspaceID, id).Scan(&sealed, &sub)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotConnected
	}
	if err != nil {
		return nil, err
	}
	out := &Disconnected{}
	// Cross-tenant by definition, so on the owner pool — and a boolean only.
	if err := a.SysDB.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM google_connections WHERE google_sub = $1 AND id <> $2)`,
		sub, id).Scan(&out.Shared); err != nil {
		return nil, err
	}
	if !out.Shared {
		if refresh, err := refreshBox.Open(sealed); err != nil {
			out.RevokeError = "the stored grant was unreadable; remove Moharrik at myaccount.google.com/permissions"
		} else if err := revoke(ctx, refresh); err != nil {
			out.RevokeError = err.Error()
			logWarn(a, "google disconnect: revoke failed", err)
		} else {
			out.Revoked = true
		}
	}
	if _, err := a.SQLDB.ExecContext(wctx,
		`DELETE FROM google_connections WHERE workspace_id = $1 AND id = $2`, workspaceID, id); err != nil {
		return nil, err
	}
	dropCache(id)
	return out, nil
}
