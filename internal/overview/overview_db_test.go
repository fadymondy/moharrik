package overview

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
)

/*
The overview against a real database with RLS on (MH-462). Every expected
figure below is worked out by hand from the rows the test inserts — the point
is that the page's numbers are the data's numbers, not the code's opinion of
them.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/overview/ -v
*/

func testApp(t *testing.T) *app.App {
	t.Helper()
	owner, appURL := testdb.Setup(t, "overview", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "overview-test-secret-overview-test-secret-0001")
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

// seeder inserts through the OWNER pool, so the rows exist exactly as written
// (timestamps included) — the thing under test is the read, not the writes.
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

var seq int

func newWorkspace(t *testing.T, a *app.App, currency, tz string) seeder {
	t.Helper()
	seq++
	suffix := time.Now().Format("150405.000000") + "-" + string(rune('a'+seq%26))
	user := "u-ov-" + suffix
	if _, err := a.SysDB.Exec(`INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', 'now')`,
		user, "ov-"+suffix+"@example.test"); err != nil {
		t.Fatal(err)
	}
	var ws string
	if err := a.SysDB.QueryRow(`INSERT INTO workspaces (name, slug, created_by, currency, timezone) VALUES ('Overview', $1, $2, $3, $4) RETURNING id`,
		"ov-"+strings.ReplaceAll(suffix, ".", "-"), user, currency, tz).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')`, ws, user); err != nil {
		t.Fatal(err)
	}
	return seeder{t: t, db: a.SysDB, ws: ws, u: user}
}

func build(t *testing.T, a *app.App, ws, role string, env Env, now time.Time) *Overview {
	t.Helper()
	o, err := Build(tenant.WithWorkspace(context.Background(), ws), a.SQLDB, ws, role, env, now)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// cairo is the workspace's zone in the figures test: 'today' and 'this month'
// must be the workspace's, not the server's or UTC's.
func cairo(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		t.Skip("no tz database on this machine")
	}
	return loc
}

func at(loc *time.Location, month time.Month, day, hour, min int) time.Time {
	return time.Date(2026, month, day, hour, min, 0, 0, loc)
}

// seedA fills a workspace with one of everything, arranged so every boundary
// the overview draws (today, this month, currency, unread, topic match) has a
// row on each side of it. It returns the ids the assertions name.
func seedA(t *testing.T, s seeder, loc *time.Location) map[string]string {
	ids := map[string]string{}

	// ---- pipeline ---------------------------------------------------------
	s1 := s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Qualify', 0, 'open') RETURNING id`)
	s2 := s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Proposal', 1, 'open') RETURNING id`)
	won := s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Won', 2, 'won') RETURNING id`)
	lost := s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Lost', 3, 'lost') RETURNING id`)
	ids["s1"], ids["s2"] = s1, s2
	co := s.id(`INSERT INTO companies (workspace_id, name) VALUES ($1, 'Acme') RETURNING id`)

	deal := func(title, stage string, value int64, cur string, expected any, closed any, company any) string {
		return s.id(`INSERT INTO deals (workspace_id, title, value_minor, currency, stage_id, expected_close, closed_at, company_id, created_by)
			VALUES ($1, $2, $3, $4, $5, $6::date, $7, $8, $9) RETURNING id`, title, value, cur, stage, expected, closed, company, s.u)
	}
	ids["d1"] = deal("d1 today", s1, 1000, "EUR", "2026-09-15", nil, nil)
	ids["d2"] = deal("d2 dollars", s1, 500, "USD", "2026-09-20", nil, nil)
	ids["d3"] = deal("d3 overdue", s1, 250, "EUR", "2026-09-14", nil, nil)
	ids["d4"] = deal("d4 tomorrow", s2, 4000, "EUR", "2026-09-16", nil, co)
	ids["d5"] = deal("d5 undated", s2, 0, "EUR", nil, nil, nil)
	// Won: 00:30 on the 1st in Cairo is still August in UTC — it counts.
	deal("w1", won, 7000, "EUR", nil, at(loc, time.September, 2, 10, 0), nil)
	deal("w2", won, 9000, "EUR", nil, at(loc, time.September, 1, 0, 30), nil)
	deal("w3 last month", won, 3000, "EUR", nil, at(loc, time.August, 31, 23, 30), nil)
	deal("w4 dollars", won, 2000, "USD", nil, at(loc, time.September, 10, 9, 0), nil)
	deal("l1", lost, 5000, "EUR", nil, at(loc, time.September, 5, 9, 0), nil)

	// ---- leads ------------------------------------------------------------
	lead := func(name, status string, created time.Time, utmSource, utmMedium string) string {
		return s.id(`INSERT INTO leads (workspace_id, name, email, message, status, utm_source, utm_medium, company, created_at)
			VALUES ($1, $2, $3, 'hello', $4, $5, $6, 'Acme', $7) RETURNING id`,
			name, strings.ToLower(name)+"@example.test", status, utmSource, utmMedium, created)
	}
	ids["L1"] = lead("L1", "new", at(loc, time.September, 15, 9, 0), "google", "cpc")
	// 00:10 in Cairo is the day before in UTC — still "today" for the workspace.
	ids["L2"] = lead("L2", "new", at(loc, time.September, 15, 0, 10), "", "")
	ids["L3"] = lead("L3", "contacted", at(loc, time.September, 14, 23, 50), "", "")
	ids["L4"] = lead("L4", "qualified", at(loc, time.September, 10, 9, 0), "", "")
	ids["L5"] = lead("L5", "converted", at(loc, time.September, 9, 9, 0), "", "")
	ids["L6"] = lead("L6", "spam", at(loc, time.September, 8, 9, 0), "", "")
	ids["L7"] = lead("L7", "new", at(loc, time.September, 1, 9, 0), "", "")

	// ---- inbox ------------------------------------------------------------
	conv := func(name string, unread bool, last time.Time) string {
		return s.id(`INSERT INTO conversations (workspace_id, customer_name, unread, last_message_at) VALUES ($1, $2, $3, $4) RETURNING id`, name, unread, last)
	}
	ids["C1"] = conv("C1", true, at(loc, time.September, 10, 9, 0))
	ids["C2"] = conv("C2", false, at(loc, time.September, 15, 9, 0))
	ids["C3"] = conv("C3", true, at(loc, time.September, 12, 9, 0))
	ids["C4"] = conv("C4", false, at(loc, time.September, 1, 9, 0))
	s.exec(`INSERT INTO messages (workspace_id, conversation_id, direction, body, created_at) VALUES ($1, $2, 'in', 'first', $3)`, ids["C3"], at(loc, time.September, 12, 8, 0))
	s.exec(`INSERT INTO messages (workspace_id, conversation_id, direction, body, created_at) VALUES ($1, $2, 'in', 'second
line', $3)`, ids["C3"], at(loc, time.September, 12, 9, 0))

	// ---- notifications ----------------------------------------------------
	for i, read := range []bool{false, true, false, true, false, false} {
		var readAt any
		if read {
			readAt = at(loc, time.September, 15, 1, 0)
		}
		n := string(rune('1' + i))
		ids["N"+n] = s.id(`INSERT INTO notification_items (workspace_id, title, severity, source_name, occurred_at, read_at)
			VALUES ($1, $2, 'warning', 'GitHub', $3, $4) RETURNING id`, "N"+n, at(loc, time.September, 1+i, 9, 0), readAt)
	}

	// ---- research ---------------------------------------------------------
	s.exec(`INSERT INTO research_runs (workspace_id, topic, status, created_at) VALUES ($1, 'old queued', 'queued', $2)`, at(loc, time.September, 14, 9, 0))
	ids["R1"] = s.id(`INSERT INTO research_runs (workspace_id, topic, status, created_at) VALUES ($1, 'coffee prices', 'running', $2) RETURNING id`, at(loc, time.September, 15, 9, 0))
	s.exec(`INSERT INTO research_runs (workspace_id, topic, status, created_at) VALUES ($1, 'done', 'ok', $2)`, at(loc, time.September, 15, 10, 0))

	// ---- marketing --------------------------------------------------------
	alpha := s.id(`INSERT INTO mail_lists (workspace_id, name) VALUES ($1, 'Alpha') RETURNING id`)
	s.exec(`INSERT INTO mail_lists (workspace_id, name) VALUES ($1, 'Beta')`)
	for i, st := range []string{"subscribed", "subscribed", "pending", "unsubscribed"} {
		c := s.id(`INSERT INTO contacts (workspace_id, email) VALUES ($1, $2) RETURNING id`, "c"+string(rune('0'+i))+"-"+s.ws+"@example.test")
		s.exec(`INSERT INTO list_subscriptions (workspace_id, list_id, contact_id, status, token) VALUES ($1, $2, $3, $4, $5)`, alpha, c, st, "tok-"+c)
	}
	for i := 1; i <= 4; i++ {
		ids["D"+string(rune('0'+i))] = s.id(`INSERT INTO campaigns (workspace_id, list_id, subject, body, created_by, created_at, channel)
			VALUES ($1, $2, $3, 'b', $4, $5, $6) RETURNING id`, alpha, "draft "+string(rune('0'+i)), s.u, at(loc, time.September, i, 9, 0), map[bool]string{true: "whatsapp", false: "email"}[i == 4])
	}
	s.exec(`INSERT INTO campaigns (workspace_id, list_id, subject, body, created_by, status, created_at) VALUES ($1, $2, 'sent one', 'b', $3, 'sent', $4)`,
		alpha, s.u, at(loc, time.September, 14, 9, 0))

	// ---- trends -----------------------------------------------------------
	coffee := s.id(`INSERT INTO topics (workspace_id, name, keywords, created_by) VALUES ($1, 'Coffee', '{coffee}', $2) RETURNING id`, s.u)
	tea := s.id(`INSERT INTO topics (workspace_id, name, keywords, created_by) VALUES ($1, 'Tea', '{tea}', $2) RETURNING id`, s.u)
	news := func(title string, topics []string, pub time.Time) string {
		return s.id(`INSERT INTO news_items (workspace_id, external_id, title, source_name, topic_ids, published_at)
			VALUES ($1, $2, $3, 'Feed', $4::text[], $5) RETURNING id`, "x-"+title, title, "{"+strings.Join(topics, ",")+"}", pub)
	}
	ids["n1"] = news("n1", []string{coffee}, at(loc, time.September, 14, 9, 0))
	ids["n2"] = news("n2", []string{coffee, tea}, at(loc, time.September, 15, 9, 0))
	ids["n3"] = news("n3 unmatched", nil, at(loc, time.September, 15, 11, 0))
	ids["n4"] = news("n4", []string{tea}, at(loc, time.September, 1, 9, 0))

	// ---- setup: an inquiry form and SMTP yes; the rest not (or not yet) -----
	s.exec(`INSERT INTO subscribe_forms (workspace_id, kind, name, public_key) VALUES ($1, 'inquiry', 'Contact', $2)`, "if-"+s.ws)
	s.exec(`INSERT INTO smtp_services (workspace_id, name, host, from_email, is_default, is_campaign_default) VALUES ($1, 'Default', 'smtp.example.test', 'hi@example.test', true, true)`)
	// A WhatsApp link still waiting for its QR code to be scanned is not linked.
	s.exec(`INSERT INTO whatsapp_links (workspace_id, instance, hook_id, state) VALUES ($1, $2, $3, 'connecting')`, "inst-"+s.ws, "hook-"+s.ws)
	// A revoked browser is not a paired browser.
	s.exec(`INSERT INTO browser_agents (workspace_id, name, token_hash, created_by, revoked_at) VALUES ($1, 'old', $2, $3, now())`, "th-"+s.ws, s.u)
	return ids
}

func TestOverviewFiguresMatchTheData(t *testing.T) {
	a := testApp(t)
	loc := cairo(t)
	s := newWorkspace(t, a, "EUR", "Africa/Cairo")
	ids := seedA(t, s, loc)
	now := at(loc, time.September, 15, 12, 0)

	o := build(t, a, s.ws, "owner", Env{WhatsAppAvailable: true, GoogleAvailable: false}, now)
	if o.Currency != "EUR" || o.Timezone != "Africa/Cairo" {
		t.Fatalf("currency/timezone: %q %q", o.Currency, o.Timezone)
	}

	// ---- today ------------------------------------------------------------
	wantLeads := LeadCounts{"new": 3, "contacted": 1, "qualified": 1, "converted": 1, "spam": 1}
	if o.Today.Leads == nil || !equalCounts(*o.Today.Leads, wantLeads) {
		t.Errorf("leads by status: %v, want %v", o.Today.Leads, wantLeads)
	}
	// L1 and L2 (00:10 Cairo, which is still yesterday in UTC); not L3 (23:50 the day before).
	if got := deref(o.Today.LeadsReceivedToday); got != 2 {
		t.Errorf("leads received today: %d, want 2", got)
	}
	if got := deref(o.Today.UnreadConversations); got != 2 {
		t.Errorf("unread conversations: %d, want 2", got)
	}
	if got := deref(o.Today.UnreadNotifications); got != 4 {
		t.Errorf("unread notifications: %d, want 4", got)
	}
	if r := o.Today.Research; r == nil || r.Live != 2 || r.Latest == nil || r.Latest.ID != ids["R1"] || r.Latest.Status != "running" {
		t.Errorf("research: %+v", r)
	}

	// ---- pipeline ---------------------------------------------------------
	p := o.Pipeline
	if p == nil {
		t.Fatal("no pipeline for an owner")
	}
	wantStages := []StageValue{{ids["s1"], "Qualify", 3, 1250}, {ids["s2"], "Proposal", 2, 4000}}
	if len(p.Stages) != 2 || p.Stages[0] != wantStages[0] || p.Stages[1] != wantStages[1] {
		t.Errorf("stages: %+v, want %+v (open stages only; USD left out of the value)", p.Stages, wantStages)
	}
	if p.OpenDeals != 5 || p.OpenValueMinor != 5250 || p.OtherCurrencyDeals != 1 {
		t.Errorf("open totals: %d deals, %d value, %d other currency; want 5, 5250, 1", p.OpenDeals, p.OpenValueMinor, p.OtherCurrencyDeals)
	}
	// w1 + w2 (00:30 on the 1st, Cairo) + w4 (USD, counted but not summed); not w3 (August), not l1 (lost).
	w := p.WonThisMonth
	if w.Count != 3 || w.ValueMinor != 16000 || w.OtherCurrencyDeals != 1 || !w.Since.Equal(at(loc, time.September, 1, 0, 0)) {
		t.Errorf("won this month: %+v, want 3 deals, 16000, 1 other currency since %v", w, at(loc, time.September, 1, 0, 0))
	}
	// Today's (d1), then d4, then d2; not d3 (overdue), not d5 (undated).
	if got := dealIDs(p.NextCloses); got != strings.Join([]string{ids["d1"], ids["d4"], ids["d2"]}, ",") {
		t.Errorf("next closes: %v", p.NextCloses)
	}
	if d := p.NextCloses[1]; d.CompanyName == nil || *d.CompanyName != "Acme" || d.ExpectedClose != "2026-09-16" || d.StageName != "Proposal" {
		t.Errorf("next close d4: %+v", d)
	}

	// ---- latest lists -----------------------------------------------------
	if got := leadIDs(o.Leads); got != strings.Join([]string{ids["L1"], ids["L2"], ids["L3"], ids["L4"], ids["L5"]}, ",") {
		t.Errorf("latest leads: %v", got)
	}
	if l := o.Leads[0]; l.Attribution.UtmSource != "google" || l.Attribution.UtmMedium != "cpc" || l.Company != "Acme" || l.Status != "new" {
		t.Errorf("lead L1: %+v", l)
	}
	// Unread first (C3 newer than C1), then read by recency.
	if got := convIDs(o.Conversations); got != strings.Join([]string{ids["C3"], ids["C1"], ids["C2"], ids["C4"]}, ",") {
		t.Errorf("conversations: %v", got)
	}
	if c := o.Conversations[0]; c.Preview != "second line" || !c.Unread {
		t.Errorf("C3's snippet is its LAST message, on one line: %+v", c)
	}
	if got := notifIDs(o.Notifications); got != strings.Join([]string{ids["N6"], ids["N5"], ids["N4"], ids["N3"], ids["N2"]}, ",") {
		t.Errorf("notifications: %v", got)
	}
	if n := o.Notifications[2]; !n.Read || n.Severity != "warning" || n.SourceName != "GitHub" {
		t.Errorf("N4: %+v", n)
	}

	// ---- marketing --------------------------------------------------------
	m := o.Marketing
	if m == nil || len(m.Lists) != 2 || m.Lists[0] != (ListCount{m.Lists[0].ID, "Alpha", 2, 1}) || m.Lists[1].Name != "Beta" || m.Lists[1].Subscribed != 0 || m.Lists[1].Pending != 0 {
		t.Errorf("lists: %+v", m)
	}
	if m.DraftCount != 4 || len(m.Drafts) != 3 || m.Drafts[0].ID != ids["D4"] || m.Drafts[0].Channel != "whatsapp" || m.Drafts[0].ListName != "Alpha" {
		t.Errorf("drafts: %d %+v", m.DraftCount, m.Drafts)
	}

	// ---- trends -----------------------------------------------------------
	tr := o.Trends
	if tr == nil || tr.Topics != 2 || tr.Matched != 3 {
		t.Fatalf("trends: %+v", tr)
	}
	if len(tr.Items) != 3 || tr.Items[0].ID != ids["n2"] || strings.Join(tr.Items[0].Topics, ",") != "Coffee,Tea" || tr.Items[1].ID != ids["n1"] || tr.Items[2].ID != ids["n4"] {
		t.Errorf("trend items (the unmatched n3 is newest and must not appear): %+v", tr.Items)
	}

	// ---- setup ------------------------------------------------------------
	want := map[string][2]bool{ // key -> {done, available}
		SetupInquiryForm: {true, true}, SetupSMTP: {true, true}, SetupChatWidget: {false, true},
		SetupWhatsApp: {false, true}, SetupGoogle: {false, false}, SetupBrowser: {false, true},
	}
	if len(o.Setup) != len(want) {
		t.Errorf("setup items: %+v", o.Setup)
	}
	for _, it := range o.Setup {
		if w, ok := want[it.Key]; !ok || w != [2]bool{it.Done, it.Available} {
			t.Errorf("setup %s: done=%v available=%v, want %v", it.Key, it.Done, it.Available, w)
		}
	}

	// Once things are connected, the checklist says so.
	s.exec(`UPDATE whatsapp_links SET state = 'open' WHERE workspace_id = $1`)
	s.exec(`INSERT INTO chat_widgets (workspace_id, name, public_key, created_by) VALUES ($1, 'w', $2, $3)`, "cw-"+s.ws, s.u)
	s.exec(`INSERT INTO browser_agents (workspace_id, name, token_hash, created_by) VALUES ($1, 'live', $2, $3)`, "th2-"+s.ws, s.u)
	s.exec(`INSERT INTO google_connections (workspace_id, google_sub) VALUES ($1, 'sub-1')`)
	o = build(t, a, s.ws, "owner", Env{WhatsAppAvailable: true, GoogleAvailable: true}, now)
	for _, it := range o.Setup {
		if !it.Done || !it.Available {
			t.Errorf("setup %s after connecting everything: %+v", it.Key, it)
		}
	}
}

// A role gets exactly the sections its own pages would give it — no more.
func TestOverviewSectionsFollowTheRole(t *testing.T) {
	a := testApp(t)
	loc := cairo(t)
	s := newWorkspace(t, a, "EUR", "Africa/Cairo")
	seedA(t, s, loc)
	now := at(loc, time.September, 15, 12, 0)
	env := Env{WhatsAppAvailable: true, GoogleAvailable: true}

	// A client holds workspace:read and nothing else: every section is null
	// (not empty), and the checklist shows nothing.
	c := build(t, a, s.ws, "client", env, now)
	if c.Pipeline != nil || c.Leads != nil || c.Conversations != nil || c.Notifications != nil || c.Marketing != nil || c.Trends != nil {
		t.Errorf("a client got a section: %+v", c)
	}
	if c.Today.Leads != nil || c.Today.LeadsReceivedToday != nil || c.Today.UnreadConversations != nil || c.Today.UnreadNotifications != nil || c.Today.Research != nil {
		t.Errorf("a client got a figure: %+v", c.Today)
	}
	if len(c.Setup) != 0 {
		t.Errorf("a client got setup items: %+v", c.Setup)
	}
	// And the JSON says null, so the page can tell "hidden" from "empty".
	b, _ := json.Marshal(c)
	for _, k := range []string{`"pipeline":null`, `"leads":null`, `"conversations":null`, `"notifications":null`, `"marketing":null`, `"trends":null`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("client JSON lacks %s: %s", k, b)
		}
	}

	// A member reads every area except the Google connections, which are an
	// admin's (integrations:manage) — so that one item is not on their list.
	m := build(t, a, s.ws, "member", env, now)
	if m.Pipeline == nil || m.Leads == nil || m.Conversations == nil || m.Notifications == nil || m.Marketing == nil || m.Marketing.Lists == nil || m.Trends == nil || m.Today.Research == nil {
		t.Errorf("a member is missing a section: %+v", m)
	}
	for _, it := range m.Setup {
		if it.Key == SetupGoogle {
			t.Errorf("a member sees the Google connection item: %+v", m.Setup)
		}
	}
	if len(m.Setup) != 5 {
		t.Errorf("member setup items: %+v", m.Setup)
	}

	ad := build(t, a, s.ws, "admin", env, now)
	var google bool
	for _, it := range ad.Setup {
		google = google || it.Key == SetupGoogle
	}
	if !google || len(ad.Setup) != 6 {
		t.Errorf("an admin should see all six items: %+v", ad.Setup)
	}
}

// Two workspaces: neither overview carries a single row of the other's, and
// asking for A's overview from inside B's scope finds nothing at all (RLS).
func TestOverviewIsolation(t *testing.T) {
	a := testApp(t)
	loc := cairo(t)
	sa := newWorkspace(t, a, "EUR", "Africa/Cairo")
	idsA := seedA(t, sa, loc)
	sb := newWorkspace(t, a, "USD", "UTC")
	now := at(loc, time.September, 15, 12, 0)

	// B has a little of its own, so "empty" is not what makes it pass.
	sb.exec(`INSERT INTO leads (workspace_id, name, email, message) VALUES ($1, 'B lead', 'b@example.test', 'hi')`)
	sb.exec(`INSERT INTO conversations (workspace_id, customer_name) VALUES ($1, 'B visitor')`)
	sb.exec(`INSERT INTO notification_items (workspace_id, title) VALUES ($1, 'B alert')`)

	ob := build(t, a, sb.ws, "owner", Env{}, now)
	raw, _ := json.Marshal(ob)
	for k, id := range idsA {
		if strings.Contains(string(raw), id) {
			t.Errorf("B's overview carries A's %s (%s)", k, id)
		}
	}
	if deref(ob.Today.UnreadConversations) != 1 || deref(ob.Today.UnreadNotifications) != 1 || (*ob.Today.Leads)["new"] != 1 || ob.Pipeline.OpenDeals != 0 || ob.Trends.Matched != 0 {
		t.Errorf("B's figures include A's rows: %s", raw)
	}
	for _, it := range ob.Setup {
		if it.Done {
			t.Errorf("B's setup reads A's configuration: %+v", it)
		}
	}

	// B's scope on the connection, A's id in the query: RLS hides A's
	// workspace row, so the build finds no workspace rather than A's data.
	_, err := Build(tenant.WithWorkspace(context.Background(), sb.ws), a.SQLDB, sa.ws, "owner", Env{}, now)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("A's overview under B's scope: %v, want ErrNotFound", err)
	}
}

// An unknown zone name must not take the page down; it falls back to UTC and
// says so.
func TestOverviewBadTimezoneFallsBackToUTC(t *testing.T) {
	a := testApp(t)
	s := newWorkspace(t, a, "EUR", "Mars/Olympus_Mons")
	o := build(t, a, s.ws, "owner", Env{}, time.Now())
	if o.Timezone != "UTC" {
		t.Errorf("timezone: %q, want UTC", o.Timezone)
	}
	if o.Pipeline == nil || len(o.Pipeline.Stages) != 0 || o.Pipeline.NextCloses == nil || o.Leads == nil || len(o.Leads) != 0 {
		t.Errorf("an empty workspace should give empty (not null) sections: %+v", o)
	}
}

func deref(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

func equalCounts(a, b LeadCounts) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
}

func dealIDs(l []DealItem) string {
	s := []string{}
	for _, d := range l {
		s = append(s, d.ID)
	}
	return strings.Join(s, ",")
}

func leadIDs(l []LeadItem) string {
	s := []string{}
	for _, d := range l {
		s = append(s, d.ID)
	}
	return strings.Join(s, ",")
}

func convIDs(l []ConversationItem) string {
	s := []string{}
	for _, d := range l {
		s = append(s, d.ID)
	}
	return strings.Join(s, ",")
}

func notifIDs(l []NotificationItem) string {
	s := []string{}
	for _, d := range l {
		s = append(s, d.ID)
	}
	return strings.Join(s, ",")
}
