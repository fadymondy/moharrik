package home

import (
	"database/sql"
	"sort"
	"time"

	"github.com/fadymondy/moharrik/internal/authz"
)

// working builds the four lanes — Discover, Listen, Understand, Act — and the last hour's
// pulse. A lane is "active" only when something real is running or configured to run: a live
// research run, an enabled schedule or prospect profile, a connected channel, work done today.
// A headline is null when the role can read none of the lane's sources.
func (b *builder) working(can func(string) bool, midnight time.Time, t HomeToday, conns *HomeConnections) HomeWorking {
	w := HomeWorking{Lanes: []HomeLane{}}

	// ---- Discover: research runs, prospect profile, research schedules.
	disc := HomeLane{Key: "discover", Subs: []HomeLaneItem{}}
	if can(authz.ResearchRead) {
		live := b.count(`SELECT count(*) FROM research_runs WHERE workspace_id = $1 AND status IN ('queued', 'running')`)
		scheds := b.count(`SELECT count(*) FROM research_schedules WHERE workspace_id = $1 AND enabled`)
		var profileOn bool
		var next sql.NullTime
		b.each(`SELECT enabled, next_run_at FROM prospect_profiles WHERE workspace_id = $1`, nil, func(r *sql.Rows) error {
			return r.Scan(&profileOn, &next)
		})
		disc.Headline = i64(add(t.Signals.Prospects, t.Signals.Companies))
		disc.Subs = append(disc.Subs, HomeLaneItem{Key: "live_runs", N: live}, HomeLaneItem{Key: "schedules", N: scheds})
		if profileOn {
			it := HomeLaneItem{Key: "prospect_profile"}
			if next.Valid {
				it.At = tptr(next.Time)
			}
			disc.Subs = append(disc.Subs, it)
		}
		disc.Active = add(live, scheds) > 0 || profileOn
	}
	w.Lanes = append(w.Lanes, disc)

	// ---- Listen: connected channels (by kind) and conversations with activity today.
	listen := HomeLane{Key: "listen", Subs: []HomeLaneItem{}}
	if can(authz.InboxRead) {
		kinds := map[string]int64{}
		var connected int64
		if conns != nil {
			for _, c := range conns.Channels {
				if c.Status == "connected" {
					connected++
					kinds[c.Type]++
				}
			}
		}
		today := b.count(`SELECT count(*) FROM conversations WHERE workspace_id = $1 AND last_message_at >= $2`, midnight)
		listen.Headline = i64(connected)
		names := make([]string, 0, len(kinds))
		for k := range kinds {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			listen.Subs = append(listen.Subs, HomeLaneItem{Key: "channel_kind", Text: k, N: i64(kinds[k])})
		}
		listen.Subs = append(listen.Subs, HomeLaneItem{Key: "conversations_today", N: today})
		listen.Active = connected > 0
	}
	w.Lanes = append(w.Lanes, listen)

	// ---- Understand: contact reports, ICP scores, evidence captured today.
	und := HomeLane{Key: "understand", Subs: []HomeLaneItem{}}
	var undN []*int64
	if can(authz.ContactsRead) {
		reports := b.count(`SELECT count(*) FROM contact_context_reports WHERE workspace_id = $1 AND created_at >= $2`, midnight)
		scored := b.count(`SELECT count(*) FROM icp_scores WHERE workspace_id = $1 AND scored_at >= $2`, midnight)
		und.Subs = append(und.Subs, HomeLaneItem{Key: "reports", N: reports}, HomeLaneItem{Key: "icp_scored", N: scored})
		undN = append(undN, reports, scored)
	}
	if can(authz.ResearchRead) {
		ev := b.count(`SELECT count(*) FROM research_evidence WHERE workspace_id = $1 AND captured_at >= $2`, midnight)
		und.Subs = append(und.Subs, HomeLaneItem{Key: "evidence", N: ev})
		undN = append(undN, ev)
	}
	if len(und.Subs) > 0 {
		n := add(undN...)
		und.Headline = i64(n)
		und.Active = n > 0
	}
	w.Lanes = append(w.Lanes, und)

	// ---- Act: what was done today plus what is running right now.
	act := HomeLane{Key: "act", Subs: []HomeLaneItem{}}
	if can(authz.AutomationRead) || can(authz.CampaignsRead) {
		act.Headline = i64(t.Actions.Total)
		act.Subs = append(act.Subs,
			HomeLaneItem{Key: "rule_runs", N: t.Actions.RuleRuns},
			HomeLaneItem{Key: "browser_runs", N: t.Actions.BrowserRuns},
			HomeLaneItem{Key: "campaign_sends", N: t.Actions.CampaignSends})
		var running *int64
		if can(authz.AutomationRead) {
			running = b.count(`SELECT count(*) FROM automation_runs WHERE workspace_id = $1 AND status IN ('queued', 'running')`)
			act.Subs = append(act.Subs, HomeLaneItem{Key: "running_now", N: running})
		}
		act.Active = t.Actions.Total > 0 || add(running) > 0
	}
	w.Lanes = append(w.Lanes, act)

	w.Pulse = b.pulse(can)
	return w
}
