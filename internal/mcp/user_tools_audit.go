package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/audit"
)

// AuditTools is the audit log over MCP (MH-392): the same list, filters and
// paging as Settings → Audit log, for owners and admins (audit:read).
func AuditTools() []Tool {
	return []Tool{{
		Name: "audit_log",
		Description: "The workspace's audit log, newest first: every write by a member, an MCP token, an OAuth client or an agent — " +
			"who (actor_kind, actor_id, actor_name), what (action, entity_type, entity_id), when, from where (ip, user_agent), " +
			"and a before/after diff of the changed fields with secrets redacted. Filter by actor, entity or action and a date " +
			"range; follow next_cursor for older entries.",
		Scope: "audit:read",
		InputSchema: schema(nil, map[string]any{
			"actor":       prop("string", "An actor id: a user id, token id, OAuth grant id or browser id"),
			"actor_kind":  map[string]any{"type": "string", "enum": []string{"member", "token", "oauth", "agent", "admin"}},
			"entity_type": prop("string", "A table, e.g. contacts, deals, campaigns"),
			"entity_id":   prop("string", "The id of one record"),
			"action":      prop("string", "create, update, delete, or an operation such as mcp:send_campaign"),
			"from":        prop("string", "On or after this time (RFC 3339 or YYYY-MM-DD)"),
			"to":          prop("string", "Before this time (RFC 3339 or YYYY-MM-DD)"),
			"cursor":      prop("string", "next_cursor from the previous page"),
			"limit":       prop("integer", "Page size (default 50, at most 200)"),
		}),
		Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
			var in struct {
				Actor      string `json:"actor"`
				ActorKind  string `json:"actor_kind"`
				EntityType string `json:"entity_type"`
				EntityID   string `json:"entity_id"`
				Action     string `json:"action"`
				From       string `json:"from"`
				To         string `json:"to"`
				Cursor     string `json:"cursor"`
				Limit      int    `json:"limit"`
			}
			if err := bind(raw, &in); err != nil {
				return "", err
			}
			f := audit.Filter{Actor: in.Actor, ActorKind: in.ActorKind, EntityType: in.EntityType, EntityID: in.EntityID,
				Action: in.Action, Cursor: in.Cursor, Limit: in.Limit}
			for _, d := range []struct {
				v    string
				into **time.Time
				name string
			}{{in.From, &f.From, "from"}, {in.To, &f.To, "to"}} {
				if d.v == "" {
					continue
				}
				t, err := parseWhen(d.v)
				if err != nil {
					return "", fmt.Errorf("%s: %w", d.name, err)
				}
				*d.into = &t
			}
			page, err := audit.List(ctx, a.SQLDB, f)
			if errors.Is(err, audit.ErrForbidden) {
				return "", err
			}
			if err != nil {
				return "", fmt.Errorf("could not read the audit log: %w", err)
			}
			return jsonResult(page)
		},
	}}
}

func parseWhen(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, errors.New("use RFC 3339 or YYYY-MM-DD")
}
