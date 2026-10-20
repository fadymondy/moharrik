// One Channels page (MH-571): every inbox channel connection of every kind,
// in one list. This file only reads — mailboxes, chat_widgets,
// whatsapp_links, telegram_channels, slack_channels, discord_channels and
// workflow_channels each keep their own settings CRUD (mailbox_routes.go in
// internal/inbox, chat_widgets' own routes, whatsapp.Register,
// telegram_routes.go/slack_routes.go/discord_routes.go/workflow_routes.go
// here) — this is the read-only cross-type view the settings page's "one
// list" asks for.
//
// Status rule, one per source table (there is no existing convention to copy
// — this is the first place these six tables' health is compared side by
// side):
//
//   - mailboxes, telegram_channels, slack_channels, discord_channels,
//     workflow_channels: every one of these clears its own last_error to ”
//     the moment it next succeeds (see mailbox_routes.go, telegram.go,
//     slack.go, discord.go, workflow.go — each UPDATE on a success path sets
//     last_error = ”). So last_error <> ” already means "the most recent
//     attempt failed", with no extra timestamp comparison needed:
//     paused   = not enabled
//     error    = enabled AND last_error <> ”
//     connected = enabled AND last_error = ”
//   - whatsapp_links: one row per linked number (several per workspace since
//     multi-number, 2026-10), no enabled column. state is the
//     source of truth (internal/whatsapp/service.go clears last_error the
//     moment state becomes 'open'):
//     connected = state = 'open'
//     error     = state <> 'open' AND last_error <> ”  (it was linked, then broke)
//     paused    = state <> 'open' AND last_error = ”   (never linked, or a
//     clean disconnect — nothing to call broken)
//   - chat_widgets: no last_error column at all — a widget has no "broken"
//     state, only on/off:
//     paused    = not enabled
//     connected = enabled
package channels

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/paging"
)

// ChannelConnection is one row of the aggregate list: one connection, of
// whatever kind, with enough of its own settings to render a card and enough
// of its conversations to show recent activity. Never carries a sealed value
// or anything derived from one beyond the has_token-style booleans the
// per-type list endpoints already expose — this file does not even select
// the *_sealed columns.
type ChannelConnection struct {
	ID      string `json:"id"`
	Type    string `json:"type"` // email | chat | whatsapp | telegram | slack | discord | workflow
	Network string `json:"network,omitempty" doc:"Only on type=workflow: linkedin, facebook, instagram or x."`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Status  string `json:"status"` // connected | error | paused

	Phone string `json:"phone,omitempty" doc:"Only on type=whatsapp."`

	LastError   string     `json:"last_error,omitempty"`
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`

	LastMessageAt    *time.Time `json:"last_message_at,omitempty"`
	Conversations30d int        `json:"conversations_30d"`
	Unread           int        `json:"unread"`

	CreatedAt time.Time `json:"created_at"`
}

// ChannelFilter narrows the aggregate list.
type ChannelFilter struct {
	// Types is a multi-select over email, chat, whatsapp, telegram, slack,
	// discord, workflow. Empty means every type.
	Types []string
	// Status narrows to connected, error or paused. Empty means every status.
	Status string
	// Q is a case-insensitive substring match on the row's name (and, for
	// whatsapp, its phone).
	Q string

	Cursor string
	Limit  int
}

// ChannelList is one page of the aggregate list.
type ChannelList struct {
	Channels   []ChannelConnection `json:"channels"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

var validChannelTypes = map[string]bool{
	"email": true, "chat": true, "whatsapp": true, "telegram": true,
	"slack": true, "discord": true, "workflow": true,
}

// unionSelect returns every connection of every kind for one workspace, each
// tagged with its type, and already carrying the status this file's own
// package comment documents — the CASE per branch, not a shared one, because
// the rule genuinely differs table to table (see the package comment above).
const unionSelect = `
	SELECT 'email', '', id, name, enabled,
	       CASE WHEN NOT enabled THEN 'paused' WHEN last_error <> '' THEN 'error' ELSE 'connected' END,
	       ''::text, last_error, last_error_at, created_at
	FROM mailboxes WHERE workspace_id = $1
	UNION ALL
	SELECT 'chat', '', id, name, enabled,
	       CASE WHEN NOT enabled THEN 'paused' ELSE 'connected' END,
	       ''::text, ''::text, NULL::timestamptz, created_at
	FROM chat_widgets WHERE workspace_id = $1
	UNION ALL
	SELECT 'whatsapp', '', id, coalesce(nullif(label, ''), nullif(phone, ''), 'WhatsApp'), (state = 'open'),
	       CASE WHEN state = 'open' THEN 'connected' WHEN last_error <> '' THEN 'error' ELSE 'paused' END,
	       phone, last_error, last_error_at, created_at
	FROM whatsapp_links WHERE workspace_id = $1
	UNION ALL
	SELECT 'telegram', '', id, name, enabled,
	       CASE WHEN NOT enabled THEN 'paused' WHEN last_error <> '' THEN 'error' ELSE 'connected' END,
	       ''::text, last_error, last_error_at, created_at
	FROM telegram_channels WHERE workspace_id = $1
	UNION ALL
	SELECT 'slack', '', id, name, enabled,
	       CASE WHEN NOT enabled THEN 'paused' WHEN last_error <> '' THEN 'error' ELSE 'connected' END,
	       ''::text, last_error, last_error_at, created_at
	FROM slack_channels WHERE workspace_id = $1
	UNION ALL
	SELECT 'discord', '', id, name, enabled,
	       CASE WHEN NOT enabled THEN 'paused' WHEN last_error <> '' THEN 'error' ELSE 'connected' END,
	       ''::text, last_error, last_error_at, created_at
	FROM discord_channels WHERE workspace_id = $1
	UNION ALL
	SELECT 'workflow', network, id, name, enabled,
	       CASE WHEN NOT enabled THEN 'paused' WHEN last_error <> '' THEN 'error' ELSE 'connected' END,
	       ''::text, last_error, last_error_at, created_at
	FROM workflow_channels WHERE workspace_id = $1`

type convStats struct {
	lastMessageAt sql.NullTime
	c30d          int
	unread        int
}

// ListChannels is the One Channels page's whole answer: every connection of
// every kind in this workspace, in one call. A workspace's connections are
// tens, not thousands (MH-571's own framing), so this reads everything and
// filters/sorts/pages in Go rather than building a fully generic keyset SQL
// query across a UNION ALL of six differently-shaped tables — correctness
// here matters far more than a perfectly reusable cursor.
func ListChannels(ctx context.Context, a *app.App, workspaceID string, f ChannelFilter) (*ChannelList, error) {
	limit, err := paging.Limit(f.Limit, 50, 200)
	if err != nil {
		return nil, err
	}

	rows, err := a.SQLDB.QueryContext(ctx, unionSelect, workspaceID)
	if err != nil {
		return nil, err
	}
	all := []ChannelConnection{}
	for rows.Next() {
		var c ChannelConnection
		var lastErrorAt sql.NullTime
		if err := rows.Scan(&c.Type, &c.Network, &c.ID, &c.Name, &c.Enabled, &c.Status,
			&c.Phone, &c.LastError, &lastErrorAt, &c.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if lastErrorAt.Valid {
			t := lastErrorAt.Time
			c.LastErrorAt = &t
		}
		all = append(all, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	// ---- conversation stats, one grouped query per link column -------------
	//
	// telegram/slack/discord/workflow all key off source_channel_id, which is
	// unique per row regardless of which of those four tables it came from
	// (internal/db/post/160_channels.sql), so one GROUP BY covers all four.
	bySource, err := groupConvStats(ctx, a, workspaceID, "source_channel_id")
	if err != nil {
		return nil, err
	}
	byMailbox, err := groupConvStats(ctx, a, workspaceID, "mailbox_id")
	if err != nil {
		return nil, err
	}
	byWidget, err := groupConvStats(ctx, a, workspaceID, "widget_id")
	if err != nil {
		return nil, err
	}
	waStats, err := whatsappConvStats(ctx, a, workspaceID)
	if err != nil {
		return nil, err
	}

	for i := range all {
		var s convStats
		var ok bool
		switch all[i].Type {
		case "email":
			s, ok = byMailbox[all[i].ID]
		case "chat":
			s, ok = byWidget[all[i].ID]
		case "whatsapp":
			s, ok = waStats[all[i].ID]
		default: // telegram, slack, discord, workflow
			s, ok = bySource[all[i].ID]
		}
		if ok {
			all[i].Conversations30d = s.c30d
			all[i].Unread = s.unread
			if s.lastMessageAt.Valid {
				t := s.lastMessageAt.Time
				all[i].LastMessageAt = &t
			}
		}
	}

	// ---- filter --------------------------------------------------------
	var typeSet map[string]bool
	if len(f.Types) > 0 {
		typeSet = map[string]bool{}
		for _, t := range f.Types {
			t = strings.TrimSpace(strings.ToLower(t))
			if t != "" {
				typeSet[t] = true
			}
		}
	}
	q := strings.ToLower(strings.TrimSpace(f.Q))
	status := strings.TrimSpace(strings.ToLower(f.Status))

	filtered := make([]ChannelConnection, 0, len(all))
	for _, c := range all {
		if typeSet != nil && !typeSet[c.Type] {
			continue
		}
		if status != "" && c.Status != status {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(c.Name), q) && !strings.Contains(strings.ToLower(c.Phone), q) {
			continue
		}
		filtered = append(filtered, c)
	}

	// ---- sort: error first, then by name (MH-571: "A channel in error is
	// listed first.") -----------------------------------------------------
	sort.SliceStable(filtered, func(i, j int) bool {
		ri, rj := errRank(filtered[i].Status), errRank(filtered[j].Status)
		if ri != rj {
			return ri < rj
		}
		ni, nj := strings.ToLower(filtered[i].Name), strings.ToLower(filtered[j].Name)
		if ni != nj {
			return ni < nj
		}
		return filtered[i].ID < filtered[j].ID
	})

	// ---- cursor + limit --------------------------------------------------
	start := 0
	if f.Cursor != "" {
		parts, err := paging.Decode(f.Cursor, 3)
		if err != nil {
			return nil, err
		}
		if parts != nil {
			cur := [3]string{parts[0], parts[1], parts[2]}
			for i, c := range filtered {
				key := [3]string{rankStr(c.Status), strings.ToLower(c.Name), c.ID}
				if tupleGreater(key, cur) {
					start = i
					goto found
				}
			}
			start = len(filtered)
		found:
		}
	}

	end := start + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	page := filtered[start:end]

	res := &ChannelList{Channels: page}
	if end < len(filtered) && len(page) > 0 {
		last := page[len(page)-1]
		res.NextCursor = paging.Encode(rankStr(last.Status), strings.ToLower(last.Name), last.ID)
	}
	return res, nil
}

func errRank(status string) int {
	if status == "error" {
		return 0
	}
	return 1
}

// rankStr is errRank as the single digit paging.Encode's cursor stores —
// ordinary string comparison on "0"/"1" sorts the same as the int.
func rankStr(status string) string {
	if status == "error" {
		return "0"
	}
	return "1"
}

func tupleGreater(a, b [3]string) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// groupConvStats aggregates conversations over the last 30 days, grouped by
// one nullable link column (mailbox_id, widget_id or source_channel_id).
// column is never user input — it is always one of those three literals — so
// building the query string is safe.
func groupConvStats(ctx context.Context, a *app.App, workspaceID, column string) (map[string]convStats, error) {
	q := `
		SELECT ` + column + `,
		       max(last_message_at),
		       count(*) FILTER (WHERE created_at > now() - interval '30 days'),
		       count(*) FILTER (WHERE unread)
		FROM conversations
		WHERE workspace_id = $1 AND ` + column + ` IS NOT NULL
		GROUP BY ` + column
	rows, err := a.SQLDB.QueryContext(ctx, q, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]convStats{}
	for rows.Next() {
		var key string
		var s convStats
		if err := rows.Scan(&key, &s.lastMessageAt, &s.c30d, &s.unread); err != nil {
			return nil, err
		}
		out[key] = s
	}
	return out, rows.Err()
}

// whatsappConvStats aggregates WhatsApp conversations per workspace number
// (conversations.whatsapp_link_id). A thread with no number on it yet (one
// nothing has arrived on since numbers were told apart) counts towards the
// default number, the one it answers from.
func whatsappConvStats(ctx context.Context, a *app.App, workspaceID string) (map[string]convStats, error) {
	rows, err := a.SQLDB.QueryContext(ctx, `
		SELECT coalesce(nullif(c.whatsapp_link_id, ''),
		                (SELECT l.id FROM whatsapp_links l WHERE l.workspace_id = $1 AND l.is_default LIMIT 1), ''),
		       max(c.last_message_at),
		       count(*) FILTER (WHERE c.created_at > now() - interval '30 days'),
		       count(*) FILTER (WHERE c.unread)
		FROM conversations c WHERE c.workspace_id = $1 AND c.channel = 'whatsapp'
		GROUP BY 1`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]convStats{}
	for rows.Next() {
		var key string
		var s convStats
		if err := rows.Scan(&key, &s.lastMessageAt, &s.c30d, &s.unread); err != nil {
			return nil, err
		}
		out[key] = s
	}
	return out, rows.Err()
}
