package home

import (
	"database/sql"
	"time"

	"github.com/fadymondy/moharrik/internal/authz"
)

// attention builds the "what needs your attention" list. Every item is derived from rows that
// exist — there is no engagement score — and carries the reasons it was derived from. Kinds a
// role cannot read are skipped; nil when no kind was readable at all.
//
// Order is urgency: failures, unanswered customers, deals going quiet, hot leads, new prospects.
func (b *builder) attention(can func(string) bool) *HomeAttention {
	var failed, unanswered, prospects *HomeAttentionItem
	var atRisk, hot []HomeAttentionItem
	var atRiskTotal, hotTotal int64
	readable := false

	if can(authz.AutomationRead) {
		readable = true
		failed = b.failedAutomation()
	}
	if can(authz.InboxRead) {
		readable = true
		unanswered = b.unanswered()
	}
	if can(authz.ContactsRead) {
		readable = true
		atRisk, atRiskTotal = b.atRisk()
	}
	if can(authz.LeadsRead) {
		readable = true
		hot, hotTotal = b.hotLeads()
	}
	if can(authz.ResearchRead) {
		readable = true
		prospects = b.newProspects()
	}
	if !readable {
		return nil
	}

	out := &HomeAttention{Items: []HomeAttentionItem{}}
	if failed != nil {
		out.Items = append(out.Items, *failed)
		out.Total++
	}
	if unanswered != nil {
		out.Items = append(out.Items, *unanswered)
		out.Total++
	}
	out.Items = append(out.Items, atRisk...)
	out.Total += atRiskTotal
	out.Items = append(out.Items, hot...)
	out.Total += hotTotal
	if prospects != nil {
		out.Items = append(out.Items, *prospects)
		out.Total++
	}
	return out
}

func moneyOf(minor int64, cur string) *HomeMoney { return &HomeMoney{Currency: cur, Minor: minor} }

// hotLeads: leads still new/contacted that either wrote to us on any channel in the last 72h
// (matched by linked contact, email or phone) or arrived in the last 24h.
func (b *builder) hotLeads() ([]HomeAttentionItem, int64) {
	var items []HomeAttentionItem
	var total int64
	b.each(`
		SELECT l.id, l.name, l.email, l.form_name, l.utm_source, l.utm_medium, l.utm_campaign, l.created_at,
		       m.at, m.channel, d.value_minor, d.currency, count(*) OVER ()
		FROM leads l
		LEFT JOIN LATERAL (
			SELECT msg.created_at AS at, c.channel
			FROM messages msg
			JOIN conversations c ON c.workspace_id = msg.workspace_id AND c.id = msg.conversation_id
			WHERE msg.workspace_id = l.workspace_id AND msg.direction = 'in' AND msg.created_at > $2
			  AND ((l.contact_id IS NOT NULL AND c.contact_id = l.contact_id)
			    OR (l.email <> '' AND lower(c.customer_email) = lower(l.email))
			    OR (l.phone <> '' AND c.customer_phone = l.phone))
			ORDER BY msg.created_at DESC LIMIT 1
		) m ON true
		LEFT JOIN deals d ON d.workspace_id = l.workspace_id AND d.id = l.deal_id
		WHERE l.workspace_id = $1 AND l.status IN ('new', 'contacted')
		  AND (m.at IS NOT NULL OR l.created_at > $3)
		ORDER BY coalesce(m.at, l.created_at) DESC, l.id
		LIMIT 3`, []any{b.now.Add(-HotLeadReplyWindow), b.now.Add(-NewLeadWindow)}, func(r *sql.Rows) error {
		var id, name, email, form, src, med, camp string
		var created time.Time
		var at sql.NullTime
		var channel sql.NullString
		var val sql.NullInt64
		var cur sql.NullString
		if err := r.Scan(&id, &name, &email, &form, &src, &med, &camp, &created, &at, &channel, &val, &cur, &total); err != nil {
			return err
		}
		it := HomeAttentionItem{Kind: "hot_lead", Tone: "success", Title: name, Entity: &HomeEntityRef{Type: "lead", ID: id},
			Why: []HomeReason{}, Actions: []HomeAction{{Key: "open", Route: "leads/" + id}}}
		if it.Title == "" {
			it.Title = email
		}
		when := created
		if at.Valid {
			it.Why = append(it.Why, HomeReason{Code: "replied", Channel: channel.String, At: tptr(at.Time)})
			when = at.Time
			it.Actions = append(it.Actions, HomeAction{Key: "open_inbox", Route: "inbox"})
		}
		if created.After(b.now.Add(-NewLeadWindow)) {
			it.Why = append(it.Why, HomeReason{Code: "new_lead", At: tptr(created)})
		}
		switch {
		case src != "":
			it.Why = append(it.Why, HomeReason{Code: "utm", Text: "utm_source=" + src})
		case med != "":
			it.Why = append(it.Why, HomeReason{Code: "utm", Text: "utm_medium=" + med})
		case camp != "":
			it.Why = append(it.Why, HomeReason{Code: "utm", Text: "utm_campaign=" + camp})
		case form != "":
			it.Why = append(it.Why, HomeReason{Code: "form", Text: form})
		}
		it.OccurredAt = tptr(when)
		if val.Valid && val.Int64 > 0 {
			it.Value = moneyOf(val.Int64, cur.String)
		}
		items = append(items, it)
		return nil
	})
	return items, total
}

// lastActivitySQL is the moment a deal last moved: its creation, its latest stage entry, its
// latest logged activity (on the deal or its contact) or its latest message on a conversation
// linked to the deal or its contact. Shared by at-risk and journeys. Expects the deal as d.
const lastActivitySQL = `greatest(
	d.created_at,
	coalesce((SELECT max(e.entered_at) FROM deal_stage_events e WHERE e.workspace_id = d.workspace_id AND e.deal_id = d.id), d.created_at),
	coalesce((SELECT max(a.created_at) FROM activities a WHERE a.workspace_id = d.workspace_id
	          AND (a.deal_id = d.id OR (d.contact_id IS NOT NULL AND a.contact_id = d.contact_id))), d.created_at),
	coalesce((SELECT max(m.created_at) FROM messages m JOIN conversations c ON c.workspace_id = m.workspace_id AND c.id = m.conversation_id
	          WHERE m.workspace_id = d.workspace_id AND (c.deal_id = d.id OR (d.contact_id IS NOT NULL AND c.contact_id = d.contact_id))), d.created_at)
)`

// atRisk: open deals with nothing for 7 days or more, biggest first.
func (b *builder) atRisk() ([]HomeAttentionItem, int64) {
	var items []HomeAttentionItem
	var total int64
	b.each(`
		SELECT d.id, d.title, d.value_minor, d.currency, s.name, x.at, count(*) OVER ()
		FROM deals d
		JOIN pipeline_stages s ON s.workspace_id = d.workspace_id AND s.id = d.stage_id AND s.kind = 'open'
		CROSS JOIN LATERAL (SELECT `+lastActivitySQL+` AS at) x
		WHERE d.workspace_id = $1 AND x.at < $2
		ORDER BY d.value_minor DESC, x.at, d.id
		LIMIT 2`, []any{b.now.Add(-QuietAfter)}, func(r *sql.Rows) error {
		var id, title, cur, stage string
		var val int64
		var at time.Time
		if err := r.Scan(&id, &title, &val, &cur, &stage, &at, &total); err != nil {
			return err
		}
		days := int64(b.now.Sub(at).Hours() / 24)
		it := HomeAttentionItem{Kind: "at_risk", Tone: "warning", Title: title, Entity: &HomeEntityRef{Type: "deal", ID: id},
			Why:        []HomeReason{{Code: "quiet_days", N: i64(days)}, {Code: "in_stage", Text: stage}},
			OccurredAt: tptr(at), Actions: []HomeAction{{Key: "open", Route: "deals/" + id}}}
		if val > 0 {
			it.Value = moneyOf(val, cur)
		}
		items = append(items, it)
		return nil
	})
	return items, total
}

// newProspects: one grouped item for prospects found in the last 7 days and still undecided.
func (b *builder) newProspects() *HomeAttentionItem {
	var n, high int64
	var newest sql.NullTime
	if b.err != nil {
		return nil
	}
	if b.fail(b.q.QueryRowContext(b.ctx, `
		SELECT count(*), count(*) FILTER (WHERE score >= $3), max(created_at)
		FROM prospects WHERE workspace_id = $1 AND status = 'new' AND created_at > $2`,
		b.ws, b.now.Add(-NewProspectWindow), HighFitScore).Scan(&n, &high, &newest)) || n == 0 {
		return nil
	}
	it := &HomeAttentionItem{Kind: "new_prospects", Tone: "info", Count: i64(n),
		Why:     []HomeReason{},
		Actions: []HomeAction{{Key: "open_prospects", Route: "prospects"}}}
	if high > 0 {
		it.Why = append(it.Why, HomeReason{Code: "high_fit", N: i64(high)})
	}
	if newest.Valid {
		it.OccurredAt = tptr(newest.Time)
	}
	return it
}

// failedAutomation: rule runs and browser/workflow runs that failed in the last 24h.
func (b *builder) failedAutomation() *HomeAttentionItem {
	if b.err != nil {
		return nil
	}
	since := b.now.Add(-FailureWindow)
	var rules, runs int64
	var lastRule, lastRun sql.NullTime
	if b.fail(b.q.QueryRowContext(b.ctx, `
		SELECT count(*), max(coalesce(finished_at, started_at)) FROM automation_rule_runs
		WHERE workspace_id = $1 AND NOT dry_run AND status = 'failed' AND coalesce(finished_at, started_at) > $2`,
		b.ws, since).Scan(&rules, &lastRule)) {
		return nil
	}
	if b.fail(b.q.QueryRowContext(b.ctx, `
		SELECT count(*), max(coalesce(finished_at, created_at)) FROM automation_runs
		WHERE workspace_id = $1 AND status = 'failed' AND coalesce(finished_at, created_at) > $2`,
		b.ws, since).Scan(&runs, &lastRun)) {
		return nil
	}
	if rules+runs == 0 {
		return nil
	}
	it := &HomeAttentionItem{Kind: "failed_automation", Tone: "danger", Count: i64(rules + runs), Why: []HomeReason{},
		Actions: []HomeAction{{Key: "open_runs", Route: "automation/runs"}}}
	if rules > 0 {
		it.Why = append(it.Why, HomeReason{Code: "rule_failed", N: i64(rules)})
		it.Actions = append(it.Actions, HomeAction{Key: "review", Route: "automation/rules"})
	}
	if runs > 0 {
		it.Why = append(it.Why, HomeReason{Code: "browser_failed", N: i64(runs)})
	}
	last := lastRule.Time
	if lastRun.Valid && lastRun.Time.After(last) {
		last = lastRun.Time
	}
	it.OccurredAt = tptr(last)
	return it
}

// unanswered: open conversations whose newest message is from the customer and at least four
// hours old. Conversations quiet for more than 14 days are left out — they are history, not a
// waiting customer — and blocked threads and groups never count.
func (b *builder) unanswered() *HomeAttentionItem {
	if b.err != nil {
		return nil
	}
	var n int64
	var oldest sql.NullTime
	if b.fail(b.q.QueryRowContext(b.ctx, `
		SELECT count(*), min(lm.created_at)
		FROM conversations c
		JOIN LATERAL (SELECT m.direction, m.created_at FROM messages m
		              WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id
		              ORDER BY m.created_at DESC, m.id DESC LIMIT 1) lm ON true
		WHERE c.workspace_id = $1 AND c.status = 'open' AND c.blocked_at IS NULL AND NOT coalesce(c.is_group, false)
		  AND lm.direction = 'in' AND lm.created_at < $2 AND lm.created_at > $3`,
		b.ws, b.now.Add(-UnansweredAfter), b.now.Add(-UnansweredWithin)).Scan(&n, &oldest)) || n == 0 {
		return nil
	}
	it := &HomeAttentionItem{Kind: "unanswered", Tone: "warning", Count: i64(n), Why: []HomeReason{},
		Actions: []HomeAction{{Key: "open_inbox", Route: "inbox"}}}
	if oldest.Valid {
		it.Why = append(it.Why, HomeReason{Code: "waiting_for_reply", At: tptr(oldest.Time)})
		it.OccurredAt = tptr(oldest.Time)
	}
	return it
}
