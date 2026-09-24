package reports

import (
	"context"
	"database/sql"
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
The CRM report against a real database with RLS on (MH-394). Every expected
figure below is worked out by hand from the rows the test inserts: the report's
numbers must be the data's numbers.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/reports/ -v
*/

func testApp(t *testing.T) *app.App {
	t.Helper()
	owner, appURL := testdb.Setup(t, "reports", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "reports-test-secret-reports-test-secret-00001")
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

// seeder writes through the OWNER pool so timestamps land exactly as written.
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
	suffix := strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "-") + "-" + string(rune('a'+seq%26))
	user := "u-rep-" + suffix
	if _, err := a.SysDB.Exec(`INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', 'now')`,
		user, "rep-"+suffix+"@example.test"); err != nil {
		t.Fatal(err)
	}
	var ws string
	if err := a.SysDB.QueryRow(`INSERT INTO workspaces (name, slug, created_by, currency, timezone) VALUES ('Reports', $1, $2, $3, $4) RETURNING id`,
		"rep-"+suffix, user, currency, tz).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')`, ws, user); err != nil {
		t.Fatal(err)
	}
	return seeder{t: t, db: a.SysDB, ws: ws, u: user}
}

func cairo(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		t.Skip("no tz database on this machine")
	}
	return loc
}

type fixture struct {
	seeder
	lead, qual, prop, won, lost string
	d1                          string
}

// seedFigures builds the workspace the figures test reads. In words:
//
//	D1 opened 06-02 Lead→Qualified→Proposal→Won, 1,000 EGP, closed 06-10
//	D2 opened 06-03 Lead→Qualified→Lost,           500 EGP, closed 06-12
//	D3 opened 06-05, in Lead,                     2,000 EGP
//	D4 opened 06-05, created straight in Proposal, 3,000 EGP
//	D5 opened 06-06, created Won,                    400 USD, closed 06-20
//	D6 opened 05-10, Won,                            600 EGP, closed 06-15
//	D7 opened 06-07, Lost,                           100 EGP, closed 07-01 00:30 (after the range)
//	D8 opened 04-01, in Qualified,                   700 EGP
//	D9 opened 06-25, in Lead,                         50 USD
//	D10 opened 05-01, Lost,                           80 EGP, closed 05-31 23:30 (before the range)
func seedFigures(t *testing.T, a *app.App) fixture {
	loc := cairo(t)
	s := newWorkspace(t, a, "EGP", "Africa/Cairo")
	f := fixture{seeder: s}
	f.lead = s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Lead', 0, 'open') RETURNING id`)
	f.qual = s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Qualified', 1, 'open') RETURNING id`)
	f.prop = s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Proposal', 2, 'open') RETURNING id`)
	f.won = s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Won', 3, 'won') RETURNING id`)
	f.lost = s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Lost', 4, 'lost') RETURNING id`)
	at := func(m time.Month, d, h, min int) time.Time { return time.Date(2026, m, d, h, min, 0, 0, loc) }
	deal := func(title string, value int64, cur, stage string, created time.Time, closed *time.Time) string {
		return s.id(`INSERT INTO deals (workspace_id, title, value_minor, currency, stage_id, created_by, created_at, closed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`, title, value, cur, stage, s.u, created, closed)
	}
	move := func(id string, stages ...string) {
		for _, st := range stages {
			s.exec(`UPDATE deals SET stage_id = $3 WHERE workspace_id = $1 AND id = $2`, id, st)
		}
	}
	tp := func(t time.Time) *time.Time { return &t }

	f.d1 = deal("D1", 100000, "EGP", f.lead, at(6, 2, 9, 0), nil)
	move(f.d1, f.qual, f.prop, f.won)
	s.exec(`UPDATE deals SET closed_at = $3 WHERE workspace_id = $1 AND id = $2`, f.d1, at(6, 10, 10, 0))
	d2 := deal("D2", 50000, "EGP", f.lead, at(6, 3, 9, 0), nil)
	move(d2, f.qual, f.lost)
	s.exec(`UPDATE deals SET closed_at = $3 WHERE workspace_id = $1 AND id = $2`, d2, at(6, 12, 15, 0))
	deal("D3", 200000, "EGP", f.lead, at(6, 5, 9, 0), nil)
	deal("D4", 300000, "EGP", f.prop, at(6, 5, 11, 0), nil)
	deal("D5", 40000, "USD", f.won, at(6, 6, 9, 0), tp(at(6, 20, 13, 0)))
	deal("D6", 60000, "EGP", f.won, at(5, 10, 9, 0), tp(at(6, 15, 9, 0)))
	deal("D7", 10000, "EGP", f.lost, at(6, 7, 9, 0), tp(at(7, 1, 0, 30)))
	deal("D8", 70000, "EGP", f.qual, at(4, 1, 9, 0), nil)
	deal("D9", 5000, "USD", f.lead, at(6, 25, 9, 0), nil)
	deal("D10", 8000, "EGP", f.lost, at(5, 1, 9, 0), tp(at(5, 31, 23, 30)))

	// Leads: L1 utm "Google " converted into D1; L2 a Google Ads click;
	// L3/L4 referrers; L5 nothing; L6 spam; L7 before the range.
	lead := func(email, utm, gclid, ref, status string, created time.Time, dealID any) {
		conv := any(nil)
		if dealID != nil {
			conv = created.Add(time.Hour)
		}
		s.exec(`INSERT INTO leads (workspace_id, email, message, utm_source, gclid, referrer, status, created_at, deal_id, converted_at)
			VALUES ($1, $2, 'hello', $3, $4, $5, $6, $7, $8, $9)`, email, utm, gclid, ref, status, created, dealID, conv)
	}
	lead("l1@x.test", "Google ", "", "", "converted", at(6, 1, 8, 0), f.d1)
	lead("l2@x.test", "", "Cj0K-abc", "https://www.google.com/", "new", at(6, 8, 8, 0), nil)
	lead("l3@x.test", "", "", "https://www.linkedin.com/feed/", "contacted", at(6, 9, 8, 0), nil)
	lead("l4@x.test", "", "", "https://l.instagram.com/?u=x", "new", at(6, 10, 8, 0), nil)
	lead("l5@x.test", "", "", "", "new", at(6, 11, 8, 0), nil)
	lead("l6@x.test", "google", "", "", "spam", at(6, 12, 8, 0), nil)
	lead("l7@x.test", "google", "", "", "new", at(5, 20, 8, 0), nil)

	// Inbox: C1 chat answered in 10 minutes, C2 email in 2 hours, C3 WhatsApp
	// unanswered, C4 before the range.
	conv := func(ch string, created time.Time, in time.Time, out *time.Time) {
		id := s.id(`INSERT INTO conversations (workspace_id, channel, created_at, last_message_at) VALUES ($1, $2, $3, $3) RETURNING id`, ch, created)
		s.exec(`INSERT INTO messages (workspace_id, conversation_id, direction, body, created_at) VALUES ($1, $2, 'in', 'hi', $3)`, id, in)
		if out != nil {
			s.exec(`INSERT INTO messages (workspace_id, conversation_id, direction, author_user_id, body, created_at) VALUES ($1, $2, 'out', $3, 'hello', $4)`, id, s.u, *out)
		}
	}
	conv("chat", at(6, 2, 10, 0), at(6, 2, 10, 0), tp(at(6, 2, 10, 10)))
	conv("email", at(6, 3, 9, 0), at(6, 3, 9, 0), tp(at(6, 3, 11, 0)))
	conv("whatsapp", at(6, 4, 9, 0), at(6, 4, 9, 0), nil)
	conv("chat", at(5, 30, 9, 0), at(5, 30, 9, 0), tp(at(5, 30, 9, 1)))
	return f
}

func report(t *testing.T, a *app.App, ws string, access Access, r Range, now time.Time) *CRMReport {
	t.Helper()
	rep, err := BuildCRM(tenant.WithWorkspace(context.Background(), ws), a.SQLDB, ws, access, r, now)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func near(p *float64, want float64) bool { return p != nil && *p > want-1e-9 && *p < want+1e-9 }

func TestCRMReportFigures(t *testing.T) {
	a := testApp(t)
	f := seedFigures(t, a)
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, cairo(t))
	rep := report(t, a, f.ws, Access{Leads: true, Inbox: true}, Range{From: "2026-06-01", To: "2026-06-30"}, now)

	if rep.From != "2026-06-01" || rep.To != "2026-06-30" || rep.Days != 30 || rep.Bucket != "day" || rep.Currency != "EGP" || rep.Timezone != "Africa/Cairo" {
		t.Fatalf("header: %+v", rep)
	}

	// Pipeline now: Lead D3 + D9(USD), Qualified D8, Proposal D4.
	p := rep.Pipeline
	if len(p.Stages) != 3 || p.Stages[0].Name != "Lead" || p.Stages[0].DealCount != 2 || p.Stages[0].ValueMinor != 200000 ||
		p.Stages[1].DealCount != 1 || p.Stages[1].ValueMinor != 70000 || p.Stages[2].DealCount != 1 || p.Stages[2].ValueMinor != 300000 {
		t.Errorf("pipeline stages: %+v", p.Stages)
	}
	if p.OpenDeals != 4 || p.OpenValueMinor != 570000 || p.OtherCurrencyDeals != 1 {
		t.Errorf("pipeline totals: %+v", p)
	}

	// Closed in June: won D1, D5 (USD), D6; lost D2. D7 and D10 fall outside.
	d := rep.Deals
	if d.Won != 3 || d.Lost != 1 || d.WonValueMinor != 160000 || d.LostValueMinor != 50000 || d.OtherCurrencyWon != 1 || d.OtherCurrencyLost != 0 {
		t.Errorf("deals: %+v", d)
	}
	if !near(d.WinRate, 0.75) || d.AvgWonValueMinor == nil || *d.AvgWonValueMinor != 80000 || d.Opened != 7 {
		t.Errorf("win rate / average / opened: %+v avg=%v", d, d.AvgWonValueMinor)
	}

	// Conversion, cohort D1 D2 D3 D4 D5 D7 D9.
	c := rep.Conversion
	if c.Opened != 7 || c.Lost != 2 || len(c.Steps) != 4 {
		t.Fatalf("conversion: %+v", c)
	}
	want := []struct {
		name    string
		reached int64
	}{{"Lead", 7}, {"Qualified", 4}, {"Proposal", 3}, {"Won", 2}}
	for i, w := range want {
		if c.Steps[i].Name != w.name || c.Steps[i].Reached != w.reached {
			t.Errorf("step %d: got %s %d, want %s %d", i, c.Steps[i].Name, c.Steps[i].Reached, w.name, w.reached)
		}
	}
	if c.Steps[0].FromPrevious != nil || !near(c.Steps[1].FromPrevious, 4.0/7) || !near(c.Steps[2].FromPrevious, 3.0/4) ||
		!near(c.Steps[3].FromPrevious, 2.0/3) || !near(c.Steps[3].FromStart, 2.0/7) || c.Steps[3].Kind != "won" || c.Steps[3].StageID != f.won {
		t.Errorf("conversion rates: %+v", c.Steps)
	}

	// Day buckets: every day of June, the closes on the 10th, 12th, 15th, 20th.
	if len(rep.Timeline) != 30 || rep.Timeline[0].Start != "2026-06-01" || rep.Timeline[29].Start != "2026-06-30" {
		t.Fatalf("timeline: %d buckets %+v", len(rep.Timeline), rep.Timeline)
	}
	byDay := map[string]ReportBucket{}
	var won, lost int64
	for _, b := range rep.Timeline {
		byDay[b.Start] = b
		won += b.Won
		lost += b.Lost
	}
	if won != 3 || lost != 1 || byDay["2026-06-10"].WonValueMinor != 100000 || byDay["2026-06-12"].LostValueMinor != 50000 ||
		byDay["2026-06-15"].Won != 1 || byDay["2026-06-20"].Won != 1 || byDay["2026-06-20"].WonValueMinor != 0 {
		t.Errorf("timeline figures: %+v", rep.Timeline)
	}

	// Lead sources.
	ls := rep.LeadSources
	if ls == nil || ls.Leads != 5 || ls.Spam != 1 || ls.Converted != 1 {
		t.Fatalf("lead totals: %+v", ls)
	}
	got := []string{}
	for _, s := range ls.Sources {
		got = append(got, s.Source)
	}
	if strings.Join(got, ",") != "google,direct,instagram.com,linkedin.com" {
		t.Errorf("sources: %v", got)
	}
	if g := ls.Sources[0]; g.Leads != 2 || g.Converted != 1 || g.WonDeals != 1 || g.WonValueMinor != 100000 {
		t.Errorf("google: %+v", g)
	}

	// Inbox: C1 600 s, C2 7200 s, C3 awaiting.
	in := rep.Inbox
	if in == nil || in.Total.Conversations != 3 || in.Total.Answered != 2 || in.Total.Awaiting != 1 ||
		!near(in.Total.MedianSeconds, 3900) || !near(in.Total.MeanSeconds, 3900) {
		t.Fatalf("inbox: %+v", in)
	}
	if in.Channels[0].Channel != "chat" || in.Channels[0].Conversations != 1 || !near(in.Channels[0].MedianSeconds, 600) ||
		in.Channels[2].Channel != "whatsapp" || in.Channels[2].Awaiting != 1 || in.Channels[2].MedianSeconds != nil {
		t.Errorf("inbox channels: %+v", in.Channels)
	}

	// A quarter groups by week (Monday starts), and the totals still agree:
	// April to June also takes D10, lost on 31 May at 23:30 Cairo time.
	q := report(t, a, f.ws, Access{}, Range{From: "2026-04-01", To: "2026-06-30"}, now)
	if q.Bucket != "week" || q.Timeline[0].Start != "2026-03-30" {
		t.Errorf("quarter buckets: %s %+v", q.Bucket, q.Timeline[:1])
	}
	won, lost = 0, 0
	for _, b := range q.Timeline {
		won += b.Won
		lost += b.Lost
	}
	if won != 3 || lost != 2 || q.Deals.Won != 3 || q.Deals.Lost != 2 {
		t.Errorf("quarter: won %d lost %d, deals %+v", won, lost, q.Deals)
	}
	// Without leads:read and inbox:read those sections are null, not zero.
	if q.LeadSources != nil || q.Inbox != nil {
		t.Errorf("sections the caller may not read: %+v %+v", q.LeadSources, q.Inbox)
	}
}

// An empty workspace answers zeros it measured and nulls for what it cannot
// divide — never a made-up rate.
func TestCRMReportEmpty(t *testing.T) {
	a := testApp(t)
	s := newWorkspace(t, a, "USD", "UTC")
	rep := report(t, a, s.ws, Access{Leads: true, Inbox: true}, Range{}, time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC))
	if rep.From != "2026-06-26" || rep.To != "2026-09-23" || rep.Days != 90 || rep.Bucket != "week" {
		t.Errorf("default range: %s..%s %d %s", rep.From, rep.To, rep.Days, rep.Bucket)
	}
	if rep.Deals.WinRate != nil || rep.Deals.AvgWonValueMinor != nil || rep.Conversion.Opened != 0 || len(rep.Conversion.Steps) != 0 {
		t.Errorf("empty deals: %+v %+v", rep.Deals, rep.Conversion)
	}
	if rep.LeadSources == nil || len(rep.LeadSources.Sources) != 0 || rep.Inbox == nil || rep.Inbox.Total.MedianSeconds != nil {
		t.Errorf("empty sections: %+v %+v", rep.LeadSources, rep.Inbox)
	}
}

func TestCRMReportRange(t *testing.T) {
	a := testApp(t)
	s := newWorkspace(t, a, "USD", "UTC")
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	for _, r := range []Range{{From: "2026-09-10", To: "2026-09-01"}, {From: "2024-01-01", To: "2026-01-02"}, {From: "yesterday"}} {
		if _, err := BuildCRM(tenant.WithWorkspace(context.Background(), s.ws), a.SQLDB, s.ws, Access{}, r, now); !errors.Is(err, ErrBadRange) {
			t.Errorf("%+v: got %v, want ErrBadRange", r, err)
		}
	}
	// days is the window ending on to (or today), in the workspace's zone.
	rep30 := report(t, a, s.ws, Access{}, Range{Days: 30}, now)
	if rep30.From != "2026-08-25" || rep30.To != "2026-09-23" || rep30.Days != 30 {
		t.Errorf("days=30: %s..%s %d", rep30.From, rep30.To, rep30.Days)
	}
	if _, err := BuildCRM(tenant.WithWorkspace(context.Background(), s.ws), a.SQLDB, s.ws, Access{}, Range{Days: 800}, now); !errors.Is(err, ErrBadRange) {
		t.Errorf("days=800: %v", err)
	}
	// Over 184 days groups by month.
	rep := report(t, a, s.ws, Access{}, Range{From: "2026-01-01", To: "2026-09-23"}, now)
	if rep.Bucket != "month" || len(rep.Timeline) != 9 || rep.Timeline[0].Start != "2026-01-01" {
		t.Errorf("month buckets: %s %d %+v", rep.Bucket, len(rep.Timeline), rep.Timeline[:1])
	}
}

// CRMFor decides the sections from the caller: a browser session by its role,
// an MCP token by its scopes as well (a CRM-only token reads no leads and no
// inbox, whatever its member could).
func TestCRMForAccess(t *testing.T) {
	a := testApp(t)
	f := seedFigures(t, a)
	ctx := func(sc *tenant.Scope) context.Context {
		sc.WorkspaceID = f.ws
		return tenant.With(context.Background(), sc)
	}
	r := Range{From: "2026-06-01", To: "2026-06-30"}
	now := time.Now()

	rep, err := CRMFor(ctx(&tenant.Scope{Role: "member", Via: "session"}), a.SQLDB, r, now)
	if err != nil || rep.LeadSources == nil || rep.Inbox == nil {
		t.Fatalf("a member's session: %v %+v", err, rep)
	}
	rep, err = CRMFor(ctx(&tenant.Scope{Role: "owner", Via: "mcp", Scopes: []string{"contacts:read"}}), a.SQLDB, r, now)
	if err != nil || rep.LeadSources != nil || rep.Inbox != nil {
		t.Fatalf("a CRM-only token: %v %+v %+v", err, rep.LeadSources, rep.Inbox)
	}
	rep, err = CRMFor(ctx(&tenant.Scope{Role: "owner", Via: "mcp", Scopes: []string{"contacts:read", "leads:read", "inbox:read"}}), a.SQLDB, r, now)
	if err != nil || rep.LeadSources == nil || rep.Inbox == nil || rep.Deals.Won != 3 {
		t.Fatalf("a token with all three: %v %+v", err, rep)
	}
	if _, err := CRMFor(ctx(&tenant.Scope{Role: "client", Via: "session"}), a.SQLDB, r, now); err == nil || !strings.Contains(err.Error(), "role") {
		t.Errorf("a client may not read the CRM report: %v", err)
	}
	if _, err := CRMFor(ctx(&tenant.Scope{Role: "member", Via: "session"}), a.SQLDB, Range{From: "2026-07-01", To: "2026-06-01"}, now); err == nil {
		t.Error("a reversed range should be refused")
	}
}

// The stage history the funnel reads is written by the trigger, whatever path
// moved the deal — including the RLS pool with the workspace set.
func TestStageHistoryTrigger(t *testing.T) {
	a := testApp(t)
	f := seedFigures(t, a)
	var n int
	if err := a.SysDB.QueryRow(`SELECT count(*) FROM deal_stage_events WHERE workspace_id = $1 AND deal_id = $2`, f.ws, f.d1).Scan(&n); err != nil || n != 4 {
		t.Fatalf("D1 moved Lead→Qualified→Proposal→Won: %d events (%v)", n, err)
	}
	ctx := tenant.WithWorkspace(context.Background(), f.ws)
	if _, err := a.SQLDB.ExecContext(ctx, `UPDATE deals SET stage_id = $3 WHERE workspace_id = $1 AND id = $2`, f.ws, f.d1, f.lead); err != nil {
		t.Fatalf("moving on the app pool: %v", err)
	}
	var from, to string
	if err := a.SysDB.QueryRow(`SELECT from_stage_id, stage_id FROM deal_stage_events WHERE workspace_id = $1 AND deal_id = $2 ORDER BY entered_at DESC, id LIMIT 1`,
		f.ws, f.d1).Scan(&from, &to); err != nil || from != f.won || to != f.lead {
		t.Errorf("latest event: %s -> %s (%v)", from, to, err)
	}
	// Another workspace sees none of it through the app pool.
	other := newWorkspace(t, a, "USD", "UTC")
	var seen int
	if err := a.SQLDB.QueryRowContext(tenant.WithWorkspace(context.Background(), other.ws), `SELECT count(*) FROM deal_stage_events`).Scan(&seen); err != nil || seen != 0 {
		t.Errorf("another workspace reads %d stage events (%v)", seen, err)
	}
}
