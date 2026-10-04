package crm

/*
The contact timeline (MH-1064): everything one real source records about one contact, merged
newest first, one cursor page at a time.

Nothing is inferred. Each kind below is a row in a table that holds the contact's id:

	contact.created                       contacts.created_at
	activity.note|call|meeting|task       activities (on the contact, or on one of its deals)
	conversation                          conversations.contact_id (one entry, at its latest message)
	deal.opened|stage_changed|won|lost    deal_stage_events of deals.contact_id
	lifecycle.changed                     contact_lifecycle_events
	lead.submitted                        leads.contact_id (a converted lead, with its UTM columns)
	rule.ran                              automation_rule_runs whose subject or event names the contact

Campaign sends are NOT here: a campaign goes to a list and no per-recipient row is written, so
"this contact received this campaign" is not recorded anywhere. Leads that were never linked to
a contact are not matched by email either.

Every kind is gated by the permission that reads its source; a role (or MCP token) that cannot
read one leaves it out, never zeroes it. Events carry kind codes plus the data they came from;
the web writes the sentence (en/ar).
*/

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/paging"
	"github.com/fadymondy/moharrik/internal/tenant"
)

const (
	DefaultContactTimelinePage = 30
	MaxContactTimelinePage     = 100
	// MaxOverviewRows bounds the conversations and automations lists on the first page.
	MaxOverviewRows = 20
)

// ContactEvent is one dated thing that happened with a contact.
type ContactEvent struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind" doc:"contact.created | activity.note | activity.call | activity.meeting | activity.task | conversation | deal.opened | deal.stage_changed | deal.won | deal.lost | lifecycle.changed | lead.submitted | rule.ran"`
	// ID is the id of the record the event is about (activity, conversation, deal, lead, rule...).
	ID   string         `json:"id"`
	Data map[string]any `json:"data" doc:"The columns the event was made from; shape depends on kind."`
}

// ContactConversation is one inbox thread linked to the contact.
type ContactConversation struct {
	ID            string    `json:"id"`
	Channel       string    `json:"channel"`
	Subject       string    `json:"subject"`
	Status        string    `json:"status"`
	Unread        bool      `json:"unread"`
	Messages      int64     `json:"messages"`
	LastDirection string    `json:"last_direction" doc:"in | out | empty for an empty thread"`
	StartedAt     time.Time `json:"started_at"`
	LastMessageAt time.Time `json:"last_message_at"`
}

// ContactAutomation is one rule that has run for the contact, with how often.
type ContactAutomation struct {
	RuleID     string    `json:"rule_id"`
	RuleName   string    `json:"rule_name"`
	Trigger    string    `json:"trigger"`
	Runs       int64     `json:"runs"`
	LastStatus string    `json:"last_status"`
	LastAt     time.Time `json:"last_at"`
}

// ContactOverview rides on the first page only: the sections of the contact page that are lists
// of things rather than a stream. A section the caller cannot read is null.
type ContactOverview struct {
	Conversations []ContactConversation `json:"conversations" doc:"Null without inbox:read."`
	Automations   []ContactAutomation   `json:"automations" doc:"Null without automation:read."`
}

// ContactTimelineOut is one page.
type ContactTimelineOut struct {
	Contact    Ref            `json:"contact"`
	Events     []ContactEvent `json:"events"`
	NextCursor string         `json:"next_cursor"`
	// Sources are the kinds of event this caller can read, so the page can say what it is not showing.
	Sources  []string         `json:"sources" doc:"Event sources the caller may read: contact, activities, deals, lifecycle, conversations, leads, automations."`
	Overview *ContactOverview `json:"overview" doc:"First page only (no cursor); null on later pages."`
}

// canSee: the role has the permission and, for an MCP token or OAuth grant, so does the token.
func canSee(s *tenant.Scope, perm string) bool {
	if !authz.Can(s.Role, perm) {
		return false
	}
	if s.Via == "mcp" || s.Via == "oauth" {
		for _, sc := range s.Scopes {
			if sc == perm {
				return true
			}
		}
		return false
	}
	return true
}

type eventSource struct {
	name string
	perm string
	// sql selects (at, key, kind, ref_id, data) with $1 = workspace, $2 = contact.
	sql string
}

var contactEventSources = []eventSource{
	{"contact", authz.ContactsRead, `
		SELECT c.created_at, 'k:' || c.id, 'contact.created', c.id, '{}'::jsonb
		FROM contacts c WHERE c.workspace_id = $1 AND c.id = $2`},
	{"activities", authz.ContactsRead, `
		SELECT a.created_at, 'a:' || a.id, 'activity.' || a.kind, a.id,
		       jsonb_build_object('body', left(a.body, 400), 'due_at', a.due_at, 'done_at', a.done_at, 'created_by', a.created_by,
		                          'deal_id', a.deal_id, 'on_deal', a.contact_id IS DISTINCT FROM $2)
		FROM activities a
		WHERE a.workspace_id = $1 AND (a.contact_id = $2 OR a.deal_id IN (SELECT id FROM deals WHERE workspace_id = $1 AND contact_id = $2))`},
	{"deals", authz.ContactsRead, `
		SELECT e.entered_at, 'd:' || e.id,
		       CASE WHEN e.from_stage_id IS NULL THEN 'deal.opened'
		            WHEN st.kind = 'won' THEN 'deal.won' WHEN st.kind = 'lost' THEN 'deal.lost'
		            ELSE 'deal.stage_changed' END,
		       d.id,
		       jsonb_build_object('title', d.title, 'value_minor', d.value_minor, 'currency', d.currency,
		                          'stage', st.name, 'stage_kind', st.kind, 'from_stage', fs.name)
		FROM deal_stage_events e
		JOIN deals d ON d.workspace_id = e.workspace_id AND d.id = e.deal_id
		JOIN pipeline_stages st ON st.workspace_id = e.workspace_id AND st.id = e.stage_id
		LEFT JOIN pipeline_stages fs ON fs.workspace_id = e.workspace_id AND fs.id = e.from_stage_id
		WHERE e.workspace_id = $1 AND d.contact_id = $2`},
	{"lifecycle", authz.ContactsRead, `
		SELECT e.created_at, 'l:' || e.id, 'lifecycle.changed', e.id,
		       jsonb_build_object('from', e.from_stage, 'to', e.to_stage, 'source', e.source, 'actor_user_id', e.actor_user_id)
		FROM contact_lifecycle_events e WHERE e.workspace_id = $1 AND e.contact_id = $2`},
	{"conversations", authz.InboxRead, `
		SELECT c.last_message_at, 'c:' || c.id, 'conversation', c.id,
		       jsonb_build_object('channel', c.channel, 'subject', c.subject, 'status', c.status, 'started_at', c.created_at,
		         'messages', (SELECT count(*) FROM messages m WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id),
		         'inbound', (SELECT count(*) FROM messages m WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id AND m.direction = 'in'))
		FROM conversations c WHERE c.workspace_id = $1 AND c.contact_id = $2`},
	{"leads", authz.LeadsRead, `
		SELECT l.created_at, 'p:' || l.id, 'lead.submitted', l.id,
		       jsonb_build_object('form_name', l.form_name, 'status', l.status, 'message', left(l.message, 240),
		         'utm_source', l.utm_source, 'utm_medium', l.utm_medium, 'utm_campaign', l.utm_campaign,
		         'utm_term', l.utm_term, 'utm_content', l.utm_content, 'referrer', l.referrer, 'landing_page', l.landing_page,
		         'converted_at', l.converted_at, 'deal_id', l.deal_id)
		FROM leads l WHERE l.workspace_id = $1 AND l.contact_id = $2`},
	{"automations", authz.AutomationRead, `
		SELECT r.started_at, 'r:' || r.id, 'rule.ran', r.rule_id,
		       jsonb_build_object('rule_name', r.rule_name, 'trigger', r.trigger, 'status', r.status,
		         'reason_code', r.reason_code, 'reason_args', r.reason_args, 'run_id', r.id)
		FROM automation_rule_runs r
		WHERE r.workspace_id = $1 AND NOT r.dry_run AND (r.subject_id = $2 OR r.event->>'contact_id' = $2)`},
}

// ContactTimeline reads one page of a contact's merged timeline.
func ContactTimeline(ctx context.Context, db *sql.DB, id, cursor string, limit int) (*ContactTimelineOut, error) {
	s, err := scope(ctx, authz.ContactsRead)
	if err != nil {
		return nil, err
	}
	id = strings.TrimSpace(id)
	n, err := paging.Limit(limit, DefaultContactTimelinePage, MaxContactTimelinePage)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	cur, err := paging.Decode(cursor, 2)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	var curAt *time.Time
	var curKey *string
	if cur != nil {
		us, perr := strconv.ParseInt(cur[0], 10, 64)
		if perr != nil {
			return nil, huma.Error422UnprocessableEntity(paging.ErrBadCursor.Error())
		}
		at := time.UnixMicro(us).UTC()
		curAt, curKey = &at, &cur[1]
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the timeline")
	}
	defer func() { _ = tx.Rollback() }()
	fail := func(error) (*ContactTimelineOut, error) {
		return nil, huma.Error500InternalServerError("could not read the timeline")
	}

	var name, email string
	if err := tx.QueryRowContext(ctx, `SELECT name, email FROM contacts WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, id).Scan(&name, &email); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("contact not found")
		}
		return fail(err)
	}

	out := &ContactTimelineOut{Contact: Ref{ID: id, Name: firstOf(name, email)}, Events: []ContactEvent{}, Sources: []string{}}
	var parts []string
	for _, src := range contactEventSources {
		if !canSee(s, src.perm) {
			continue
		}
		out.Sources = append(out.Sources, src.name)
		parts = append(parts, src.sql)
	}
	if len(parts) > 0 {
		q := `SELECT e.at, e.key, e.kind, e.ref_id, e.data FROM (` + strings.Join(parts, "\nUNION ALL\n") + `) e(at, key, kind, ref_id, data)
			WHERE ($3::timestamptz IS NULL OR (e.at, e.key) < ($3::timestamptz, $4::text))
			ORDER BY e.at DESC, e.key DESC LIMIT $5`
		rows, err := tx.QueryContext(ctx, q, s.WorkspaceID, id, curAt, curKey, n+1)
		if err != nil {
			return fail(err)
		}
		defer rows.Close()
		var lastKey string
		for rows.Next() {
			var ev ContactEvent
			var key string
			var raw []byte
			if err := rows.Scan(&ev.At, &key, &ev.Kind, &ev.ID, &raw); err != nil {
				return fail(err)
			}
			if len(out.Events) == n { // the extra row: there is a next page
				out.NextCursor = paging.Encode(strconv.FormatInt(out.Events[n-1].At.UnixMicro(), 10), lastKey)
				break
			}
			ev.Data = map[string]any{}
			if err := json.Unmarshal(raw, &ev.Data); err != nil {
				return fail(err)
			}
			out.Events = append(out.Events, ev)
			lastKey = key
		}
		if err := rows.Err(); err != nil {
			return fail(err)
		}
		rows.Close()
	}

	if cursor == "" {
		ov := &ContactOverview{}
		if canSee(s, authz.InboxRead) {
			if ov.Conversations, err = contactConversations(ctx, tx, s.WorkspaceID, id); err != nil {
				return fail(err)
			}
		}
		if canSee(s, authz.AutomationRead) {
			if ov.Automations, err = contactAutomations(ctx, tx, s.WorkspaceID, id); err != nil {
				return fail(err)
			}
		}
		out.Overview = ov
	}
	return out, nil
}

func contactConversations(ctx context.Context, tx *sql.Tx, ws, id string) ([]ContactConversation, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT c.id, c.channel, c.subject, c.status, c.unread, c.created_at, c.last_message_at,
		       (SELECT count(*) FROM messages m WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id),
		       coalesce((SELECT m.direction FROM messages m WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id
		                 ORDER BY m.created_at DESC, m.id DESC LIMIT 1), '')
		FROM conversations c WHERE c.workspace_id = $1 AND c.contact_id = $2
		ORDER BY c.last_message_at DESC, c.id DESC LIMIT $3`, ws, id, MaxOverviewRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []ContactConversation{}
	for rows.Next() {
		var c ContactConversation
		if err := rows.Scan(&c.ID, &c.Channel, &c.Subject, &c.Status, &c.Unread, &c.StartedAt, &c.LastMessageAt, &c.Messages, &c.LastDirection); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

func contactAutomations(ctx context.Context, tx *sql.Tx, ws, id string) ([]ContactAutomation, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT r.rule_id, (array_agg(r.rule_name ORDER BY r.started_at DESC))[1], (array_agg(r.trigger ORDER BY r.started_at DESC))[1],
		       count(*), (array_agg(r.status ORDER BY r.started_at DESC))[1], max(r.started_at)
		FROM automation_rule_runs r
		WHERE r.workspace_id = $1 AND NOT r.dry_run AND (r.subject_id = $2 OR r.event->>'contact_id' = $2)
		GROUP BY r.rule_id ORDER BY max(r.started_at) DESC, r.rule_id LIMIT $3`, ws, id, MaxOverviewRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []ContactAutomation{}
	for rows.Next() {
		var a ContactAutomation
		if err := rows.Scan(&a.RuleID, &a.RuleName, &a.Trigger, &a.Runs, &a.LastStatus, &a.LastAt); err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}
