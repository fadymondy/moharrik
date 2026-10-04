package home

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
)

/*
Home against a real database with RLS on (MH-1062). Every expected figure is worked out by hand
from the rows inserted below, relative to a fixed clock, so the point under test is that the page
shows the data's numbers, with the windows it documents.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/home/ -v
*/

func testApp(t *testing.T) *app.App {
	t.Helper()
	owner, appURL := testdb.Setup(t, "home", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "home-test-secret-home-test-secret-home-0001")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("SCHEDULER", "off")
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	k := togo.New()
	a := app.New(context.Background(), k)
	t.Cleanup(func() { k.Close() })
	if a.SQLDB == nil || a.SysDB == nil {
		t.Fatal("both pools are required")
	}
	return a
}

type seeder struct {
	t  *testing.T
	db *sql.DB
	ws string
	u  string
}

func (s seeder) id(q string, args ...any) string {
	s.t.Helper()
	var id string
	if err := s.db.QueryRow(q, append([]any{s.ws}, args...)...).Scan(&id); err != nil {
		s.t.Fatalf("%v\n%s", err, q)
	}
	return id
}

func (s seeder) exec(q string, args ...any) {
	s.t.Helper()
	if _, err := s.db.Exec(q, append([]any{s.ws}, args...)...); err != nil {
		s.t.Fatalf("%v\n%s", err, q)
	}
}

func newWorkspace(t *testing.T, a *app.App) seeder {
	t.Helper()
	suffix := strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "-")
	user := "u-home-" + suffix
	if _, err := a.SysDB.Exec(`INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', 'now')`, user, "home-"+suffix+"@example.test"); err != nil {
		t.Fatal(err)
	}
	var ws string
	if err := a.SysDB.QueryRow(`INSERT INTO workspaces (name, slug, created_by, currency, timezone) VALUES ('Home', $1, $2, 'USD', 'Africa/Cairo') RETURNING id`,
		"home-"+suffix, user).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')`, ws, user); err != nil {
		t.Fatal(err)
	}
	return seeder{t: t, db: a.SysDB, ws: ws, u: user}
}

func build(t *testing.T, a *app.App, ws, role string, env Env, now time.Time) *Home {
	t.Helper()
	h, err := Build(tenant.WithWorkspace(context.Background(), ws), a.SQLDB, ws, role, env, now)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func eq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func n(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

func TestHome(t *testing.T) {
	a := testApp(t)
	s := newWorkspace(t, a)
	cairo, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		t.Skip("no tz database")
	}
	// 15:00 Cairo on 15 Sep 2026. "Today" starts 00:00 Cairo — which is 21:00 UTC the day before.
	now := time.Date(2026, 9, 15, 15, 0, 0, 0, cairo)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	h := time.Hour
	day := 24 * h

	// ---- pipeline & deals --------------------------------------------------
	qualify := s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Qualify', 0, 'open') RETURNING id`)
	proposal := s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Proposal', 1, 'open') RETURNING id`)
	won := s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Won', 2, 'won') RETURNING id`)
	lost := s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Lost', 3, 'lost') RETURNING id`)
	deal := func(title, stage string, value int64, cur string, created time.Time) string {
		return s.id(`INSERT INTO deals (workspace_id, title, value_minor, currency, stage_id, created_by, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			title, value, cur, stage, s.u, created)
	}
	// Triggers add a stage event at insert time (now); pin every event to the deal's own time.
	quiet := deal("Quiet deal", qualify, 100000, "USD", ago(20*day))
	fresh := deal("Fresh deal", proposal, 50000, "EUR", ago(2*h))
	deal("Won deal", won, 999, "USD", ago(3*day))
	deal("Lost deal", lost, 888, "USD", ago(3*day))
	s.exec(`DELETE FROM deal_stage_events WHERE workspace_id = $1`)
	s.exec(`INSERT INTO deal_stage_events (workspace_id, deal_id, stage_id, entered_at) VALUES ($1, $2, $3, $4)`, quiet, qualify, ago(20*day))
	s.exec(`INSERT INTO deal_stage_events (workspace_id, deal_id, stage_id, entered_at) VALUES ($1, $2, $3, $4)`, fresh, proposal, ago(2*h))

	// ---- leads & conversations ---------------------------------------------
	lead := func(name, email, status string, created time.Time, src string) string {
		return s.id(`INSERT INTO leads (workspace_id, name, email, message, status, utm_source, created_at) VALUES ($1, $2, $3, 'hi', $4, $5, $6) RETURNING id`,
			name, email, status, src, created)
	}
	l1 := lead("Fresh Lead", "fresh@example.test", "new", ago(2*h), "google")        // new in 24h: hot, today
	l2 := lead("Replied Lead", "replied@example.test", "contacted", ago(10*day), "") // old, but wrote 5h ago: hot
	lead("Old Lead", "old@example.test", "new", ago(10*day), "")                     // old, silent: not hot
	lead("Qualified Lead", "q@example.test", "qualified", ago(1*h), "")              // wrong status: not hot
	lead("Spam", "spam@example.test", "spam", ago(1*h), "")                          // spam: not hot, not a signal
	conv := func(status, email, channel string) string {
		return s.id(`INSERT INTO conversations (workspace_id, status, customer_email, customer_name, channel, last_message_at) VALUES ($1, $2, $3, 'C', $4, now()) RETURNING id`,
			status, email, channel)
	}
	msg := func(c, dir string, at time.Time) {
		s.exec(`INSERT INTO messages (workspace_id, conversation_id, direction, body, created_at) VALUES ($1, $2, $3, $4, $5)`,
			c, dir, "  hello \n\n  there  "+strings.Repeat("x", 300), at)
	}
	cReplied := conv("open", "replied@example.test", "whatsapp")
	msg(cReplied, "in", ago(5*h)) // last message inbound 5h ago: unanswered AND the lead's reply
	cAnswered := conv("open", "a@example.test", "email")
	msg(cAnswered, "in", ago(9*h))
	msg(cAnswered, "out", ago(8*h)) // answered: not unanswered
	cFresh := conv("open", "b@example.test", "chat")
	msg(cFresh, "in", ago(30*time.Minute)) // inbound but under 4h: not yet
	cClosed := conv("closed", "c@example.test", "chat")
	msg(cClosed, "in", ago(10*h)) // closed: not unanswered
	cAncient := conv("open", "d@example.test", "chat")
	msg(cAncient, "in", ago(40*day)) // older than the 14-day window: history

	// ---- research: prospects ------------------------------------------------
	company := func(key string, firstSeen time.Time) string {
		return s.id(`INSERT INTO research_companies (workspace_id, company_key, name, first_seen_at) VALUES ($1, $2, $2, $3) RETURNING id`, key, firstSeen)
	}
	c1, c2, c3 := company("acme", ago(2*h)), company("globex", ago(3*day)), company("initech", ago(20*day))
	prospect := func(sug string, score float32, created time.Time) {
		s.exec(`INSERT INTO prospects (workspace_id, suggestion_id, score, created_at) VALUES ($1, $2, $3, $4)`, sug, score, created)
	}
	prospect(c1, 90, ago(2*h))    // new, high fit, today
	prospect(c2, 40, ago(3*day))  // new, low fit
	prospect(c3, 95, ago(20*day)) // older than 7 days: not counted

	ruleID := s.id(`INSERT INTO automation_rules (workspace_id, name, trigger, created_by, updated_by) VALUES ($1, 'Welcome', 'lead.created', $2, $2) RETURNING id`, s.u)
	agentID := s.id(`INSERT INTO browser_agents (workspace_id, name, token_hash, created_by) VALUES ($1, 'Agent', $2, $3) RETURNING id`, "h-"+s.ws, s.u)
	// ---- automation: rule runs, browser runs, campaigns ----------------------
	rule := func(status string, dry bool, started time.Time) {
		s.exec(`INSERT INTO automation_rule_runs (workspace_id, rule_id, rule_name, trigger, status, dry_run, started_at, finished_at)
			VALUES ($1, $5, 'Welcome', 'lead.created', $2, $3, $4, $4)`, status, dry, started, ruleID)
	}
	rule("ok", false, ago(1*h))       // action today
	rule("partial", false, ago(2*h))  // action today
	rule("ok", true, ago(1*h))        // dry run: ignored everywhere
	rule("failed", false, ago(3*h))   // failed in 24h
	rule("failed", false, ago(3*day)) // too old to flag
	rule("ok", false, ago(20*h))      // yesterday in Cairo (before 00:00): not today
	run := func(status string, at time.Time) {
		s.exec(`INSERT INTO automation_runs (workspace_id, agent_id, status, created_at, finished_at) VALUES ($1, $4, $2, $3, $3)`, status, at, agentID)
	}
	run("ok", ago(1*h))
	run("failed", ago(4*h))
	s.exec(`INSERT INTO automation_runs (workspace_id, agent_id, status, created_at) VALUES ($1, $3, 'running', $2)`, ago(10*time.Minute), agentID)
	ml := s.id(`INSERT INTO mail_lists (workspace_id, name) VALUES ($1, 'L') RETURNING id`)
	s.exec(`INSERT INTO campaigns (workspace_id, list_id, subject, body, created_by, status, sent_count, sent_at) VALUES ($1, $2, 'HomeToday', 'b', $3, 'sent', 12, $4)`, ml, s.u, ago(2*h))
	s.exec(`INSERT INTO campaigns (workspace_id, list_id, subject, body, created_by, status, sent_count, sent_at) VALUES ($1, $2, 'Old', 'b', $3, 'sent', 99, $4)`, ml, s.u, ago(3*day))

	// ---- audit rows (the trigger records nothing for system writes, so write them) ---
	audit := func(action, table, id, changes string, at time.Time) {
		s.exec(`INSERT INTO audit_log (workspace_id, occurred_at, actor_kind, actor_name, via, action, entity_type, entity_id, changes)
			VALUES ($1, $2, 'member', 'sam@example.test', 'web', $3, $4, $5, $6::jsonb)`, at, action, table, id, changes)
	}
	audit("update", "deals", fresh, `{"stage_id":{"before":"x","after":"y"}}`, ago(10*time.Minute)) // stage change, in the last hour
	audit("update", "conversation_prefs", "p", `{"favourite":{"after":true}}`, ago(5*time.Minute))  // noise: never an event
	audit("update", "contacts", "ct", `{"phone":{"after":"1"}}`, ago(2*time.Minute))                // a deleted/unknown contact: still an event

	// ---- context -------------------------------------------------------------
	env := Env{}
	got := build(t, a, s.ws, "owner", env, now)

	_ = l1
	_ = l2

	// Today (local midnight, Cairo): Fresh + Qualified leads (spam out, older leads out),
	// one prospect, one company.
	eq(t, "signals.leads", n(got.Today.Signals.Leads), 2)
	eq(t, "signals.prospects", n(got.Today.Signals.Prospects), 1)
	eq(t, "signals.companies", n(got.Today.Signals.Companies), 1)
	eq(t, "signals.trends", n(got.Today.Signals.Trends), 0)
	eq(t, "signals.total", got.Today.Signals.Total, 4)
	eq(t, "open deals (won/lost excluded)", n(got.Today.Opportunities.Open), 2)
	vals := map[string]int64{}
	for _, m := range got.Today.Opportunities.Value {
		vals[m.Currency] = m.Minor
	}
	eq(t, "USD value (never converted)", vals["USD"], 100000)
	eq(t, "EUR value", vals["EUR"], 50000)
	eq(t, "rule runs today (dry, failed and yesterday out)", n(got.Today.Actions.RuleRuns), 2)
	eq(t, "browser runs today", n(got.Today.Actions.BrowserRuns), 1)
	eq(t, "campaign sends today", n(got.Today.Actions.CampaignSends), 12)
	eq(t, "actions total", got.Today.Actions.Total, 15)

	// Attention: failure(1 item) + unanswered + at-risk + 2 hot leads + prospects.
	if got.Attention == nil {
		t.Fatal("owner must see attention")
	}
	kinds := map[string]int{}
	for _, it := range got.Attention.Items {
		kinds[it.Kind]++
		if len(it.Actions) == 0 {
			t.Errorf("%s has no action", it.Kind)
		}
	}
	eq(t, "attention total", got.Attention.Total, 6)
	eq(t, "today.attention", got.Today.Attention, 6)
	eq(t, "failed_automation", kinds["failed_automation"], 1)
	eq(t, "unanswered", kinds["unanswered"], 1)
	eq(t, "at_risk", kinds["at_risk"], 1)
	eq(t, "hot_lead", kinds["hot_lead"], 2)
	eq(t, "new_prospects", kinds["new_prospects"], 1)
	for _, it := range got.Attention.Items {
		switch it.Kind {
		case "failed_automation":
			eq(t, "failed count (1 rule + 1 run)", n(it.Count), 2)
		case "unanswered":
			eq(t, "unanswered count", n(it.Count), 1)
		case "new_prospects":
			eq(t, "prospects count", n(it.Count), 2)
		case "at_risk":
			eq(t, "at-risk title", it.Title, "Quiet deal")
			eq(t, "at-risk quiet days", n(it.Why[0].N), 20)
		}
	}

	// Journeys: both open deals, the quiet one flagged, won/lost never listed.
	if got.Journeys == nil {
		t.Fatal("owner must see journeys")
	}
	eq(t, "journey deals", len(got.Journeys.Deals), 2)
	for _, j := range got.Journeys.Deals {
		eq(t, j.Title+" quiet", j.Quiet, j.Title == "Quiet deal")
	}

	// Pulse is zero-filled and counts the audit rows inside the hour (the noise table is not one).
	eq(t, "pulse buckets", len(got.Working.Pulse.Buckets), SparkBuckets)
	if got.Working.Pulse.Total < 2 {
		t.Errorf("pulse total = %d, want >= 2", got.Working.Pulse.Total)
	}
	for _, e := range got.Activity {
		if e.Kind == "message.received" && len(e.Snippet) > 150 {
			t.Errorf("snippet not cut: %d", len(e.Snippet))
		}
		if strings.Contains(e.Snippet, "\n") {
			t.Errorf("snippet keeps newlines")
		}
	}
	eq(t, "activity is capped", len(got.Activity) <= ActivityLimit, true)

	// A client sees none of the CRM sections: null, not zero.
	c := build(t, a, s.ws, "client", env, now)
	if c.Attention != nil || c.Journeys != nil {
		t.Errorf("client got attention/journeys: %+v %+v", c.Attention, c.Journeys)
	}
	if c.Today.Opportunities.Open != nil {
		t.Errorf("client got open deals")
	}
}
