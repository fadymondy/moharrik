package opportunities

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
Opportunity signals against a real database with RLS on (MH-1065). Every expected figure is worked
out by hand from the rows inserted below, relative to a fixed clock.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/opportunities/ -v
*/

func testApp(t *testing.T) *app.App {
	t.Helper()
	owner, appURL := testdb.Setup(t, "opps", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "opps-test-secret-opps-test-secret-opps-0001")
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
	user := "u-opps-" + suffix
	if _, err := a.SysDB.Exec(`INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', 'now')`, user, "opps-"+suffix+"@example.test"); err != nil {
		t.Fatal(err)
	}
	var ws string
	if err := a.SysDB.QueryRow(`INSERT INTO workspaces (name, slug, created_by, currency, timezone) VALUES ('Opps', $1, $2, 'USD', 'Africa/Cairo') RETURNING id`,
		"opps-"+suffix, user).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')`, ws, user); err != nil {
		t.Fatal(err)
	}
	return seeder{t: t, db: a.SysDB, ws: ws, u: user}
}

func read(t *testing.T, a *app.App, sc tenant.Scope, kind string, per int, now time.Time) *OpportunitySignals {
	t.Helper()
	got, err := Read(tenant.With(context.Background(), &sc), a.SQLDB, kind, per, now)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func n(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestSignals(t *testing.T) {
	a := testApp(t)
	s := newWorkspace(t, a)
	now := time.Date(2026, 9, 15, 15, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	h := time.Hour
	day := 24 * h

	// ---- deals ----------------------------------------------------------------
	qualify := s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Qualify', 0, 'open') RETURNING id`)
	won := s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Won', 1, 'won') RETURNING id`)
	deal := func(title, stage string, value int64, created time.Time) string {
		return s.id(`INSERT INTO deals (workspace_id, title, value_minor, currency, stage_id, created_by, created_at) VALUES ($1, $2, $3, 'USD', $4, $5, $6) RETURNING id`,
			title, value, stage, s.u, created)
	}
	big := deal("Big quiet", qualify, 900000, ago(30*day))
	small := deal("Small quiet", qualify, 100000, ago(10*day))
	fresh := deal("Fresh", qualify, 500000, ago(2*h))
	wonQuiet := deal("Won quiet", won, 777, ago(40*day))
	edge := deal("Six days", qualify, 300000, ago(6*day)) // under the 7-day threshold
	// Triggers add a stage event at insert time (the real now); pin every event to the deal's own time.
	s.exec(`DELETE FROM deal_stage_events WHERE workspace_id = $1`)
	for id, at := range map[string]time.Time{big: ago(30 * day), small: ago(10 * day), fresh: ago(2 * h), edge: ago(6 * day)} {
		s.exec(`INSERT INTO deal_stage_events (workspace_id, deal_id, stage_id, entered_at) VALUES ($1, $2, $3, $4)`, id, qualify, at)
	}
	s.exec(`INSERT INTO deal_stage_events (workspace_id, deal_id, stage_id, entered_at) VALUES ($1, $2, $3, $4)`, wonQuiet, won, ago(40*day))

	// ---- leads ----------------------------------------------------------------
	lead := func(name, status, src, form string, created time.Time) {
		s.exec(`INSERT INTO leads (workspace_id, name, email, message, status, utm_source, form_name, created_at) VALUES ($1, $2, $2 || '@example.test', 'hi', $3, $4, $5, $6)`,
			name, status, src, form, created)
	}
	lead("newest", "new", "google", "", ago(1*h))
	lead("formlead", "contacted", "", "Contact us", ago(2*day))
	lead("converted", "converted", "", "", ago(1*h)) // converted: not new
	lead("spam", "spam", "", "", ago(1*h))           // spam: not new
	lead("old", "new", "", "", ago(10*day))          // older than 7 days

	// ---- prospects --------------------------------------------------------------
	company := func(key string, evidence int) string {
		return s.id(`INSERT INTO research_companies (workspace_id, company_key, name, domain, evidence_count, first_seen_at) VALUES ($1, $2, $2, $2 || '.test', $3, now()) RETURNING id`, key, evidence)
	}
	prospect := func(c string, score float32, status string, created time.Time) string {
		return s.id(`INSERT INTO prospects (workspace_id, suggestion_id, score, status, created_at) VALUES ($1, $2, $3, $4, $5) RETURNING id`, c, score, status, created)
	}
	hi1 := prospect(company("acme", 3), 95, "new", ago(2*h))
	prospect(company("globex", 0), 70, "new", ago(1*day))   // exactly the threshold: in
	prospect(company("initech", 0), 69, "new", ago(1*h))    // below
	prospect(company("hooli", 0), 99, "accepted", ago(1*h)) // decided

	owner := tenant.Scope{WorkspaceID: s.ws, UserID: s.u, Role: "owner", Via: "session"}
	got := read(t, a, owner, "", 0, now)

	if got.Currency != "USD" || got.Thresholds.QuietDays != 7 || got.Thresholds.NewLeadDays != 7 || got.Thresholds.HighFitScore != 70 {
		t.Errorf("thresholds/currency = %+v %s", got.Thresholds, got.Currency)
	}
	if n(got.Counts.AtRiskDeals) != 2 || n(got.Counts.NewLeads) != 2 || n(got.Counts.HighFitProspects) != 2 {
		t.Fatalf("counts = %d/%d/%d, want 2/2/2", n(got.Counts.AtRiskDeals), n(got.Counts.NewLeads), n(got.Counts.HighFitProspects))
	}
	if len(got.Items) != 6 {
		t.Fatalf("items = %d, want 6", len(got.Items))
	}
	// Order: at-risk by value desc, leads newest first, prospects by score desc.
	wantTitles := []string{"Big quiet", "Small quiet", "newest", "formlead", "acme", "globex"}
	for i, w := range wantTitles {
		if got.Items[i].Title != w {
			t.Errorf("item %d = %q, want %q", i, got.Items[i].Title, w)
		}
	}
	if got.Items[0].ID != big || got.Items[0].Kind != KindAtRisk || got.Items[0].Tone != "warning" {
		t.Errorf("first item = %+v", got.Items[0])
	}
	if w := got.Items[0].Why; w[0].Code != "quiet_days" || n(w[0].N) != 30 || w[1].Code != "in_stage" || w[1].Text != "Qualify" {
		t.Errorf("at-risk why = %+v", w)
	}
	if got.Items[0].Value == nil || got.Items[0].Value.Minor != 900000 {
		t.Errorf("at-risk value = %+v", got.Items[0].Value)
	}
	if w := got.Items[2].Why; len(w) != 3 || w[0].Code != "new_lead" || w[1].Text != "new" || w[2].Code != "utm" || w[2].Text != "utm_source=google" {
		t.Errorf("lead why = %+v", w)
	}
	if w := got.Items[3].Why; len(w) != 3 || w[2].Code != "form" || w[2].Text != "Contact us" {
		t.Errorf("form lead why = %+v", w)
	}
	if p := got.Items[4]; p.ID != hi1 || p.Score == nil || *p.Score != 95 || n(p.Why[0].N) != 95 || len(p.Why) != 2 || p.Why[1].Code != "evidence" || n(p.Why[1].N) != 3 || p.Tone != "violet" {
		t.Errorf("prospect = %+v", p)
	}
	if len(got.Items[5].Why) != 1 {
		t.Errorf("a prospect with no evidence must not claim any: %+v", got.Items[5].Why)
	}
	for _, it := range got.Items {
		if it.ID == fresh || it.ID == wonQuiet || it.ID == edge || it.ID == small && it.Title != "Small quiet" {
			t.Errorf("%s must not be a signal", it.Title)
		}
	}
	if !has(got.Items[2].Actions, ActionConvert) || !has(got.Items[4].Actions, ActionAccept) || !has(got.Items[4].Actions, ActionDismiss) {
		t.Errorf("owner is missing actions: %v %v", got.Items[2].Actions, got.Items[4].Actions)
	}
	if has(got.Items[0].Actions, ActionConvert) || !has(got.Items[0].Actions, ActionOpen) || len(got.Items[0].Actions) != 1 {
		t.Errorf("deal actions = %v", got.Items[0].Actions)
	}

	// Kind filter: counts stay whole, only that kind's rows come back.
	only := read(t, a, owner, KindNewLead, 0, now)
	if len(only.Items) != 2 || only.Items[0].Kind != KindNewLead || n(only.Counts.AtRiskDeals) != 2 {
		t.Errorf("kind filter: %d items, at-risk count %d", len(only.Items), n(only.Counts.AtRiskDeals))
	}
	// Per-kind limit caps rows, not counts.
	lim := read(t, a, owner, "", 1, now)
	if len(lim.Items) != 3 || n(lim.Counts.AtRiskDeals) != 2 {
		t.Errorf("limit: %d items, at-risk count %d", len(lim.Items), n(lim.Counts.AtRiskDeals))
	}

	// A token without contacts:write sees the signals but is offered no accept/convert.
	tok := tenant.Scope{WorkspaceID: s.ws, UserID: s.u, Role: "owner", Via: "mcp", Scopes: []string{"contacts:read", "leads:read", "research:read", "workspace:read"}}
	tk := read(t, a, tok, "", 0, now)
	if len(tk.Items) != 6 || has(tk.Items[2].Actions, ActionConvert) || has(tk.Items[4].Actions, ActionAccept) {
		t.Errorf("read-only token: %d items", len(tk.Items))
	}
	// A token holding only leads:read sees leads and nothing else (null counts, not zero).
	lo := tenant.Scope{WorkspaceID: s.ws, UserID: s.u, Role: "owner", Via: "mcp", Scopes: []string{"leads:read"}}
	lr := read(t, a, lo, "", 0, now)
	if lr.Counts.AtRiskDeals != nil || lr.Counts.HighFitProspects != nil || n(lr.Counts.NewLeads) != 2 || len(lr.Sources) != 1 {
		t.Errorf("leads-only token: %+v %v", lr.Counts, lr.Sources)
	}

	// A client sees none of it: no sources, null counts, no items.
	cl := tenant.Scope{WorkspaceID: s.ws, UserID: s.u, Role: "client", Via: "session"}
	cr := read(t, a, cl, "", 0, now)
	if len(cr.Sources) != 0 || len(cr.Items) != 0 || cr.Counts.AtRiskDeals != nil || cr.Counts.NewLeads != nil || cr.Counts.HighFitProspects != nil {
		t.Errorf("client saw signals: %+v", cr)
	}

	// Another workspace's rows never leak in (RLS).
	other := newWorkspace(t, a)
	or := read(t, a, tenant.Scope{WorkspaceID: other.ws, UserID: other.u, Role: "owner", Via: "session"}, "", 0, now)
	if len(or.Items) != 0 || n(or.Counts.NewLeads) != 0 {
		t.Errorf("empty workspace has %d items", len(or.Items))
	}

	// Bad input.
	if _, err := Read(tenant.With(context.Background(), &owner), a.SQLDB, "nope", 0, now); err == nil {
		t.Error("an unknown kind must be rejected")
	}
	if _, err := Read(tenant.With(context.Background(), &owner), a.SQLDB, "", 101, now); err == nil {
		t.Error("a limit over the cap must be rejected")
	}
}
