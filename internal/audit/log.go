package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/paging"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// AuditEntry is one audit row as the Settings page and the audit_log tool see it.
type AuditEntry struct {
	ID         string          `json:"id"`
	OccurredAt time.Time       `json:"occurred_at"`
	Kind       string          `json:"kind" doc:"change: a row changed; request: a write that changed no audited row (refused, sent, synced)"`
	RequestID  string          `json:"request_id"`
	ActorKind  string          `json:"actor_kind" doc:"member | token | oauth | agent | admin"`
	ActorID    string          `json:"actor_id"`
	ActorName  string          `json:"actor_name"`
	UserID     string          `json:"user_id"`
	Via        string          `json:"via"`
	Operation  string          `json:"operation"`
	Action     string          `json:"action"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Changes    json.RawMessage `json:"changes" doc:"{field: {before, after}}; secrets are [redacted], message bodies [hidden]"`
	Status     int             `json:"status"`
	IP         string          `json:"ip"`
	UserAgent  string          `json:"user_agent"`
}

// Filter narrows the list. Every field is optional.
type Filter struct {
	Actor      string // actor_id
	ActorKind  string
	EntityType string
	EntityID   string
	Action     string // create | update | delete | an operation
	From, To   *time.Time
	Cursor     string
	Limit      int
}

// AuditPage is one page of the log, newest first.
type AuditPage struct {
	Entries    []AuditEntry `json:"entries"`
	NextCursor string       `json:"next_cursor"`
	// Total is every entry matching the filter (MH-467's list shape).
	Total int `json:"total"`
	Limit int `json:"limit"`
}

var ErrForbidden = errors.New("the audit log is for owners and admins of this workspace")

// List is one page of the caller's workspace's log, newest first. It runs on
// the app pool, so RLS confines it to the workspace whatever the filter says.
func List(ctx context.Context, db *sql.DB, f Filter) (*AuditPage, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, tenant.ErrNotMember
	}
	if !authz.Can(s.Role, authz.AuditRead) {
		return nil, ErrForbidden
	}
	limit, err := paging.Limit(f.Limit, 50, 200)
	if err != nil {
		return nil, err
	}
	cur, err := paging.Decode(f.Cursor, 2)
	if err != nil {
		return nil, err
	}
	args := []any{s.WorkspaceID}
	where := []string{"workspace_id = $1"}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if f.Actor != "" {
		add("actor_id = ?", f.Actor)
	}
	if f.ActorKind != "" {
		add("actor_kind = ?", f.ActorKind)
	}
	if f.EntityType != "" {
		add("entity_type = ?", f.EntityType)
	}
	if f.EntityID != "" {
		add("entity_id = ?", f.EntityID)
	}
	if f.Action != "" {
		add("action = ?", f.Action)
	}
	if f.From != nil {
		add("occurred_at >= ?", *f.From)
	}
	if f.To != nil {
		add("occurred_at < ?", *f.To)
	}
	page := &AuditPage{Entries: []AuditEntry{}, Limit: limit}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE `+strings.Join(where, " AND "), args...).Scan(&page.Total); err != nil {
		return nil, err
	}
	if cur != nil {
		ts, err := time.Parse(time.RFC3339Nano, cur[0])
		if err != nil {
			return nil, paging.ErrBadCursor
		}
		args = append(args, ts, cur[1])
		n := len(args)
		where = append(where, "(occurred_at, id) < ($"+strconv.Itoa(n-1)+", $"+strconv.Itoa(n)+")")
	}
	args = append(args, limit+1)
	rows, err := db.QueryContext(ctx, `
		SELECT id, occurred_at, kind, request_id, actor_kind, actor_id, actor_name, user_id, via, operation,
		       action, entity_type, entity_id, changes::text, status, ip, user_agent
		FROM audit_log WHERE `+strings.Join(where, " AND ")+`
		ORDER BY occurred_at DESC, id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e AuditEntry
		var changes string
		if err := rows.Scan(&e.ID, &e.OccurredAt, &e.Kind, &e.RequestID, &e.ActorKind, &e.ActorID, &e.ActorName, &e.UserID,
			&e.Via, &e.Operation, &e.Action, &e.EntityType, &e.EntityID, &changes, &e.Status, &e.IP, &e.UserAgent); err != nil {
			return nil, err
		}
		e.Changes = json.RawMessage(changes)
		page.Entries = append(page.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(page.Entries) > limit {
		page.Entries = page.Entries[:limit]
		last := page.Entries[limit-1]
		page.NextCursor = paging.Encode(last.OccurredAt.UTC().Format(time.RFC3339Nano), last.ID)
	}
	return page, nil
}

// AuditActor is one actor that appears in the log, for the filter.
type AuditActor struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AuditFacets are the values the page's filters offer: who has acted, on what, how.
type AuditFacets struct {
	Actors        []AuditActor `json:"actors"`
	EntityTypes   []string     `json:"entity_types"`
	Actions       []string     `json:"actions"`
	RetentionDays int          `json:"retention_days"`
}

// GetFacets lists the distinct actors, entity types and actions in the log.
func GetFacets(ctx context.Context, db *sql.DB) (*AuditFacets, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, tenant.ErrNotMember
	}
	if !authz.Can(s.Role, authz.AuditRead) {
		return nil, ErrForbidden
	}
	out := &AuditFacets{Actors: []AuditActor{}, EntityTypes: []string{}, Actions: []string{}}
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT ON (actor_id) actor_kind, actor_id, actor_name FROM audit_log
		WHERE workspace_id = $1 AND actor_id <> '' ORDER BY actor_id, occurred_at DESC LIMIT 500`, s.WorkspaceID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a AuditActor
		if err := rows.Scan(&a.Kind, &a.ID, &a.Name); err != nil {
			rows.Close()
			return nil, err
		}
		out.Actors = append(out.Actors, a)
	}
	rows.Close()
	for _, q := range []struct {
		sql  string
		into *[]string
	}{
		{`SELECT DISTINCT entity_type FROM audit_log WHERE workspace_id = $1 AND entity_type <> '' ORDER BY 1 LIMIT 200`, &out.EntityTypes},
		{`SELECT DISTINCT action FROM audit_log WHERE workspace_id = $1 AND action <> '' ORDER BY 1 LIMIT 300`, &out.Actions},
	} {
		r, err := db.QueryContext(ctx, q.sql, s.WorkspaceID)
		if err != nil {
			return nil, err
		}
		for r.Next() {
			var v string
			if err := r.Scan(&v); err == nil {
				*q.into = append(*q.into, v)
			}
		}
		r.Close()
	}
	out.RetentionDays, err = Retention(ctx, db, s.WorkspaceID)
	return out, err
}

// DefaultRetentionDays is how long a workspace keeps its log unless an admin
// changes it.
const DefaultRetentionDays = 365

// Retention is the workspace's retention window in days.
func Retention(ctx context.Context, db *sql.DB, workspaceID string) (int, error) {
	var days int
	err := db.QueryRowContext(ctx, `SELECT retention_days FROM audit_settings WHERE workspace_id = $1`, workspaceID).Scan(&days)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultRetentionDays, nil
	}
	return days, err
}

// SetRetention changes the window (30 to 3650 days). Admins only; the change
// is itself recorded by the trigger on audit_settings.
func SetRetention(ctx context.Context, db *sql.DB, days int) (int, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return 0, tenant.ErrNotMember
	}
	if !authz.Can(s.Role, authz.AuditManage) {
		return 0, ErrForbidden
	}
	if days < 30 || days > 3650 {
		return 0, errors.New("keep the log between 30 and 3650 days")
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO audit_settings (workspace_id, retention_days) VALUES ($1, $2)
		ON CONFLICT (workspace_id) DO UPDATE SET retention_days = EXCLUDED.retention_days, updated_at = now()`,
		s.WorkspaceID, days)
	return days, err
}
