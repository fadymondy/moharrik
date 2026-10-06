package admin

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/platform"
)

// AdminMembership is one workspace a user belongs to.
type AdminMembership struct {
	WorkspaceID string `json:"workspace_id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	Status      string `json:"status"`
}

// User is an account as the panel lists it.
type AdminUser struct {
	ID              string            `json:"id"`
	Email           string            `json:"email"`
	IsAdmin         bool              `json:"is_admin"`
	Status          string            `json:"status" enum:"active,suspended"`
	SuspendedReason string            `json:"suspended_reason,omitempty"`
	CreatedAt       string            `json:"created_at"`
	LastSeenAt      *time.Time        `json:"last_seen_at"`
	Workspaces      []AdminMembership `json:"workspaces"`
}

// AdminUserDetail adds what only the one-user view needs.
type AdminUserDetail struct {
	AdminUser
	SessionsValidAfter *time.Time `json:"sessions_valid_after"`
	MCPTokens          int        `json:"mcp_tokens"`
}

// UserFilter narrows the list. Status is active, suspended or admin.
type UserFilter struct {
	Q, Status string
	Limit     int
}

const userCols = `u.id, u.email, u.roles, u.created_at, p.suspended_at, COALESCE(p.suspended_reason, ''), p.last_seen_at, p.sessions_valid_after`

type rowScanner interface{ Scan(...any) error }

func scanUser(row rowScanner) (*AdminUserDetail, error) {
	d := &AdminUserDetail{}
	var roles string
	var suspended *time.Time
	if err := row.Scan(&d.ID, &d.Email, &roles, &d.CreatedAt, &suspended, &d.SuspendedReason, &d.LastSeenAt, &d.SessionsValidAfter); err != nil {
		return nil, err
	}
	d.IsAdmin = platform.IsAdmin(roles)
	d.Status = "active"
	if suspended != nil {
		d.Status = "suspended"
	}
	d.Workspaces = []AdminMembership{}
	return d, nil
}

// ListUsers lists accounts, newest first.
func (sv *Service) ListUsers(ctx context.Context, f UserFilter) ([]AdminUser, error) {
	rows, err := sv.DB.QueryContext(ctx, `
		SELECT `+userCols+`
		FROM users u LEFT JOIN platform_users p ON p.user_id = u.id
		WHERE ($1 = '' OR u.email ILIKE $2)
		  AND ($3 = '' OR ($3 = 'suspended' AND p.suspended_at IS NOT NULL)
		       OR ($3 = 'active' AND p.suspended_at IS NULL)
		       OR ($3 = 'admin' AND (',' || replace(u.roles, ' ', '') || ',') LIKE '%,admin,%'))
		ORDER BY u.created_at DESC LIMIT $4`,
		strings.TrimSpace(f.Q), like(f.Q), f.Status, limitOf(f.Limit, 500, 1000))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminUser{}
	byID := map[string]int{}
	for rows.Next() {
		d, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		byID[d.ID] = len(out)
		out = append(out, d.AdminUser)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(out))
	for _, u := range out {
		ids = append(ids, u.ID)
	}
	ms, err := sv.memberships(ctx, ids)
	if err != nil {
		return nil, err
	}
	for id, list := range ms {
		out[byID[id]].Workspaces = list
	}
	return out, nil
}

func (sv *Service) memberships(ctx context.Context, userIDs []string) (map[string][]AdminMembership, error) {
	rows, err := sv.DB.QueryContext(ctx, `
		SELECT m.user_id, w.id, w.slug, w.name, m.role, m.status
		FROM workspace_members m JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.user_id = ANY(string_to_array($1, ',')) ORDER BY w.name`, strings.Join(userIDs, ","))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]AdminMembership{}
	for rows.Next() {
		var uid string
		var m AdminMembership
		if err := rows.Scan(&uid, &m.WorkspaceID, &m.Slug, &m.Name, &m.Role, &m.Status); err != nil {
			return nil, err
		}
		out[uid] = append(out[uid], m)
	}
	return out, rows.Err()
}

// GetUser reads one account by id or email.
func (sv *Service) GetUser(ctx context.Context, ref string) (*AdminUserDetail, error) {
	ref = strings.TrimSpace(ref)
	d, err := scanUser(sv.DB.QueryRowContext(ctx, `
		SELECT `+userCols+`
		FROM users u LEFT JOIN platform_users p ON p.user_id = u.id
		WHERE u.id = $1 OR lower(u.email) = lower($1)`, ref))
	if isNoRows(err) {
		return nil, notFound("user")
	}
	if err != nil {
		return nil, err
	}
	ms, err := sv.memberships(ctx, []string{d.ID})
	if err != nil {
		return nil, err
	}
	if list := ms[d.ID]; list != nil {
		d.Workspaces = list
	}
	_ = sv.DB.QueryRowContext(ctx, `SELECT count(*) FROM mcp_tokens WHERE user_id = $1 AND revoked_at IS NULL`, d.ID).Scan(&d.MCPTokens)
	return d, nil
}

// userForChange reads the target and refuses an admin acting on their own account where that
// would lock them out.
func (sv *Service) userForChange(ctx context.Context, a Actor, ref string, selfMsg string) (*AdminUserDetail, error) {
	d, err := sv.GetUser(ctx, ref)
	if err != nil {
		return nil, err
	}
	if selfMsg != "" && d.ID == a.ID {
		return nil, conflict(selfMsg)
	}
	return d, nil
}

func (sv *Service) setUserFlags(ctx context.Context, a Actor, d *AdminUserDetail, action, set string, args []any, changes any) (*AdminUserDetail, error) {
	err := sv.tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO platform_users (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, d.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE platform_users SET `+set+`, updated_at = now() WHERE user_id = $1`,
			append([]any{d.ID}, args...)...); err != nil {
			return err
		}
		return sv.audit(ctx, tx, a, action, "user", d.ID, d.Email, changes)
	})
	if err != nil {
		return nil, err
	}
	platform.Invalidate(d.ID)
	return sv.GetUser(ctx, d.ID)
}

// SuspendUser blocks an account: every call answers 403 and sign-in is refused.
func (sv *Service) SuspendUser(ctx context.Context, a Actor, ref, reason string) (*AdminUserDetail, error) {
	d, err := sv.userForChange(ctx, a, ref, "you cannot suspend your own account")
	if err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	return sv.setUserFlags(ctx, a, d, "user.suspend", "suspended_at = now(), suspended_reason = NULLIF($2, '')",
		[]any{reason}, map[string]any{"reason": reason})
}

// ReactivateUser lifts a suspension.
func (sv *Service) ReactivateUser(ctx context.Context, a Actor, ref string) (*AdminUserDetail, error) {
	d, err := sv.userForChange(ctx, a, ref, "")
	if err != nil {
		return nil, err
	}
	return sv.setUserFlags(ctx, a, d, "user.reactivate", "suspended_at = NULL, suspended_reason = NULL", nil, nil)
}

// SignOut ends every session the account has: anything issued before now is refused.
func (sv *Service) SignOut(ctx context.Context, a Actor, ref string) (*AdminUserDetail, error) {
	d, err := sv.userForChange(ctx, a, ref, "")
	if err != nil {
		return nil, err
	}
	return sv.setUserFlags(ctx, a, d, "user.sign_out", "sessions_valid_after = now()", nil, nil)
}

// SetAdmin grants or revokes platform admin (the "admin" entry in users.roles). An admin cannot
// revoke their own, so the platform always keeps the one doing it.
func (sv *Service) SetAdmin(ctx context.Context, a Actor, ref string, admin bool) (*AdminUserDetail, error) {
	msg := ""
	if !admin {
		msg = "you cannot revoke your own platform admin role"
	}
	d, err := sv.userForChange(ctx, a, ref, msg)
	if err != nil {
		return nil, err
	}
	err = sv.tx(ctx, func(tx *sql.Tx) error {
		var roles string
		if err := tx.QueryRowContext(ctx, `SELECT roles FROM users WHERE id = $1 FOR UPDATE`, d.ID).Scan(&roles); err != nil {
			return err
		}
		kept := []string{}
		for _, r := range strings.Split(roles, ",") {
			if r = strings.TrimSpace(r); r != "" && r != platform.AdminRole {
				kept = append(kept, r)
			}
		}
		if admin {
			kept = append(kept, platform.AdminRole)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET roles = $2 WHERE id = $1`, d.ID, strings.Join(kept, ",")); err != nil {
			return err
		}
		action := "user.revoke_admin"
		if admin {
			action = "user.grant_admin"
		}
		return sv.audit(ctx, tx, a, action, "user", d.ID, d.Email, map[string]any{"admin": admin})
	})
	if err != nil {
		return nil, err
	}
	platform.Invalidate(d.ID)
	return sv.GetUser(ctx, d.ID)
}
