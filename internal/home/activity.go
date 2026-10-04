package home

import (
	"database/sql"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/authz"
)

// auditEntity says how an audit_log table reads as an activity event and who may see it.
type auditEntity struct {
	table   string
	noun    string // contact | company | deal | lead | campaign | rule | activity
	link    string // EntityRef type, "" for none
	perm    string
	creates bool // only creations are interesting (activities)
}

var auditEntities = []auditEntity{
	{"contacts", "contact", "contact", authz.ContactsRead, false},
	{"companies", "company", "company", authz.ContactsRead, false},
	{"deals", "deal", "deal", authz.ContactsRead, false},
	{"activities", "activity", "", authz.ContactsRead, true},
	{"leads", "lead", "lead", authz.LeadsRead, false},
	{"campaigns", "campaign", "campaign", authz.CampaignsRead, false},
	{"automation_rules", "rule", "rule", authz.AutomationRead, false},
}

// auditFilter is the SQL predicate for the audit rows the caller may see, with literal table
// names from the fixed list above (never user input). "" means nothing is readable.
func auditFilter(can func(string) bool, alias string) string {
	var parts []string
	for _, e := range auditEntities {
		if !can(e.perm) {
			continue
		}
		p := alias + ".entity_type = '" + e.table + "'"
		if e.creates {
			p += " AND " + alias + ".action = 'create'"
		}
		parts = append(parts, "("+p+")")
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

func auditByTable(table string) (auditEntity, bool) {
	for _, e := range auditEntities {
		if e.table == table {
			return e, true
		}
	}
	return auditEntity{}, false
}

// activity merges the newest events of every readable source and keeps the latest ActivityLimit.
func (b *builder) activity(can func(string) bool) []HomeEvent {
	out := []HomeEvent{}

	if f := auditFilter(can, "a"); f != "" {
		b.each(`
			SELECT a.occurred_at, a.action, a.entity_type, a.entity_id, a.actor_kind, a.actor_name, a.via,
			       (a.changes ? 'stage_id'),
			       coalesce(
			         CASE a.entity_type
			           WHEN 'contacts'  THEN (SELECT nullif(x.name, '') FROM contacts x WHERE x.workspace_id = a.workspace_id AND x.id = a.entity_id)
			           WHEN 'companies' THEN (SELECT nullif(x.name, '') FROM companies x WHERE x.workspace_id = a.workspace_id AND x.id = a.entity_id)
			           WHEN 'deals'     THEN (SELECT nullif(x.title, '') FROM deals x WHERE x.workspace_id = a.workspace_id AND x.id = a.entity_id)
			           WHEN 'leads'     THEN (SELECT coalesce(nullif(x.name, ''), nullif(x.email, '')) FROM leads x WHERE x.workspace_id = a.workspace_id AND x.id = a.entity_id)
			           WHEN 'campaigns' THEN (SELECT nullif(x.subject, '') FROM campaigns x WHERE x.workspace_id = a.workspace_id AND x.id = a.entity_id)
			           WHEN 'automation_rules' THEN (SELECT nullif(x.name, '') FROM automation_rules x WHERE x.workspace_id = a.workspace_id AND x.id = a.entity_id)
			         END,
			         a.changes->'name'->>'after', a.changes->'name'->>'before',
			         a.changes->'title'->>'after', a.changes->'title'->>'before',
			         a.changes->'subject'->>'after', a.changes->'subject'->>'before',
			         a.changes->'email'->>'after', ''),
			       coalesce(a.changes->'contact_id'->>'after', ''), coalesce(a.changes->'kind'->>'after', '')
			FROM audit_log a
			WHERE a.workspace_id = $1 AND a.action IN ('create', 'update', 'delete') AND a.changes <> '{}'::jsonb
			  AND `+f+`
			ORDER BY a.occurred_at DESC LIMIT `+itoa(ActivityLimit), nil, func(r *sql.Rows) error {
			var at time.Time
			var action, table, id, actorKind, actorName, via, subject, contactID, actKind string
			var stage bool
			if err := r.Scan(&at, &action, &table, &id, &actorKind, &actorName, &via, &stage, &subject, &contactID, &actKind); err != nil {
				return err
			}
			e, ok := auditByTable(table)
			if !ok {
				return nil
			}
			verb := map[string]string{"create": "created", "update": "updated", "delete": "deleted"}[action]
			if e.noun == "deal" && action == "update" && stage {
				verb = "stage_changed"
			}
			ev := HomeEvent{Kind: e.noun + "." + verb, OccurredAt: at, Subject: subject}
			switch {
			case e.noun == "activity":
				ev.Status = actKind // note | call | meeting | task
				if contactID != "" {
					ev.Link = &HomeEntityRef{Type: "contact", ID: contactID}
				}
			case action != "delete" && e.link != "":
				ev.Link = &HomeEntityRef{Type: e.link, ID: id}
			}
			if actorKind != "" {
				ev.Actor = &HomeActor{Kind: actorKind, Name: actorName, Via: via}
			}
			out = append(out, ev)
			return nil
		})
	}

	if can(authz.InboxRead) {
		b.each(`
			SELECT m.created_at, m.conversation_id, c.channel,
			       coalesce(nullif(m.sender_name, ''), nullif(c.customer_name, ''), nullif(c.customer_email, ''), ''), m.body
			FROM messages m JOIN conversations c ON c.workspace_id = m.workspace_id AND c.id = m.conversation_id
			WHERE m.workspace_id = $1 AND m.direction = 'in'
			ORDER BY m.created_at DESC LIMIT `+itoa(ActivityLimit), nil, func(r *sql.Rows) error {
			var at time.Time
			var conv, channel, sender, body string
			if err := r.Scan(&at, &conv, &channel, &sender, &body); err != nil {
				return err
			}
			out = append(out, HomeEvent{Kind: "message.received", OccurredAt: at, Channel: channel, Subject: snippet(sender, 60),
				Snippet: snippet(body, 140), Link: &HomeEntityRef{Type: "conversation", ID: conv}})
			return nil
		})
	}

	if can(authz.AutomationRead) {
		b.each(`
			SELECT coalesce(finished_at, started_at), rule_id, rule_name, status
			FROM automation_rule_runs
			WHERE workspace_id = $1 AND NOT dry_run AND status IN ('ok', 'partial', 'failed')
			ORDER BY coalesce(finished_at, started_at) DESC LIMIT `+itoa(ActivityLimit), nil, func(r *sql.Rows) error {
			var at time.Time
			var id, name, status string
			if err := r.Scan(&at, &id, &name, &status); err != nil {
				return err
			}
			out = append(out, HomeEvent{Kind: "rule.ran", OccurredAt: at, Subject: name, Status: status, Link: &HomeEntityRef{Type: "rule", ID: id}})
			return nil
		})
		b.each(`
			SELECT coalesce(r.finished_at, r.created_at), r.id, coalesce(nullif(w.name, ''), r.trigger), r.status
			FROM automation_runs r LEFT JOIN automation_workflows w ON w.workspace_id = r.workspace_id AND w.id = r.workflow_id
			WHERE r.workspace_id = $1 AND r.status IN ('ok', 'failed')
			ORDER BY coalesce(r.finished_at, r.created_at) DESC LIMIT `+itoa(ActivityLimit), nil, func(r *sql.Rows) error {
			var at time.Time
			var id, name, status string
			if err := r.Scan(&at, &id, &name, &status); err != nil {
				return err
			}
			out = append(out, HomeEvent{Kind: "run.finished", OccurredAt: at, Subject: name, Status: status, Link: &HomeEntityRef{Type: "run", ID: id}})
			return nil
		})
	}

	if can(authz.ResearchRead) {
		b.each(`
			SELECT coalesce(finished_at, updated_at), id, topic, status, found_count
			FROM research_runs
			WHERE workspace_id = $1 AND status IN ('ok', 'failed') AND finished_at IS NOT NULL
			ORDER BY finished_at DESC LIMIT `+itoa(ActivityLimit), nil, func(r *sql.Rows) error {
			var at time.Time
			var id, topic, status string
			var found int64
			if err := r.Scan(&at, &id, &topic, &status, &found); err != nil {
				return err
			}
			out = append(out, HomeEvent{Kind: "research.finished", OccurredAt: at, Subject: snippet(topic, 80), Status: status, N: i64(found),
				Link: &HomeEntityRef{Type: "research", ID: id}})
			return nil
		})
	}

	sortEvents(out)
	if len(out) > ActivityLimit {
		out = out[:ActivityLimit]
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

// pulse counts the last hour's events in five-minute buckets, oldest first: the audit rows the
// activity list also reads, plus inbound messages. Zero-filled — a quiet hour really is zeros.
func (b *builder) pulse(can func(string) bool) HomePulse {
	p := HomePulse{BucketMinutes: int(SparkBucket / time.Minute), Buckets: make([]int64, SparkBuckets)}
	since := b.now.Add(-SparkBucket * SparkBuckets)
	put := func(at time.Time) {
		age := b.now.Sub(at)
		if age < 0 {
			age = 0
		}
		i := SparkBuckets - 1 - int(age/SparkBucket)
		if i >= 0 && i < SparkBuckets {
			p.Buckets[i]++
			p.Total++
		}
	}
	scan := func(r *sql.Rows) error {
		var at time.Time
		if err := r.Scan(&at); err != nil {
			return err
		}
		put(at)
		return nil
	}
	if f := auditFilter(can, "a"); f != "" {
		b.each(`SELECT a.occurred_at FROM audit_log a WHERE a.workspace_id = $1 AND a.occurred_at > $2 AND a.occurred_at <= $3
			AND a.action IN ('create', 'update', 'delete') AND a.changes <> '{}'::jsonb AND `+f, []any{since, b.now}, scan)
	}
	if can(authz.InboxRead) {
		b.each(`SELECT created_at FROM messages WHERE workspace_id = $1 AND direction = 'in' AND created_at > $2 AND created_at <= $3`,
			[]any{since, b.now}, scan)
	}
	return p
}
