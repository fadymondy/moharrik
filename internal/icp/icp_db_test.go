package icp

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
)

/*
Lifecycle and scoring against a real database with RLS on (MH-395): the
triggers fire on the real writes (made through the app pool, as the API makes
them), the stage only moves forward on its own, a hand-set stage is recorded
with who set it, and a changed profile re-ranks contacts with their reasons.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/icp/ -v
*/

func testApp(t *testing.T) *app.App {
	t.Helper()
	owner, appURL := testdb.Setup(t, "icp", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "icp-test-secret-icp-test-secret-icp-test-0001")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("SCHEDULER", "off")
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	k := togo.New()
	a := app.New(context.Background(), k)
	t.Cleanup(func() { k.Close() })
	return a
}

// ws is one workspace, written through the APP pool with its tenant context,
// so every trigger runs exactly as it does under a request.
type ws struct {
	t   *testing.T
	db  *sql.DB
	ctx context.Context
	id  string
	u   string
}

var seq int

func newWS(t *testing.T, a *app.App) ws {
	t.Helper()
	seq++
	suffix := strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "-") + "-" + string(rune('a'+seq%26))
	user := "u-icp-" + suffix
	if _, err := a.SysDB.Exec(`INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', 'now')`, user, "icp-"+suffix+"@example.test"); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := a.SysDB.QueryRow(`INSERT INTO workspaces (name, slug, created_by) VALUES ('ICP', $1, $2) RETURNING id`, "icp-"+suffix, user).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')`, id, user); err != nil {
		t.Fatal(err)
	}
	return ws{t: t, db: a.SQLDB, ctx: tenant.WithWorkspace(context.Background(), id), id: id, u: user}
}

func (w ws) one(q string, args ...any) string {
	w.t.Helper()
	var id string
	if err := w.db.QueryRowContext(w.ctx, q, append([]any{w.id}, args...)...).Scan(&id); err != nil {
		w.t.Fatalf("%v\n%s", err, q)
	}
	return id
}

func (w ws) exec(q string, args ...any) {
	w.t.Helper()
	if _, err := w.db.ExecContext(w.ctx, q, append([]any{w.id}, args...)...); err != nil {
		w.t.Fatalf("%v\n%s", err, q)
	}
}

func (w ws) stage(contact string) string {
	w.t.Helper()
	var s string
	if err := w.db.QueryRowContext(w.ctx, `SELECT lifecycle FROM contacts WHERE workspace_id = $1 AND id = $2`, w.id, contact).Scan(&s); err != nil {
		w.t.Fatal(err)
	}
	return s
}

func (w ws) stages() (open, won string) {
	open = w.one(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Lead', 0, 'open') RETURNING id`)
	won = w.one(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Won', 1, 'won') RETURNING id`)
	return
}

func TestLifecycleFollowsRealEvents(t *testing.T) {
	a := testApp(t)
	w := newWS(t, a)
	open, won := w.stages()
	c := w.one(`INSERT INTO contacts (workspace_id, email) VALUES ($1, 'hana@x.test') RETURNING id`)
	list := w.one(`INSERT INTO mail_lists (workspace_id, name) VALUES ($1, 'News') RETURNING id`)

	// A pending (unconfirmed) subscription is not a subscriber yet.
	sub := w.one(`INSERT INTO list_subscriptions (workspace_id, list_id, contact_id, status, token) VALUES ($1, $2, $3, 'pending', $4) RETURNING id`, list, c, "tok-"+c)
	if s := w.stage(c); s != "" {
		t.Fatalf("pending subscription moved the stage to %q", s)
	}
	w.exec(`UPDATE list_subscriptions SET status = 'subscribed' WHERE workspace_id = $1 AND id = $2`, sub)
	if s := w.stage(c); s != "subscriber" {
		t.Fatalf("confirmed: %q", s)
	}
	// A lead converted into the contact.
	w.exec(`INSERT INTO leads (workspace_id, email, message, status, contact_id, converted_at) VALUES ($1, 'hana@x.test', 'hi', 'converted', $2, now())`, c)
	if s := w.stage(c); s != "lead" {
		t.Fatalf("converted lead: %q", s)
	}
	// A deal opened with them, then won.
	d := w.one(`INSERT INTO deals (workspace_id, title, stage_id, contact_id, created_by) VALUES ($1, 'D', $2, $3, $4) RETURNING id`, open, c, w.u)
	if s := w.stage(c); s != "qualified" {
		t.Fatalf("deal opened: %q", s)
	}
	w.exec(`UPDATE deals SET stage_id = $3 WHERE workspace_id = $1 AND id = $2`, d, won)
	if s := w.stage(c); s != "customer" {
		t.Fatalf("deal won: %q", s)
	}
	// Events only move forward: a customer who subscribes again stays a customer.
	list2 := w.one(`INSERT INTO mail_lists (workspace_id, name) VALUES ($1, 'Club') RETURNING id`)
	w.exec(`INSERT INTO list_subscriptions (workspace_id, list_id, contact_id, status, token) VALUES ($1, $2, $3, 'subscribed', $4)`, list2, c, "tok2-"+c)
	if s := w.stage(c); s != "customer" {
		t.Fatalf("a later subscription demoted a customer to %q", s)
	}
	// A member sets it by hand, backwards, and is recorded.
	if err := SetLifecycle(w.ctx, w.db, w.id, c, w.u, "lead"); err != nil {
		t.Fatal(err)
	}
	if err := SetLifecycle(w.ctx, w.db, w.id, c, w.u, "lead"); err != nil { // same stage: nothing recorded
		t.Fatal(err)
	}
	if err := SetLifecycle(w.ctx, w.db, w.id, c, w.u, "vip"); err != ErrBadStage {
		t.Fatalf("an unknown stage: %v", err)
	}
	hist, total, err := LifecycleHistory(w.ctx, w.db, w.id, c)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for i := len(hist) - 1; i >= 0; i-- {
		got = append(got, hist[i].From+">"+hist[i].To+":"+hist[i].Source)
	}
	want := ">subscriber:subscribed,subscriber>lead:lead_converted,lead>qualified:deal_opened,qualified>customer:deal_won,customer>lead:manual"
	if total != 5 || strings.Join(got, ",") != want {
		t.Fatalf("history (%d):\n got %s\nwant %s", total, strings.Join(got, ","), want)
	}
	if hist[0].ActorUserID == nil || *hist[0].ActorUserID != w.u || hist[1].ActorUserID != nil {
		t.Errorf("the manual change is the member's, the automatic ones nobody's: %+v %+v", hist[0].ActorUserID, hist[1].ActorUserID)
	}
	// Another workspace reads none of this history through RLS.
	other := newWS(t, a)
	var n int
	if err := other.db.QueryRowContext(other.ctx, `SELECT count(*) FROM contact_lifecycle_events`).Scan(&n); err != nil || n != 0 {
		t.Errorf("another workspace reads %d lifecycle events (%v)", n, err)
	}
	if err := SetLifecycle(other.ctx, other.db, other.id, c, other.u, "customer"); err != ErrNoContact {
		t.Errorf("setting another workspace's contact: %v", err)
	}
}

func score(t *testing.T, w ws, table, id string) (*int, []Reason) {
	t.Helper()
	var s sql.NullInt64
	var raw string
	if err := w.db.QueryRowContext(w.ctx, `SELECT score, reasons::text FROM icp_scores WHERE workspace_id = $1 AND `+subject(table)+` = $2`, w.id, id).Scan(&s, &raw); err != nil {
		t.Fatal(err)
	}
	var rs []Reason
	_ = json.Unmarshal([]byte(raw), &rs)
	if !s.Valid {
		return nil, rs
	}
	v := int(s.Int64)
	return &v, rs
}

// subject is the icp_scores column that names a row of table.
func subject(table string) string {
	if table == "companies" {
		return "company_id"
	}
	return "contact_id"
}

func stale(t *testing.T, w ws, table, id string) bool {
	t.Helper()
	var b bool
	if err := w.db.QueryRowContext(w.ctx, `SELECT stale FROM icp_scores WHERE workspace_id = $1 AND `+subject(table)+` = $2`, w.id, id).Scan(&b); err != nil {
		t.Fatal(err)
	}
	return b
}

// Changing the profile's weights re-ranks contacts, with the reasons; a change
// to a fact marks exactly the scores that rest on it stale.
func TestProfileReRanksAndFactsMarkStale(t *testing.T) {
	a := testApp(t)
	w := newWS(t, a)
	open, _ := w.stages()
	hotel := w.one(`INSERT INTO companies (workspace_id, name, industry, size, country) VALUES ($1, 'Nile Hotel', 'Hospitality', '201-1000', 'Egypt') RETURNING id`)
	soft := w.one(`INSERT INTO companies (workspace_id, name, industry) VALUES ($1, 'Soft Co', 'Software') RETURNING id`)
	fit := w.one(`INSERT INTO contacts (workspace_id, email, title, company_id) VALUES ($1, 'fit@x.test', 'F&B Director', $2) RETURNING id`, hotel)
	busy := w.one(`INSERT INTO contacts (workspace_id, email, title, company_id) VALUES ($1, 'busy@x.test', 'CTO', $2) RETURNING id`, soft)
	w.one(`INSERT INTO deals (workspace_id, title, stage_id, contact_id, company_id, created_by) VALUES ($1, 'D', $2, $3, $4, $5) RETURNING id`, open, busy, soft, w.u)

	// Industry 2, open deal 3: busy (deal, no fit) 3/5 = 60 beats fit 2/5 = 40.
	p1, err := Profile{Industries: Criterion{Values: []string{"Hospitality"}, Weight: 2}, Signals: map[string]float64{SigOpenDeal: 3}}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveProfile(w.ctx, w.db, w.id, w.u, p1); err != nil {
		t.Fatal(err)
	}
	sf, rf := score(t, w, "contacts", fit)
	sb, _ := score(t, w, "contacts", busy)
	if sf == nil || sb == nil || *sf != 40 || *sb != 60 {
		t.Fatalf("deal-heavy: fit %v busy %v", sf, sb)
	}
	if len(rf) != 2 || rf[0].Dimension != DimIndustry || rf[0].Contribution != 2 || !strings.Contains(rf[0].Text, "Hospitality") {
		t.Errorf("fit's reasons: %+v", rf)
	}
	// The same weight on industry as three open deals: the fit contact passes.
	p2 := p1
	p2.Industries.Weight = 6
	if err := SaveProfile(w.ctx, w.db, w.id, w.u, p2); err != nil {
		t.Fatal(err)
	}
	sf, _ = score(t, w, "contacts", fit)
	sb, _ = score(t, w, "contacts", busy)
	if *sf != 67 || *sb != 33 {
		t.Fatalf("industry-heavy: fit %d busy %d", *sf, *sb)
	}
	// The companies are scored too: the hotel fits, Soft Co has the deal.
	sh, _ := score(t, w, "companies", hotel)
	ss, _ := score(t, w, "companies", soft)
	if *sh != 67 || *ss != 33 {
		t.Errorf("companies: hotel %d soft %d", *sh, *ss)
	}

	// Nothing stale after a rescore; renaming Soft Co's industry marks it and its
	// people, and nobody else.
	if stale(t, w, "contacts", fit) || stale(t, w, "contacts", busy) {
		t.Fatal("stale right after a rescore")
	}
	w.exec(`UPDATE companies SET industry = 'Hospitality tech' WHERE workspace_id = $1 AND id = $2`, soft)
	if !stale(t, w, "contacts", busy) || !stale(t, w, "companies", soft) || stale(t, w, "contacts", fit) {
		t.Fatal("a company change should mark its people and itself, only")
	}
	if err := Rescore(w.ctx, w.db, w.id); err != nil {
		t.Fatal(err)
	}
	if sb, _ = score(t, w, "contacts", busy); *sb != 100 {
		t.Errorf("busy after its company became a (substring) fit: %d", *sb)
	}
	// A new activity marks its contact; a score is not rewritten by its own rescore.
	w.exec(`INSERT INTO activities (workspace_id, kind, body, contact_id, created_by) VALUES ($1, 'note', 'hi', $2, $3)`, fit, w.u)
	if !stale(t, w, "contacts", fit) {
		t.Error("an activity did not mark its contact stale")
	}
	if err := Rescore(w.ctx, w.db, w.id); err != nil {
		t.Fatal(err)
	}
	if stale(t, w, "contacts", fit) {
		t.Error("the rescore marked its own write stale")
	}

	// The other workspace's rows are neither read nor written by this one's rescore.
	other := newWS(t, a)
	oc := other.one(`INSERT INTO contacts (workspace_id, email) VALUES ($1, 'o@x.test') RETURNING id`)
	if err := Rescore(w.ctx, w.db, w.id); err != nil {
		t.Fatal(err)
	}
	if !stale(t, other, "contacts", oc) {
		t.Error("one workspace's rescore touched another's contact")
	}
	// The saved profile is the workspace's own.
	sp, err := LoadProfile(other.ctx, other.db, other.id)
	if err != nil || sp.Configured || len(sp.Profile.Industries.Values) != 0 {
		t.Errorf("another workspace sees this profile: %+v %v", sp, err)
	}
}

// A rescore writes only icp_scores: the contact and company rows are not
// touched (updated_at unchanged), so the audit log never records a rescore as
// an edit of a contact (MH-392).
func TestRescoreDoesNotTouchContacts(t *testing.T) {
	a := testApp(t)
	w := newWS(t, a)
	c := w.one(`INSERT INTO contacts (workspace_id, email, title) VALUES ($1, 'x@x.test', 'Owner') RETURNING id`)
	var before, after string
	if err := w.db.QueryRowContext(w.ctx, `SELECT updated_at::text FROM contacts WHERE workspace_id = $1 AND id = $2`, w.id, c).Scan(&before); err != nil {
		t.Fatal(err)
	}
	p, _ := Profile{Keywords: Criterion{Values: []string{"owner"}, Weight: 2}}.Normalize()
	if err := SaveProfile(w.ctx, w.db, w.id, w.u, p); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(w.ctx, `SELECT updated_at::text FROM contacts WHERE workspace_id = $1 AND id = $2`, w.id, c).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if s, _ := score(t, w, "contacts", c); s == nil || *s != 50 || before != after {
		t.Errorf("score %v; contact updated_at %s -> %s", s, before, after)
	}
}
