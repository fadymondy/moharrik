package admin

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/authz"
)

// StatDay is one point of a daily series.
type StatDay struct {
	Date  string `json:"date"` // YYYY-MM-DD, UTC
	Value int    `json:"value"`
}

// AdminTopWorkspace is a workspace ranked by recent activity.
type AdminTopWorkspace struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Messages30d int    `json:"messages_30d"`
	Members     int    `json:"members"`
}

// PlatformStats is the platform overview.
type PlatformStats struct {
	Users struct {
		Total     int `json:"total"`
		New30d    int `json:"new_30d"`
		Active7d  int `json:"active_7d"`
		Active30d int `json:"active_30d"`
		Admins    int `json:"admins"`
		Suspended int `json:"suspended"`
	} `json:"users"`
	Workspaces struct {
		Total     int `json:"total"`
		New30d    int `json:"new_30d"`
		Active30d int `json:"active_30d"`
		Suspended int `json:"suspended"`
	} `json:"workspaces"`
	Records struct {
		Contacts          int `json:"contacts"`
		Companies         int `json:"companies"`
		Deals             int `json:"deals"`
		Leads             int `json:"leads"`
		Conversations     int `json:"conversations"`
		Messages30d       int `json:"messages_30d"`
		Campaigns         int `json:"campaigns"`
		AutomationRuns30d int `json:"automation_runs_30d"`
	} `json:"records"`
	Roles         map[string]int      `json:"roles"`
	Signups       []StatDay           `json:"signups"`
	NewWorkspaces []StatDay           `json:"new_workspaces"`
	Messages      []StatDay           `json:"messages"`
	Top           []AdminTopWorkspace `json:"top_workspaces"`
}

const statDays = 30

// userTimeLayouts are the shapes the auth plugin has written users.created_at in (it is text).
var userTimeLayouts = []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05", "2006-01-02T15:04:05"}

func parseUserTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, l := range userTimeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func emptySeries(now time.Time) ([]StatDay, map[string]int) {
	out := make([]StatDay, statDays)
	idx := map[string]int{}
	start := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -(statDays - 1))
	for i := range out {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		out[i] = StatDay{Date: d}
		idx[d] = i
	}
	return out, idx
}

// dailySeries counts rows of table per day over the last 30 days (created_at is timestamptz).
func (sv *Service) dailySeries(ctx context.Context, table string, now time.Time) ([]StatDay, error) {
	out, idx := emptySeries(now)
	rows, err := sv.DB.QueryContext(ctx, `
		SELECT to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD'), count(*) FROM `+table+`
		WHERE created_at >= (now() AT TIME ZONE 'UTC')::date - $1::int
		GROUP BY 1`, statDays-1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			return nil, err
		}
		if i, ok := idx[d]; ok {
			out[i].Value = n
		}
	}
	return out, rows.Err()
}

// PlatformStats reads the platform overview. Every number is a count across all workspaces.
func (sv *Service) Stats(ctx context.Context) (*PlatformStats, error) {
	s := &PlatformStats{Roles: map[string]int{}}
	now := time.Now()

	err := sv.DB.QueryRowContext(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE (',' || replace(u.roles, ' ', '') || ',') LIKE '%,admin,%'),
		       count(*) FILTER (WHERE p.suspended_at IS NOT NULL),
		       count(*) FILTER (WHERE p.last_seen_at > now() - interval '7 days'),
		       count(*) FILTER (WHERE p.last_seen_at > now() - interval '30 days')
		FROM users u LEFT JOIN platform_users p ON p.user_id = u.id`,
	).Scan(&s.Users.Total, &s.Users.Admins, &s.Users.Suspended, &s.Users.Active7d, &s.Users.Active30d)
	if err != nil {
		return nil, err
	}

	// Signups: users.created_at is text, so the series is built here.
	signups, idx := emptySeries(now)
	rows, err := sv.DB.QueryContext(ctx, `SELECT created_at FROM users`)
	if err != nil {
		return nil, err
	}
	cutoff := now.AddDate(0, 0, -statDays)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		t, ok := parseUserTime(raw)
		if !ok {
			continue
		}
		if t.After(cutoff) {
			s.Users.New30d++
		}
		if i, ok := idx[t.Format("2006-01-02")]; ok {
			signups[i].Value++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.Signups = signups

	err = sv.DB.QueryRowContext(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE w.created_at > now() - interval '30 days'),
		       count(*) FILTER (WHERE w.suspended_at IS NOT NULL),
		       count(*) FILTER (WHERE EXISTS (
		           SELECT 1 FROM workspace_members m JOIN platform_users p ON p.user_id = m.user_id
		           WHERE m.workspace_id = w.id AND p.last_seen_at > now() - interval '30 days'))
		FROM workspaces w`,
	).Scan(&s.Workspaces.Total, &s.Workspaces.New30d, &s.Workspaces.Suspended, &s.Workspaces.Active30d)
	if err != nil {
		return nil, err
	}

	r := &s.Records
	err = sv.DB.QueryRowContext(ctx, `
		SELECT (SELECT count(*) FROM contacts), (SELECT count(*) FROM companies), (SELECT count(*) FROM deals),
		       (SELECT count(*) FROM leads), (SELECT count(*) FROM conversations),
		       (SELECT count(*) FROM messages WHERE created_at > now() - interval '30 days'),
		       (SELECT count(*) FROM campaigns),
		       (SELECT count(*) FROM automation_runs WHERE created_at > now() - interval '30 days')`,
	).Scan(&r.Contacts, &r.Companies, &r.Deals, &r.Leads, &r.Conversations, &r.Messages30d, &r.Campaigns, &r.AutomationRuns30d)
	if err != nil {
		return nil, err
	}

	rrows, err := sv.DB.QueryContext(ctx, `SELECT role, count(*) FROM workspace_members WHERE status = 'active' GROUP BY role`)
	if err != nil {
		return nil, err
	}
	for rrows.Next() {
		var role string
		var n int
		if err := rrows.Scan(&role, &n); err != nil {
			rrows.Close()
			return nil, err
		}
		s.Roles[role] = n
	}
	rrows.Close()

	if s.NewWorkspaces, err = sv.dailySeries(ctx, "workspaces", now); err != nil {
		return nil, err
	}
	if s.Messages, err = sv.dailySeries(ctx, "messages", now); err != nil {
		return nil, err
	}

	trows, err := sv.DB.QueryContext(ctx, `
		SELECT w.id, w.slug, w.name,
		       (SELECT count(*) FROM messages x WHERE x.workspace_id = w.id AND x.created_at > now() - interval '30 days') AS n,
		       (SELECT count(*) FROM workspace_members m WHERE m.workspace_id = w.id)
		FROM workspaces w ORDER BY n DESC, w.created_at LIMIT 8`)
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	s.Top = []AdminTopWorkspace{}
	for trows.Next() {
		var t AdminTopWorkspace
		if err := trows.Scan(&t.ID, &t.Slug, &t.Name, &t.Messages30d, &t.Members); err != nil {
			return nil, err
		}
		s.Top = append(s.Top, t)
	}
	return s, trows.Err()
}

// AdminAuditEntry is one admin action as the log shows it.
type AdminAuditEntry struct {
	ID          string          `json:"id"`
	ActorID     string          `json:"actor_id"`
	ActorEmail  string          `json:"actor_email"`
	Action      string          `json:"action"`
	TargetType  string          `json:"target_type"`
	TargetID    string          `json:"target_id"`
	TargetLabel string          `json:"target_label"`
	Changes     json.RawMessage `json:"changes,omitempty"`
	IP          string          `json:"ip"`
	Via         string          `json:"via"`
	At          time.Time       `json:"at"`
}

// AuditFilter narrows the log: Q matches the actor, action or target; Target is an id.
type AuditFilter struct {
	Q, Target string
	Limit     int
}

// Audit lists admin actions, newest first.
func (sv *Service) Audit(ctx context.Context, f AuditFilter) ([]AdminAuditEntry, error) {
	rows, err := sv.DB.QueryContext(ctx, `
		SELECT id::text, COALESCE(actor_id, ''), COALESCE(actor_email, ''), action, COALESCE(target_type, ''),
		       COALESCE(target_id, ''), COALESCE(target_label, ''), changes, COALESCE(ip, ''), via, at
		FROM admin_audit
		WHERE ($1 = '' OR actor_email ILIKE $2 OR action ILIKE $2 OR target_label ILIKE $2)
		  AND ($3 = '' OR target_id = $3)
		ORDER BY at DESC LIMIT $4`,
		strings.TrimSpace(f.Q), like(f.Q), strings.TrimSpace(f.Target), limitOf(f.Limit, 200, 1000))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminAuditEntry{}
	for rows.Next() {
		var e AdminAuditEntry
		var changes []byte
		if err := rows.Scan(&e.ID, &e.ActorID, &e.ActorEmail, &e.Action, &e.TargetType, &e.TargetID, &e.TargetLabel,
			&changes, &e.IP, &e.Via, &e.At); err != nil {
			return nil, err
		}
		if len(changes) > 0 {
			e.Changes = json.RawMessage(changes)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PlatformPermission is one action with the lowest workspace role that may perform it.
type PlatformPermission struct {
	Action  string `json:"action"`
	Group   string `json:"group"`
	MinRole string `json:"min_role"` // empty: no role may
}

// PlatformPermissions is the workspace role × action matrix (authz.Matrix), read-only: roles and what
// they may do are code, reviewed like code.
type PlatformPermissions struct {
	Roles   []string             `json:"roles"`
	Actions []PlatformPermission `json:"actions"`
}

// PermissionMatrix returns the matrix, grouped by the action's prefix.
func PermissionMatrix() PlatformPermissions {
	p := PlatformPermissions{Roles: []string{string(authz.Owner), string(authz.Admin), string(authz.Manager), string(authz.Member), string(authz.Client)}}
	for action, min := range authz.Matrix {
		group, _, _ := strings.Cut(action, ":")
		p.Actions = append(p.Actions, PlatformPermission{Action: action, Group: group, MinRole: string(min)})
	}
	sort.Slice(p.Actions, func(i, j int) bool {
		if p.Actions[i].Group != p.Actions[j].Group {
			return p.Actions[i].Group < p.Actions[j].Group
		}
		return p.Actions[i].Action < p.Actions[j].Action
	})
	return p
}
