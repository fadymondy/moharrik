package prospects

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/db/migrate"
	"github.com/fadymondy/moharrik/internal/icp"
	"github.com/fadymondy/moharrik/internal/research"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
)

// setICP sets the workspace's CRM ICP industries/regions/keywords (MH-514):
// prospect discovery reads its "who" from here now, not from
// ProspectProfileInput.
func setICP(t *testing.T, f *fixture, industries, regions, keywords []string) {
	t.Helper()
	p := icp.Default()
	p.Industries.Values, p.Regions.Values, p.Keywords.Values = industries, regions, keywords
	n, err := p.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if err := icp.SaveProfile(f.ctx, f.a.SQLDB, f.ws, f.user, n); err != nil {
		t.Fatal(err)
	}
}

/*
Prospect discovery against a real database (MH-397): a workspace with a
profile gets prospects from a scheduled run, with evidence that points at
the source; accept adds company + contact + deal; reject changes later
scoring; a draft is produced and nothing is sent.
*/

func testApp(t *testing.T) *app.App {
	t.Helper()
	owner, appURL := testdb.Setup(t, "prospects", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "prospects-test-secret-prospects-test-secret-0123456")
	t.Setenv("STORAGE_DIR", t.TempDir())
	k := togo.New()
	a := app.New(context.Background(), k)
	t.Cleanup(func() { k.Close() })
	return a
}

type fixture struct {
	a      *app.App
	ws     string
	user   string
	engine *research.Engine
	ctx    context.Context
}

// The fake web: every query answers with the same three articles.
var pages = map[string][2]string{
	"https://news.test/zeal": {"Zeal raises $10m | News", `# Egyptian fintech startup Zeal raises $10m for payments expansion

Egyptian fintech startup Zeal has raised US$10 million in funding to grow its payments business in Egypt.
Omar Ebeid, co-founder and CEO of Zeal, said the money will fund expansion across the region.
Media enquiries: press@zeal.test`},
	"https://news.test/bokra": {"Bokra raises pre-seed | News", `# Egyptian fintech startup Bokra raises pre-seed funding

Egyptian fintech startup Bokra raised $4.6 million in a pre-seed funding round for its payments savings app in Egypt.`},
	"https://news.test/mylo": {"mylo is hiring | News", `# mylo is hiring engineers in Cairo

Mylo, a fintech payments platform in Egypt, is hiring backend engineers in Cairo to grow its team.`},
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	a := testApp(t)
	f := &fixture{a: a}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	f.user = "u-prospects-" + suffix
	bg := context.Background()
	if _, err := a.SysDB.ExecContext(bg, `INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', now()::text)`, f.user, f.user+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRowContext(bg, `INSERT INTO workspaces (name, slug, created_by, locale, currency) VALUES ('Prospect Test', $1, $2, 'en', 'USD') RETURNING id`,
		"prospects-"+suffix, f.user).Scan(&f.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.ExecContext(bg, `INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')`, f.ws, f.user); err != nil {
		t.Fatal(err)
	}
	f.ctx = tenant.With(bg, &tenant.Scope{WorkspaceID: f.ws, UserID: f.user, Role: "owner", Via: "session"})

	sends := 0
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type res struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
		}
		out := struct {
			Results []res `json:"results"`
		}{}
		for u, p := range pages {
			lines := strings.Split(p[1], "\n")
			out.Results = append(out.Results, res{u, p[0], lines[2]})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(search.Close)
	reader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			URL string `json:"url"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]string{"markdown": pages[in.URL][1]}})
	}))
	t.Cleanup(reader.Close)
	_ = sends
	f.engine = &research.Engine{
		Search: &research.Searcher{BaseURL: search.URL, Client: search.Client()},
		Read:   &research.Reader{BaseURL: reader.URL, Client: reader.Client()},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	Init(f.engine)
	return f
}

func (f *fixture) settle(t *testing.T, runID string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		_ = f.a.SysDB.QueryRow(`SELECT status FROM research_runs WHERE id = $1`, runID).Scan(&status)
		if status != "queued" && status != "running" {
			// The hook runs after the status is written, and posts its digest
			// last: wait for that.
			for i := 0; i < 100; i++ {
				var n int
				_ = f.a.SysDB.QueryRow(`SELECT count(*) FROM notification_items WHERE workspace_id = $1 AND external_id = $2`, f.ws, "prospects:"+runID).Scan(&n)
				if n > 0 {
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the discovery run did not settle")
}

func TestScheduledDiscoveryScoresReviewsAndDrafts(t *testing.T) {
	f := newFixture(t)
	on, industries, regions, offer := true, []string{"fintech"}, []string{"Egypt"}, "We build payment integrations for fintechs."
	setICP(t, f, industries, regions, nil)
	if _, err := SaveProfile(f.ctx, f.a.SQLDB, f.ws, f.user, ProspectProfileInput{Offer: &offer, Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	if _, err := research.SetBudget(f.ctx, f.a.SQLDB, f.ws, 1); err != nil {
		t.Fatal(err)
	}

	// The clock: one pass, spending the budget.
	RunDue(context.Background(), f.a, f.engine, 20)
	p, _ := GetProfile(f.ctx, f.a.SQLDB, f.ws)
	if p.LastStatus != "started" || p.LastRunID == "" || !p.NextRunAt.After(time.Now().Add(23*time.Hour)) {
		t.Fatalf("scheduled discovery: %+v", p)
	}
	f.settle(t, p.LastRunID)
	var origin string
	_ = f.a.SysDB.QueryRow(`SELECT origin FROM research_runs WHERE id = $1`, p.LastRunID).Scan(&origin)
	if origin != "discovery" {
		t.Errorf("origin %q", origin)
	}

	list, counts, err := List(f.ctx, f.a.SQLDB, f.ws, Query{})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Prospect{}
	for _, x := range list {
		byName[x.Name] = x
	}
	zeal, ok := byName["Zeal"]
	if !ok || counts["new"] < 2 {
		t.Fatalf("prospects: %+v", list)
	}
	if zeal.Score <= 0 || len(zeal.Reasons) == 0 || zeal.Contact.Name != "Omar Ebeid" || zeal.Contact.Email != "" {
		// zeal.test is not a domain research knows for Zeal, so the address is not taken.
		t.Errorf("Zeal: %+v", zeal)
	}
	// Nothing reached the CRM on its own.
	var crm int
	_ = f.a.SysDB.QueryRow(`SELECT count(*) FROM companies WHERE workspace_id = $1`, f.ws).Scan(&crm)
	if crm != 0 {
		t.Fatalf("discovery added %d CRM companies", crm)
	}
	// Evidence that opens the source, on a timeline.
	d, err := Get(f.ctx, f.a.SQLDB, f.ws, zeal.ID)
	if err != nil || len(d.Timeline) == 0 || !strings.HasPrefix(d.Timeline[0].SourceURL, "https://news.test/") {
		t.Fatalf("timeline: %v %+v", err, d)
	}
	// The digest was announced.
	var items int
	_ = f.a.SysDB.QueryRow(`SELECT count(*) FROM notification_items WHERE workspace_id = $1 AND kind = 'prospects'`, f.ws).Scan(&items)
	if items != 1 {
		t.Errorf("%d digest notifications", items)
	}
	dg, _ := TakeDigest(f.ctx, f.a.SQLDB, f.ws, true)
	if len(dg.Prospects) < 2 {
		t.Errorf("digest: %+v", dg)
	}
	if dg2, _ := TakeDigest(f.ctx, f.a.SQLDB, f.ws, false); len(dg2.Prospects) != 0 {
		t.Errorf("the digest mark did not move: %+v", dg2)
	}

	// The budget holds: the second pass today is refused, and says so.
	_, _ = f.a.SysDB.Exec(`UPDATE prospect_profiles SET next_run_at = now() WHERE workspace_id = $1`, f.ws)
	RunDue(context.Background(), f.a, f.engine, 20)
	p, _ = GetProfile(f.ctx, f.a.SQLDB, f.ws)
	if p.LastStatus != "over_budget" {
		t.Errorf("second pass past the budget: %+v", p)
	}

	// Reject Bokra (funding): Zeal (funding) drops, with the reason.
	bokra := byName["Bokra"]
	if bokra.ID == "" {
		t.Fatalf("no Bokra: %+v", list)
	}
	if _, err := Reject(f.ctx, f.a.SQLDB, f.ws, f.user, bokra.ID, "too early for us"); err != nil {
		t.Fatal(err)
	}
	after, _ := get(f.ctx, f.a.SQLDB, f.ws, zeal.ID)
	if after.Score >= zeal.Score {
		t.Errorf("rejecting a similar prospect did not lower Zeal: %v -> %v", zeal.Score, after.Score)
	}
	found := false
	for _, r := range after.Reasons {
		if r.Dimension == "feedback_down" && r.Count == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("Zeal's reasons do not say why it dropped: %+v", after.Reasons)
	}
	var sugStatus string
	_ = f.a.SysDB.QueryRow(`SELECT status FROM research_companies WHERE id = $1`, bokra.SuggestionID).Scan(&sugStatus)
	if sugStatus != "dismissed" {
		t.Errorf("a rejected prospect's suggestion is %q", sugStatus)
	}

	// Draft, en and ar: a template (no model), stored, not sent.
	for _, loc := range []string{"en", "ar"} {
		dr, err := MakeDraft(f.ctx, f.a.SQLDB, nil, f.ws, f.user, zeal.ID, loc)
		if err != nil {
			t.Fatal(err)
		}
		if dr.Generator != "template" || dr.Locale != loc || !strings.Contains(dr.Body, "Zeal") || len(dr.EvidenceIDs) == 0 {
			t.Errorf("%s draft: %+v", loc, dr)
		}
		if loc == "en" && (!strings.Contains(dr.Body, "Hi Omar") || !strings.Contains(dr.Body, offer)) {
			t.Errorf("en draft does not use the named person or the offer: %q", dr.Body)
		}
		if loc == "ar" && !strings.Contains(dr.Body, "مرحباً") {
			t.Errorf("ar draft: %q", dr.Body)
		}
	}

	// Accept Zeal: company, contact (address given) and deal.
	res, err := Accept(f.ctx, f.a.SQLDB, f.ws, f.user, zeal.ID, ProspectAcceptInput{ContactEmail: "omar@zeal.test"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.CompanyCreated || res.ContactID == "" || res.DealID == "" || res.Prospect.Status != "accepted" {
		t.Fatalf("accept: %+v", res)
	}
	var contactName, contactCo, dealCo, dealTitle string
	_ = f.a.SysDB.QueryRow(`SELECT name, coalesce(company_id, '') FROM contacts WHERE id = $1`, res.ContactID).Scan(&contactName, &contactCo)
	_ = f.a.SysDB.QueryRow(`SELECT coalesce(company_id, ''), title FROM deals WHERE id = $1`, res.DealID).Scan(&dealCo, &dealTitle)
	if contactName != "Omar Ebeid" || contactCo != res.CompanyID || dealCo != res.CompanyID || !strings.Contains(dealTitle, "Zeal") {
		t.Errorf("CRM rows: contact %q/%q deal %q/%q", contactName, contactCo, dealCo, dealTitle)
	}
	if _, err := Accept(f.ctx, f.a.SQLDB, f.ws, f.user, zeal.ID, ProspectAcceptInput{}); err != ErrDecided {
		t.Errorf("accepting twice: %v", err)
	}
	// A client cannot accept.
	mylo := byName["Mylo"]
	if mylo.ID == "" {
		mylo = byName["mylo"]
	}
	client := tenant.With(context.Background(), &tenant.Scope{WorkspaceID: f.ws, UserID: f.user, Role: "client", Via: "session"})
	if mylo.ID != "" {
		if _, err := Accept(client, f.a.SQLDB, f.ws, f.user, mylo.ID, ProspectAcceptInput{}); err == nil {
			t.Error("a client accepted a prospect")
		}
	}
}

func TestDiscoveryNeedsAProfileAndSearch(t *testing.T) {
	f := newFixture(t)
	if _, _, err := Discover(f.ctx, f.a, f.engine, f.ws, f.user, false); err != ErrNotConfigured {
		t.Errorf("an empty profile: %v", err)
	}
	on := true
	if _, err := SaveProfile(f.ctx, f.a.SQLDB, f.ws, f.user, ProspectProfileInput{Enabled: &on}); err == nil {
		t.Error("discovery turned on with nothing to look for")
	}
	setICP(t, f, nil, nil, []string{"fintech"})
	noSearch := &research.Engine{Read: f.engine.Read, Log: f.engine.Log}
	if _, _, err := Discover(f.ctx, f.a, noSearch, f.ws, f.user, false); err != research.ErrNoSearch {
		t.Errorf("no search configured: %v", err)
	}
}

// TestBackfillSeedsICPFromLegacyProspectProfile is the MH-514 migration's own
// job: a workspace stuck in the pre-514 shape — industries/regions/keywords
// written on prospect_profiles, no icp_profiles row at all — gets an
// icp_profiles row built from those columns the next time the migration
// runs, so it keeps finding what it already found. internal/db/post/514_
// prospect_icp_link.sql only backfills WHERE NOT EXISTS, so this simulates
// the pre-514 state on an already-migrated fixture database and re-runs
// migrate.Run, which is idempotent and safe to call twice.
func TestBackfillSeedsICPFromLegacyProspectProfile(t *testing.T) {
	f := newFixture(t)
	// A prospect_profiles row must exist before it can be updated — GetProfile
	// creates one lazily (ensureProfile), like a workspace's first visit to
	// the discovery page would.
	if _, err := GetProfile(f.ctx, f.a.SQLDB, f.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.SysDB.Exec(`UPDATE prospect_profiles SET industries = '{fintech,retail}', regions = '{Egypt}', keywords = '{payments}' WHERE workspace_id = $1`, f.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.SysDB.Exec(`DELETE FROM icp_profiles WHERE workspace_id = $1`, f.ws); err != nil {
		t.Fatal(err)
	}

	appURL := os.Getenv("APP_DATABASE_URL")
	if appURL == "" {
		t.Fatal("APP_DATABASE_URL not set by testApp")
	}
	if err := migrate.Run(context.Background(), f.a.SysDB, appURL, "../..", func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}

	sp, err := icp.LoadProfile(f.ctx, f.a.SQLDB, f.ws)
	if err != nil {
		t.Fatal(err)
	}
	if !sp.Configured {
		t.Fatal("backfill: expected an icp_profiles row to now exist for the workspace")
	}
	if got := sp.Profile.Industries.Values; !equalSet(got, []string{"fintech", "retail"}) {
		t.Errorf("industries: %v", got)
	}
	if got := sp.Profile.Regions.Values; !equalSet(got, []string{"Egypt"}) {
		t.Errorf("regions: %v", got)
	}
	if got := sp.Profile.Keywords.Values; !equalSet(got, []string{"payments"}) {
		t.Errorf("keywords: %v", got)
	}

	// Re-running the migration again must not clobber the ICP a person might
	// have since customized: the backfill only fires WHERE NOT EXISTS.
	custom := icp.Default()
	custom.Industries.Values = []string{"logistics"}
	customN, err := custom.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if err := icp.SaveProfile(f.ctx, f.a.SQLDB, f.ws, f.user, customN); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Run(context.Background(), f.a.SysDB, appURL, "../..", func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	sp2, err := icp.LoadProfile(f.ctx, f.a.SQLDB, f.ws)
	if err != nil {
		t.Fatal(err)
	}
	if !equalSet(sp2.Profile.Industries.Values, []string{"logistics"}) {
		t.Errorf("a second migration run overwrote the customized ICP: %v", sp2.Profile.Industries.Values)
	}
}

// TestEditingICPChangesDiscoverysView is the other half of MH-514: discovery
// no longer keeps its own copy of the who, it reads the CRM ICP live, so
// changing the ICP on its own settings page — never touching
// prospect_profiles — changes what GetProfile reports and what discovery
// would search and score against.
func TestEditingICPChangesDiscoverysView(t *testing.T) {
	f := newFixture(t)
	before, err := GetProfile(f.ctx, f.a.SQLDB, f.ws)
	if err != nil {
		t.Fatal(err)
	}
	if before.ICPConfigured || len(before.Industries) != 0 {
		t.Fatalf("a fresh workspace should read the ICP default: %+v", before)
	}

	setICP(t, f, []string{"logistics", "retail"}, []string{"Egypt", "UAE"}, []string{"fleet", "warehouse"})

	after, err := GetProfile(f.ctx, f.a.SQLDB, f.ws)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ICPConfigured {
		t.Error("discovery did not see the ICP as configured")
	}
	if !equalSet(after.Industries, []string{"logistics", "retail"}) {
		t.Errorf("industries: %v", after.Industries)
	}
	if !equalSet(after.Regions, []string{"Egypt", "UAE"}) {
		t.Errorf("regions: %v", after.Regions)
	}
	if !equalSet(after.Keywords, []string{"fleet", "warehouse"}) {
		t.Errorf("keywords: %v", after.Keywords)
	}
	if !after.Configured {
		t.Error("discovery should now consider itself configured (industries/keywords came from the ICP)")
	}
	found := false
	for _, q := range after.EffectiveQueries {
		if strings.Contains(q, "logistics") || strings.Contains(q, "retail") {
			found = true
		}
	}
	if !found {
		t.Errorf("effective queries did not pick up the new ICP industries: %v", after.EffectiveQueries)
	}

	// The change did not touch prospect_profiles' own (dead) columns.
	var industriesText string
	if err := f.a.SysDB.QueryRow(`SELECT industries::text FROM prospect_profiles WHERE workspace_id = $1`, f.ws).Scan(&industriesText); err != nil {
		t.Fatal(err)
	}
	if industries := pgList(industriesText); len(industries) != 0 {
		t.Errorf("SaveProfile via the ICP must not write prospect_profiles.industries, got %v", industries)
	}
}

// equalSet compares two string slices ignoring order.
func equalSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ma := map[string]int{}
	for _, s := range a {
		ma[s]++
	}
	for _, s := range b {
		ma[s]--
	}
	for _, n := range ma {
		if n != 0 {
			return false
		}
	}
	return true
}
