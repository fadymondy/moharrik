package overview

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The traffic summary, the daily charts and the leads/conversations series added
for MH-567 ("one analytics summary card and real charts on the home page"),
against a real database with RLS on. Same rule as overview_db_test.go: every
expected figure is worked out by hand from the rows the test inserts.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/overview/ -v
*/

// trafficSeeder adds one Google connection and enabled resources of every
// kind, with a full 56-day history so both the current and the previous
// 28-day windows are "known" (>=20 of 28 days present) and the % change is
// round numbers, easy to assert exactly.
type trafficSeeder struct {
	seeder
	conn string
}

func newTrafficSeeder(t *testing.T, s seeder) trafficSeeder {
	t.Helper()
	conn := s.id(`INSERT INTO google_connections (workspace_id, google_sub, email) VALUES ($1, $2, $3) RETURNING id`,
		"sub-"+s.ws, "svc-"+s.ws+"@example.test")
	return trafficSeeder{seeder: s, conn: conn}
}

func (s trafficSeeder) resource(kind, name string) string {
	s.t.Helper()
	return s.id(`INSERT INTO google_resources (workspace_id, connection_id, kind, external_id, display_name, enabled)
		VALUES ($1, $2, $3, $4, $5, true) RETURNING id`, s.conn, kind, "ext-"+name, name)
}

// ga4Report writes the 28-day snapshot (what the headline totals sum), with
// the extra payload fields the summary reads beyond users/sessions/views.
func (s trafficSeeder) ga4Report(resourceID string, users, sessions, views, engagedSessions, engagementSeconds, keyEvents int64, start, end string) {
	s.t.Helper()
	payload, err := json.Marshal(map[string]any{
		"totals": map[string]any{
			"engaged_sessions":   engagedSessions,
			"engagement_seconds": engagementSeconds,
			"key_events":         keyEvents,
		},
	})
	if err != nil {
		s.t.Fatal(err)
	}
	s.exec(`INSERT INTO ga4_reports (workspace_id, resource_id, days, payload, users, sessions, views, start_date, end_date)
		VALUES ($1, $2, 28, $3, $4, $5, $6, $7::date, $8::date)`, resourceID, payload, users, sessions, views, start, end)
}

// ga4DailyRange fills one uniform row per day in [start,end] (inclusive),
// through generate_series, so a whole 28-day window's sum is exact and easy
// to predict from dailyUsers*28 etc.
func (s trafficSeeder) ga4DailyRange(resourceID string, start, end string, dailyUsers, dailySessions, dailyViews int64) {
	s.t.Helper()
	s.exec(`INSERT INTO ga4_daily (workspace_id, resource_id, day, users, sessions, views)
		SELECT $1, $2, d::date, $4, $5, $6 FROM generate_series($3::date, $7::date, interval '1 day') d`,
		resourceID, start, dailyUsers, dailySessions, dailyViews, end)
}

func (s trafficSeeder) gscDailyRange(resourceID string, start, end string, dailyClicks, dailyImpressions int64, position float64) {
	s.t.Helper()
	s.exec(`INSERT INTO gsc_search_totals_daily (workspace_id, resource_id, date, clicks, impressions, position)
		SELECT $1, $2, d::date, $4, $5, $6 FROM generate_series($3::date, $7::date, interval '1 day') d`,
		resourceID, start, dailyClicks, dailyImpressions, position, end)
}

func (s trafficSeeder) youtubeChannel(resourceID string, subs, videos int64, hidden bool) {
	s.t.Helper()
	s.exec(`INSERT INTO youtube_channels (workspace_id, resource_id, title, subscriber_count, subscribers_hidden, video_count)
		VALUES ($1, $2, 'Channel One', $3, $4, $5)`, resourceID, subs, hidden, videos)
}

func (s trafficSeeder) youtubeAnalytics(resourceID string, views28 int64) {
	s.t.Helper()
	payload, err := json.Marshal(map[string]any{"totals": map[string]any{"views": views28}})
	if err != nil {
		s.t.Fatal(err)
	}
	s.exec(`INSERT INTO youtube_analytics (workspace_id, resource_id, scope, days, payload) VALUES ($1, $2, 'channel', 28, $3)`, resourceID, payload)
}

// ymdStr is a small local helper so the test reads in dates, not time.Time
// arithmetic.
func ymdStr(loc *time.Location, m time.Month, d int) string {
	return at(loc, m, d, 0, 0).Format("2006-01-02")
}

func TestOverviewTrafficSummaryAggregatesAndDeltas(t *testing.T) {
	a := testApp(t)
	loc := cairo(t)
	s := newWorkspace(t, a, "EUR", "Africa/Cairo")
	ts := newTrafficSeeder(t, s)
	now := at(loc, time.September, 15, 12, 0)

	// The analytics window ends yesterday (Sep 14): periodStart=Aug18,
	// periodEnd=Sep14 (28 days); prevStart=Jul21, prevEnd=Aug17 (28 days) —
	// both windows fully populated below, so every delta is "known".
	periodStart, periodEnd := ymdStr(loc, time.August, 18), ymdStr(loc, time.September, 14)
	prevStart, prevEnd := ymdStr(loc, time.July, 21), ymdStr(loc, time.August, 17)

	p1 := ts.resource("ga4_property", "Site One")
	p2 := ts.resource("ga4_property", "Site Two")
	// P3 is enabled but was never fetched: it must show up nowhere as a
	// number, and must sort LAST in the top-properties list, never first.
	p3 := ts.resource("ga4_property", "Site Three")

	ts.ga4Report(p1, 1000, 1500, 3000, 900, 45000, 50, periodStart, periodEnd)
	ts.ga4Report(p2, 500, 700, 1200, 350, 14000, 20, periodStart, periodEnd)

	// Current period: P1 50/80/150 per day * 28 = 1400/2240/4200; P2 20/30/50 * 28 = 560/840/1400.
	ts.ga4DailyRange(p1, periodStart, periodEnd, 50, 80, 150)
	ts.ga4DailyRange(p2, periodStart, periodEnd, 20, 30, 50)
	// Previous period: P1 40/64/120 * 28 = 1120/1792/3360; P2 16/24/40 * 28 = 448/672/1120.
	ts.ga4DailyRange(p1, prevStart, prevEnd, 40, 64, 120)
	ts.ga4DailyRange(p2, prevStart, prevEnd, 16, 24, 40)
	// Total current: 1960/3080/5600. Total previous: 1568/2464/4480. Each is +25%.

	site1 := ts.resource("gsc_site", "Search A")
	site2 := ts.resource("gsc_site", "Search B")
	ts.gscDailyRange(site1, periodStart, periodEnd, 10, 100, 5.0)
	ts.gscDailyRange(site2, periodStart, periodEnd, 5, 50, 10.0)
	ts.gscDailyRange(site1, prevStart, prevEnd, 8, 80, 5.0)
	ts.gscDailyRange(site2, prevStart, prevEnd, 4, 40, 10.0)
	// Current: clicks 280+140=420, impressions 2800+1400=4200 (both sites' full 28 days).
	// Previous: clicks 224+112=336, impressions 2240+1120=3360. Each is +25%.

	yt := ts.resource("yt_channel", "Channel One")
	ts.youtubeChannel(yt, 5000, 42, false)
	ts.youtubeAnalytics(yt, 12345)

	o := build(t, a, s.ws, "owner", Env{}, now)
	if o.Traffic == nil || o.Traffic.Summary == nil {
		t.Fatal("no traffic summary")
	}
	sum := o.Traffic.Summary
	if sum.PeriodStart != periodStart || sum.PeriodEnd != periodEnd {
		t.Errorf("summary period: %s..%s, want %s..%s", sum.PeriodStart, sum.PeriodEnd, periodStart, periodEnd)
	}

	// ---- GA4 ----------------------------------------------------------
	g := sum.GA4
	if g == nil {
		t.Fatal("no GA4 summary")
	}
	if g.PropertiesCount != 3 {
		t.Errorf("properties_count: %d, want 3 (P3 counts even though never fetched)", g.PropertiesCount)
	}
	if got := derefI(g.Users); got != 1500 {
		t.Errorf("GA4 users (from ga4_reports totals): %d, want 1500", got)
	}
	if got := derefI(g.Sessions); got != 2200 {
		t.Errorf("GA4 sessions: %d, want 2200", got)
	}
	if got := derefI(g.Views); got != 4200 {
		t.Errorf("GA4 views: %d, want 4200", got)
	}
	if got := derefI(g.KeyEvents); got != 70 {
		t.Errorf("GA4 key_events: %d, want 70", got)
	}
	if g.EngagementRate == nil || !approx(*g.EngagementRate, 1250.0/2200.0) {
		t.Errorf("engagement rate: %v, want %v", g.EngagementRate, 1250.0/2200.0)
	}
	if g.AvgSessionSeconds == nil || !approx(*g.AvgSessionSeconds, 59000.0/2200.0) {
		t.Errorf("avg session seconds: %v, want %v", g.AvgSessionSeconds, 59000.0/2200.0)
	}
	// Deltas computed from ga4_daily on both sides, not from the headline sum:
	// current 1960/3080/5600, previous 1568/2464/4480, each is +25%.
	if g.UsersPct == nil || !approx(*g.UsersPct, 25) {
		t.Errorf("users delta: %v, want 25", g.UsersPct)
	}
	if g.SessionsPct == nil || !approx(*g.SessionsPct, 25) {
		t.Errorf("sessions delta: %v, want 25", g.SessionsPct)
	}
	if g.ViewsPct == nil || !approx(*g.ViewsPct, 25) {
		t.Errorf("views delta: %v, want 25", g.ViewsPct)
	}

	// ---- Search Console -------------------------------------------------
	sc := sum.SearchConsole
	if sc == nil {
		t.Fatal("no Search Console summary")
	}
	if sc.SitesCount != 2 {
		t.Errorf("sites_count: %d, want 2", sc.SitesCount)
	}
	if got := derefI(sc.Clicks); got != 420 {
		t.Errorf("clicks: %d, want 420", got)
	}
	if got := derefI(sc.Impressions); got != 4200 {
		t.Errorf("impressions: %d, want 4200", got)
	}
	if sc.CTR == nil || !approx(*sc.CTR, 0.1) {
		t.Errorf("ctr: %v, want 0.1", sc.CTR)
	}
	// Impression-weighted: (5.0*2800 + 10.0*1400) / 4200 = 28000/4200.
	if sc.AvgPosition == nil || !approx(*sc.AvgPosition, 28000.0/4200.0) {
		t.Errorf("avg position: %v, want %v", sc.AvgPosition, 28000.0/4200.0)
	}
	if sc.ClicksPct == nil || !approx(*sc.ClicksPct, 25) {
		t.Errorf("clicks delta: %v, want 25", sc.ClicksPct)
	}
	if sc.ImpressionsPct == nil || !approx(*sc.ImpressionsPct, 25) {
		t.Errorf("impressions delta: %v, want 25", sc.ImpressionsPct)
	}

	// ---- YouTube: totals only, never a delta -----------------------------
	yts := sum.YouTube
	if yts == nil {
		t.Fatal("no YouTube summary")
	}
	if yts.ChannelsCount != 1 || derefI(yts.Views28) != 12345 || derefI(yts.Subscribers) != 5000 {
		t.Errorf("youtube summary: %+v", yts)
	}

	// ---- top properties: P1 > P2 > P3 (never fetched, sorts last) --------
	if len(sum.TopProperties) != 3 {
		t.Fatalf("top properties: %+v", sum.TopProperties)
	}
	if sum.TopProperties[0].ResourceID != p1 || derefI(sum.TopProperties[0].Users) != 1000 {
		t.Errorf("top property 0: %+v, want P1/1000", sum.TopProperties[0])
	}
	if sum.TopProperties[1].ResourceID != p2 || derefI(sum.TopProperties[1].Users) != 500 {
		t.Errorf("top property 1: %+v, want P2/500", sum.TopProperties[1])
	}
	if sum.TopProperties[2].ResourceID != p3 || sum.TopProperties[2].Users != nil {
		t.Errorf("top property 2: %+v, want P3 with nil users last", sum.TopProperties[2])
	}

	// ---- daily chart: a day well inside the period sums both sources -----
	var day *TrafficDay
	for i := range sum.Daily {
		if sum.Daily[i].Date == ymdStr(loc, time.August, 25) {
			day = &sum.Daily[i]
		}
	}
	if day == nil {
		t.Fatal("Aug 25 missing from the daily series")
	}
	if derefI(day.Users) != 70 || derefI(day.Sessions) != 110 || derefI(day.Clicks) != 15 {
		t.Errorf("Aug 25: %+v, want users=70 sessions=110 clicks=15", day)
	}
	if len(sum.Daily) != 28 {
		t.Errorf("daily series length: %d, want 28 (periodStart..periodEnd inclusive)", len(sum.Daily))
	}
}

// A property enabled with no ga4_reports/ga4_daily row at all, and a
// workspace whose previous-period history is too thin, must show nulls
// rather than a fabricated 0% change — the daily series must be null (not 0)
// for the days that were genuinely never fetched.
func TestOverviewTrafficSummaryUnknownDeltaAndNeverFetched(t *testing.T) {
	a := testApp(t)
	loc := cairo(t)
	s := newWorkspace(t, a, "EUR", "Africa/Cairo")
	ts := newTrafficSeeder(t, s)
	now := at(loc, time.September, 15, 12, 0)

	periodStart, periodEnd := ymdStr(loc, time.August, 18), ymdStr(loc, time.September, 14)

	p1 := ts.resource("ga4_property", "Only Site")
	ts.ga4Report(p1, 1000, 1500, 3000, 900, 45000, 50, periodStart, periodEnd)
	// Only the current period's daily history exists; the previous period has
	// nothing at all, so the delta must be null, never a fake 0% or 100%.
	ts.ga4DailyRange(p1, periodStart, periodEnd, 50, 80, 150)

	// A GSC site that was enabled but never imported: sites_count is still 1,
	// but every figure is null and the delta is null too.
	ts.resource("gsc_site", "Never Imported")

	o := build(t, a, s.ws, "owner", Env{}, now)
	if o.Traffic == nil || o.Traffic.Summary == nil {
		t.Fatal("no traffic summary")
	}
	g := o.Traffic.Summary.GA4
	if g == nil || g.UsersPct != nil || g.SessionsPct != nil || g.ViewsPct != nil {
		t.Errorf("GA4 deltas should be nil with no previous history: %+v", g)
	}
	if derefI(g.Users) != 1000 {
		t.Errorf("GA4 users should still be the headline total: %d", derefI(g.Users))
	}

	sc := o.Traffic.Summary.SearchConsole
	if sc == nil || sc.SitesCount != 1 {
		t.Fatalf("search console summary: %+v", sc)
	}
	if sc.Clicks != nil || sc.Impressions != nil || sc.CTR != nil || sc.AvgPosition != nil || sc.ClicksPct != nil || sc.ImpressionsPct != nil {
		t.Errorf("a never-imported site must show nulls, not zeros: %+v", sc)
	}

	// No YouTube channel enabled at all: the whole section is null.
	if o.Traffic.Summary.YouTube != nil {
		t.Errorf("no YouTube channel enabled: %+v", o.Traffic.Summary.YouTube)
	}

	// The daily series: a day inside the period has GA4 numbers but a null
	// clicks figure (no GSC data was ever imported for that site).
	var day *TrafficDay
	for i := range o.Traffic.Summary.Daily {
		if o.Traffic.Summary.Daily[i].Date == ymdStr(loc, time.August, 25) {
			day = &o.Traffic.Summary.Daily[i]
		}
	}
	if day == nil {
		t.Fatal("Aug 25 missing")
	}
	if derefI(day.Users) != 50 || day.Clicks != nil {
		t.Errorf("Aug 25: %+v, want users=50 and clicks=null", day)
	}
}

// Leads and conversations are the workspace's own first-party data, so a day
// with none really is 0 — the series is zero-filled across the whole 28-day
// window ending today, including the workspace's own timezone boundary.
func TestOverviewLeadsAndConversationsDailyZeroFill(t *testing.T) {
	a := testApp(t)
	loc := cairo(t)
	s := newWorkspace(t, a, "EUR", "Africa/Cairo")
	now := at(loc, time.September, 15, 12, 0)

	// Window is Aug19..Sep15 (28 days, today included). One lead exactly at
	// the start of the window, one at 00:10 Cairo on Sep15 (still "today" in
	// the workspace's zone even though it is Sep14 in UTC), and a chat and a
	// WhatsApp conversation on the same day to check the per-channel split.
	s.exec(`INSERT INTO leads (workspace_id, name, email, message, created_at) VALUES ($1, 'first day', 'a@example.test', 'hi', $2)`, at(loc, time.August, 19, 9, 0))
	s.exec(`INSERT INTO leads (workspace_id, name, email, message, created_at) VALUES ($1, 'today early', 'b@example.test', 'hi', $2)`, at(loc, time.September, 15, 0, 10))
	s.exec(`INSERT INTO leads (workspace_id, name, email, message, created_at) VALUES ($1, 'before window', 'c@example.test', 'hi', $2)`, at(loc, time.August, 18, 9, 0))

	s.exec(`INSERT INTO conversations (workspace_id, customer_name, channel, created_at) VALUES ($1, 'chat one', 'chat', $2)`, at(loc, time.September, 1, 9, 0))
	s.exec(`INSERT INTO conversations (workspace_id, customer_name, channel, created_at) VALUES ($1, 'wa one', 'whatsapp', $2)`, at(loc, time.September, 1, 10, 0))
	s.exec(`INSERT INTO conversations (workspace_id, customer_name, channel, created_at) VALUES ($1, 'wa two', 'whatsapp', $2)`, at(loc, time.September, 1, 11, 0))

	o := build(t, a, s.ws, "owner", Env{}, now)
	if len(o.LeadsDaily) != 28 {
		t.Fatalf("leads daily length: %d, want 28", len(o.LeadsDaily))
	}
	if o.LeadsDaily[0].Date != ymdStr(loc, time.August, 19) || o.LeadsDaily[0].Count != 1 {
		t.Errorf("first day: %+v, want Aug 19 with count 1 (the pre-window lead must not appear)", o.LeadsDaily[0])
	}
	if last := o.LeadsDaily[len(o.LeadsDaily)-1]; last.Date != ymdStr(loc, time.September, 15) || last.Count != 1 {
		t.Errorf("today: %+v, want Sep 15 with count 1 (00:10 Cairo is still today)", last)
	}
	var zeroDays int
	for _, d := range o.LeadsDaily {
		if d.Count == 0 {
			zeroDays++
		}
	}
	if zeroDays != 26 {
		t.Errorf("zero-filled days: %d, want 26 of 28", zeroDays)
	}

	if len(o.ConversationsDaily) != 28 {
		t.Fatalf("conversations daily length: %d, want 28", len(o.ConversationsDaily))
	}
	var sep1 *ConversationDay
	for i := range o.ConversationsDaily {
		if o.ConversationsDaily[i].Date == ymdStr(loc, time.September, 1) {
			sep1 = &o.ConversationsDaily[i]
		}
	}
	if sep1 == nil || sep1.Chat != 1 || sep1.WhatsApp != 2 || sep1.Email != 0 {
		t.Errorf("Sep 1: %+v, want chat=1 whatsapp=2 email=0", sep1)
	}
}

// The new MH-567 fields follow the same role and RLS rules as the rest of the
// Overview: null without the section's own :read action, and never carrying
// another workspace's rows.
func TestOverviewNewFieldsFollowRoleAndIsolation(t *testing.T) {
	a := testApp(t)
	loc := cairo(t)
	sa := newWorkspace(t, a, "EUR", "Africa/Cairo")
	ts := newTrafficSeeder(t, sa)
	now := at(loc, time.September, 15, 12, 0)

	periodStart, periodEnd := ymdStr(loc, time.August, 18), ymdStr(loc, time.September, 14)
	p1 := ts.resource("ga4_property", "Site One")
	ts.ga4Report(p1, 100, 150, 300, 90, 4500, 5, periodStart, periodEnd)
	sa.exec(`INSERT INTO leads (workspace_id, name, email, message, created_at) VALUES ($1, 'x', 'x@example.test', 'hi', $2)`, at(loc, time.September, 1, 9, 0))
	sa.exec(`INSERT INTO conversations (workspace_id, customer_name, channel, created_at) VALUES ($1, 'x', 'chat', $2)`, at(loc, time.September, 1, 9, 0))

	// A client (workspace:read only) gets none of it.
	c := build(t, a, sa.ws, "client", Env{}, now)
	if c.LeadsDaily != nil || c.ConversationsDaily != nil {
		t.Errorf("a client got the daily series: leads=%v conversations=%v", c.LeadsDaily, c.ConversationsDaily)
	}
	if c.Traffic != nil {
		t.Errorf("a client got traffic: %+v", c.Traffic)
	}
	raw, _ := json.Marshal(c)
	for _, k := range []string{`"leads_daily":null`, `"conversations_daily":null`, `"traffic":null`} {
		if !strings.Contains(string(raw), k) {
			t.Errorf("client JSON lacks %s: %s", k, raw)
		}
	}

	// A member reads leads/inbox but has no analytics:read grant in this
	// workspace's default role set — verify by checking whichever of
	// leads/conversations a member does hold is populated (leads:read and
	// inbox:read are member-level actions).
	m := build(t, a, sa.ws, "member", Env{}, now)
	if m.LeadsDaily == nil || m.ConversationsDaily == nil {
		t.Errorf("a member should read leads_daily/conversations_daily: %+v / %+v", m.LeadsDaily, m.ConversationsDaily)
	}
	if len(m.LeadsDaily) != 28 || len(m.ConversationsDaily) != 28 {
		t.Errorf("member daily series length: %d / %d, want 28 / 28", len(m.LeadsDaily), len(m.ConversationsDaily))
	}

	// Isolation: workspace B has its own analytics and daily activity; A's
	// resource ids, property names and figures must not appear anywhere in
	// B's overview.
	sb := newWorkspace(t, a, "USD", "UTC")
	tsB := newTrafficSeeder(t, sb)
	pB := tsB.resource("ga4_property", "B Site")
	tsB.ga4Report(pB, 999, 999, 999, 900, 45000, 9, periodStart, periodEnd)
	sb.exec(`INSERT INTO leads (workspace_id, name, email, message, created_at) VALUES ($1, 'B lead', 'bl@example.test', 'hi', $2)`, at(loc, time.September, 1, 9, 0))

	ob := build(t, a, sb.ws, "owner", Env{}, now)
	rawB, _ := json.Marshal(ob)
	if strings.Contains(string(rawB), p1) {
		t.Errorf("B's overview carries A's GA4 resource id %s", p1)
	}
	if ob.Traffic == nil || ob.Traffic.Summary == nil || ob.Traffic.Summary.GA4 == nil || derefI(ob.Traffic.Summary.GA4.Users) != 999 {
		t.Errorf("B's own GA4 summary: %+v", ob.Traffic)
	}
	var todaySum int64
	for _, d := range ob.LeadsDaily {
		todaySum += d.Count
	}
	if todaySum != 1 {
		t.Errorf("B's leads_daily should total 1 (its own lead only), got %d", todaySum)
	}

	// And asking for A's overview from inside B's connection scope finds
	// nothing (same RLS the rest of the package already exercises).
	_, err := Build(tenant.WithWorkspace(context.Background(), sb.ws), a.SQLDB, sa.ws, "owner", Env{}, now)
	if err == nil {
		t.Error("A's overview under B's scope should fail")
	}
}

func derefI(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

func approx(got, want float64) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d < 0.01
}
