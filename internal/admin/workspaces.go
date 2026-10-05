package admin

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/workspaces"
)

// Workspace is a tenant as the panel lists it.
type AdminWorkspace struct {
	ID              string     `json:"id"`
	Slug            string     `json:"slug"`
	Name            string     `json:"name"`
	Plan            string     `json:"plan"`
	Status          string     `json:"status" enum:"active,suspended"`
	SuspendedReason string     `json:"suspended_reason,omitempty"`
	OwnerEmail      string     `json:"owner_email"`
	Members         int        `json:"members"`
	Contacts        int        `json:"contacts"`
	Conversations   int        `json:"conversations"`
	Messages30d     int        `json:"messages_30d"`
	LastActiveAt    *time.Time `json:"last_active_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

// Member is one person in a workspace.
type AdminMember struct {
	UserID     string     `json:"user_id"`
	Email      string     `json:"email"`
	Role       string     `json:"role"`
	Status     string     `json:"status"`
	IsAdmin    bool       `json:"is_admin"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	JoinedAt   time.Time  `json:"joined_at"`
}

// AdminWorkspaceDetail adds the members and the record counts.
type AdminWorkspaceDetail struct {
	AdminWorkspace
	MemberList []AdminMember  `json:"member_list"`
	Counts     map[string]int `json:"counts"`
}

// WorkspaceFilter narrows the list. Status is active or suspended.
type WorkspaceFilter struct {
	Q, Status string
	Limit     int
}

const wsCols = `w.id, w.slug, w.name, w.plan, w.suspended_at, COALESCE(w.suspended_reason, ''), w.created_at,
	COALESCE((SELECT u.email FROM workspace_members m JOIN users u ON u.id = m.user_id
	          WHERE m.workspace_id = w.id AND m.role = 'owner' AND m.status = 'active' ORDER BY m.created_at LIMIT 1), ''),
	(SELECT count(*) FROM workspace_members m WHERE m.workspace_id = w.id),
	(SELECT count(*) FROM contacts c WHERE c.workspace_id = w.id),
	(SELECT count(*) FROM conversations c WHERE c.workspace_id = w.id),
	(SELECT count(*) FROM messages x WHERE x.workspace_id = w.id AND x.created_at > now() - interval '30 days'),
	(SELECT max(p.last_seen_at) FROM workspace_members m JOIN platform_users p ON p.user_id = m.user_id WHERE m.workspace_id = w.id)`

func scanWorkspace(row rowScanner) (*AdminWorkspace, error) {
	w := &AdminWorkspace{}
	var suspended *time.Time
	if err := row.Scan(&w.ID, &w.Slug, &w.Name, &w.Plan, &suspended, &w.SuspendedReason, &w.CreatedAt,
		&w.OwnerEmail, &w.Members, &w.Contacts, &w.Conversations, &w.Messages30d, &w.LastActiveAt); err != nil {
		return nil, err
	}
	w.Status = "active"
	if suspended != nil {
		w.Status = "suspended"
	}
	return w, nil
}

// ListWorkspaces lists tenants, newest first.
func (sv *Service) ListWorkspaces(ctx context.Context, f WorkspaceFilter) ([]AdminWorkspace, error) {
	rows, err := sv.DB.QueryContext(ctx, `
		SELECT `+wsCols+` FROM workspaces w
		WHERE ($1 = '' OR w.name ILIKE $2 OR w.slug ILIKE $2)
		  AND ($3 = '' OR ($3 = 'suspended') = (w.suspended_at IS NOT NULL))
		ORDER BY w.created_at DESC LIMIT $4`,
		strings.TrimSpace(f.Q), like(f.Q), f.Status, limitOf(f.Limit, 500, 1000))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminWorkspace{}
	for rows.Next() {
		w, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

func (sv *Service) findWorkspace(ctx context.Context, ref string) (*AdminWorkspace, error) {
	w, err := scanWorkspace(sv.DB.QueryRowContext(ctx, `SELECT `+wsCols+` FROM workspaces w WHERE w.id = $1 OR w.slug = $1`,
		strings.TrimSpace(ref)))
	if isNoRows(err) {
		return nil, notFound("workspace")
	}
	return w, err
}

// countTables are the record counts the one-workspace view shows.
var countTables = []string{"contacts", "companies", "deals", "leads", "conversations", "campaigns", "automation_workflows", "automation_rules", "mcp_tokens"}

// GetWorkspace reads one tenant by id or slug, with its members and record counts.
func (sv *Service) GetWorkspace(ctx context.Context, ref string) (*AdminWorkspaceDetail, error) {
	w, err := sv.findWorkspace(ctx, ref)
	if err != nil {
		return nil, err
	}
	d := &AdminWorkspaceDetail{AdminWorkspace: *w, MemberList: []AdminMember{}, Counts: map[string]int{}}
	rows, err := sv.DB.QueryContext(ctx, `
		SELECT m.user_id, u.email, u.roles, m.role, m.status, p.last_seen_at, m.created_at
		FROM workspace_members m JOIN users u ON u.id = m.user_id
		LEFT JOIN platform_users p ON p.user_id = m.user_id
		WHERE m.workspace_id = $1
		ORDER BY array_position(ARRAY['owner','admin','manager','member','client'], m.role), u.email`, w.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m AdminMember
		var roles string
		if err := rows.Scan(&m.UserID, &m.Email, &roles, &m.Role, &m.Status, &m.LastSeenAt, &m.JoinedAt); err != nil {
			return nil, err
		}
		m.IsAdmin = strings.Contains(","+strings.ReplaceAll(roles, " ", "")+",", ",admin,")
		d.MemberList = append(d.MemberList, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, t := range countTables {
		var n int
		q := `SELECT count(*) FROM ` + t + ` WHERE workspace_id = $1`
		if t == "mcp_tokens" {
			q += ` AND revoked_at IS NULL`
		}
		if err := sv.DB.QueryRowContext(ctx, q, w.ID).Scan(&n); err != nil {
			return nil, err
		}
		d.Counts[t] = n
	}
	return d, nil
}

func (sv *Service) setWorkspaceStatus(ctx context.Context, a Actor, ref string, suspend bool, reason string) (*AdminWorkspaceDetail, error) {
	w, err := sv.findWorkspace(ctx, ref)
	if err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	err = sv.tx(ctx, func(tx *sql.Tx) error {
		action := "workspace.reactivate"
		var changes any
		if suspend {
			action, changes = "workspace.suspend", map[string]any{"reason": reason}
			_, err = tx.ExecContext(ctx, `UPDATE workspaces SET suspended_at = now(), suspended_reason = NULLIF($2, ''), updated_at = now() WHERE id = $1`, w.ID, reason)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE workspaces SET suspended_at = NULL, suspended_reason = NULL, updated_at = now() WHERE id = $1`, w.ID)
		}
		if err != nil {
			return err
		}
		return sv.audit(ctx, tx, a, action, "workspace", w.ID, w.Slug, changes)
	})
	if err != nil {
		return nil, err
	}
	return sv.GetWorkspace(ctx, w.ID)
}

// SuspendWorkspace closes a workspace to its members (403 on every call) without touching its data.
func (sv *Service) SuspendWorkspace(ctx context.Context, a Actor, ref, reason string) (*AdminWorkspaceDetail, error) {
	return sv.setWorkspaceStatus(ctx, a, ref, true, reason)
}

// ReactivateWorkspace lifts a suspension.
func (sv *Service) ReactivateWorkspace(ctx context.Context, a Actor, ref string) (*AdminWorkspaceDetail, error) {
	return sv.setWorkspaceStatus(ctx, a, ref, false, "")
}

// DeleteWorkspace removes a workspace and everything in it. confirm must be its slug.
func (sv *Service) DeleteWorkspace(ctx context.Context, a Actor, ref, confirm string) error {
	w, err := sv.findWorkspace(ctx, ref)
	if err != nil {
		return err
	}
	if strings.TrimSpace(confirm) != w.Slug {
		return invalid("type the workspace's slug to confirm")
	}
	if err := workspaces.Delete(ctx, sv.DB, w.ID); err != nil {
		return err
	}
	return sv.audit(ctx, sv.DB, a, "workspace.delete", "workspace", w.ID, w.Slug,
		map[string]any{"name": w.Name, "members": w.Members, "contacts": w.Contacts})
}

// SetPlan records a workspace's plan label. Moharrik has no plan catalog or limits yet (pricing is
// MH-429), so this is the label the admin MCP has always been able to set.
func (sv *Service) SetPlan(ctx context.Context, a Actor, ref, plan string) (*AdminWorkspaceDetail, error) {
	w, err := sv.findWorkspace(ctx, ref)
	if err != nil {
		return nil, err
	}
	plan = strings.TrimSpace(plan)
	if plan == "" || len(plan) > 40 {
		return nil, invalid("a plan is 1 to 40 characters")
	}
	err = sv.tx(ctx, func(tx *sql.Tx) error {
		if err := tenant.AuditTx(a.Context(ctx), tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE workspaces SET plan = $2, updated_at = now() WHERE id = $1`, w.ID, plan); err != nil {
			return err
		}
		return sv.audit(ctx, tx, a, "workspace.set_plan", "workspace", w.ID, w.Slug, map[string]any{"from": w.Plan, "to": plan})
	})
	if err != nil {
		return nil, err
	}
	return sv.GetWorkspace(ctx, w.ID)
}

func (sv *Service) memberTarget(ctx context.Context, wsRef, userRef string) (*AdminWorkspace, *AdminUserDetail, error) {
	w, err := sv.findWorkspace(ctx, wsRef)
	if err != nil {
		return nil, nil, err
	}
	u, err := sv.GetUser(ctx, userRef)
	if err != nil {
		return nil, nil, err
	}
	return w, u, nil
}

// AddMember puts an existing account into a workspace with a role, without an invitation.
func (sv *Service) AddMember(ctx context.Context, a Actor, wsRef, userRef, role string) (*AdminWorkspaceDetail, error) {
	if !authz.Valid(role) {
		return nil, invalid("unknown role: " + role)
	}
	w, u, err := sv.memberTarget(ctx, wsRef, userRef)
	if err != nil {
		return nil, err
	}
	err = sv.tx(ctx, func(tx *sql.Tx) error {
		if err := tenant.AuditTx(a.Context(ctx), tx); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, $3, 'active')
			ON CONFLICT (workspace_id, user_id) DO NOTHING`, w.ID, u.ID, role)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return conflict("already a member: change their role instead")
		}
		return sv.audit(ctx, tx, a, "member.add", "workspace", w.ID, w.Slug, map[string]any{"user": u.Email, "role": role})
	})
	if err != nil {
		return nil, err
	}
	return sv.GetWorkspace(ctx, w.ID)
}

// SetMemberRole changes a member's role. The workspace keeps at least one owner.
func (sv *Service) SetMemberRole(ctx context.Context, a Actor, wsRef, userRef, role string) (*AdminWorkspaceDetail, error) {
	w, u, err := sv.memberTarget(ctx, wsRef, userRef)
	if err != nil {
		return nil, err
	}
	// A platform admin acts with an owner's reach in any workspace.
	if err := workspaces.SetRole(a.Context(ctx), sv.DB, w.ID, string(authz.Owner), u.ID, role); err != nil {
		return nil, err
	}
	if err := sv.audit(ctx, sv.DB, a, "member.set_role", "workspace", w.ID, w.Slug, map[string]any{"user": u.Email, "role": role}); err != nil {
		return nil, err
	}
	return sv.GetWorkspace(ctx, w.ID)
}

// RemoveMember takes a member out; their MCP tokens and OAuth grants there are revoked.
func (sv *Service) RemoveMember(ctx context.Context, a Actor, wsRef, userRef string) (*AdminWorkspaceDetail, error) {
	w, u, err := sv.memberTarget(ctx, wsRef, userRef)
	if err != nil {
		return nil, err
	}
	if err := workspaces.RemoveMember(a.Context(ctx), sv.DB, w.ID, string(authz.Owner), u.ID, false); err != nil {
		return nil, err
	}
	if err := sv.audit(ctx, sv.DB, a, "member.remove", "workspace", w.ID, w.Slug, map[string]any{"user": u.Email}); err != nil {
		return nil, err
	}
	return sv.GetWorkspace(ctx, w.ID)
}
