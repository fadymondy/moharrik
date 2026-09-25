package leads

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
)

/*
Converting a lead, against a real database with RLS on (MH-390).

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/leads/ -v
*/

func testApp(t *testing.T) *app.App {
	t.Helper()
	owner, appURL := testdb.Setup(t, "leads", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "leads-test-secret-leads-test-secret-leads-01")
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

type fixture struct {
	a      *app.App
	wsID   string
	userID string
	form   marketing.PublicForm
	ctx    context.Context
}

var seq int

// workspace makes a workspace with a user in it and one inquiry form.
func workspace(t *testing.T, a *app.App, locale, currency string) fixture {
	t.Helper()
	ctx := context.Background()
	seq++
	suffix := time.Now().Format("150405.000000") + "-" + string(rune('a'+seq%26))
	f := fixture{a: a}
	f.userID = "u-" + suffix
	if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', 'now')`,
		f.userID, "owner-"+suffix+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRowContext(ctx, `
		INSERT INTO workspaces (name, slug, created_by, locale, currency) VALUES ('Leads Test', $1, $2, $3, $4) RETURNING id`,
		"leads-"+strings.ReplaceAll(suffix, ".", "-"), f.userID, locale, currency).Scan(&f.wsID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')`,
		f.wsID, f.userID); err != nil {
		t.Fatal(err)
	}
	f.form = marketing.PublicForm{WorkspaceID: f.wsID, Name: "Contact page"}
	if err := a.SysDB.QueryRowContext(ctx, `
		INSERT INTO subscribe_forms (workspace_id, kind, name, public_key, allowed_origins) VALUES ($1, 'inquiry', 'Contact page', $2, 'https://shop.test')
		RETURNING id`, f.wsID, "if_test_"+suffix).Scan(&f.form.ID); err != nil {
		t.Fatal(err)
	}
	f.ctx = tenant.WithWorkspace(ctx, f.wsID)
	return f
}

func (f fixture) lead(t *testing.T, b inquiryBody) string {
	t.Helper()
	id, err := intake(f.ctx, f.a.SQLDB, f.form, b, "https://shop.test")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f fixture) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.a.SQLDB.QueryRowContext(f.ctx, q, append([]any{f.wsID}, args...)...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestConvertMakesContactCompanyDealAndFirstActivity(t *testing.T) {
	a := testApp(t)
	f := workspace(t, a, "en", "EUR")
	id := f.lead(t, inquiryBody{Name: "Rana Ali", Email: "Rana@Acme-Labs.io", Phone: "+20 100", Message: "We need a CRM for 40 people.",
		UtmSource: "google", UtmMedium: "cpc", UtmCampaign: "spring", Gclid: "g-1", Referrer: "https://www.google.com/", LandingPage: "https://shop.test/?utm_source=google"})

	// Backdate the lead, so "the activity is dated when the visitor wrote" is
	// something the test can tell apart from "now".
	sent := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	if _, err := a.SQLDB.ExecContext(f.ctx, `UPDATE leads SET created_at = $3 WHERE workspace_id = $1 AND id = $2`, f.wsID, id, sent); err != nil {
		t.Fatal(err)
	}

	c, err := Convert(f.ctx, a.SQLDB, f.wsID, id, f.userID)
	if err != nil {
		t.Fatal(err)
	}
	if !c.ContactCreated || c.ContactID == "" {
		t.Fatalf("a new address must make a contact: %+v", c)
	}
	if c.CompanyID == "" || !c.CompanyCreated || c.FreeMail {
		t.Fatalf("a business address must make a company: %+v", c)
	}
	if c.Lead.Status != StatusConverted || c.Lead.ConvertedContact == nil || c.Lead.ConvertedCompany == nil || c.Lead.ConvertedDeal == nil {
		t.Fatalf("the lead must be converted and link to what it became: %+v", c.Lead)
	}
	if c.Lead.ConvertedCompany.Name != "Acme Labs" {
		t.Errorf("company named %q, want the domain's readable form \"Acme Labs\"", c.Lead.ConvertedCompany.Name)
	}

	var email, name, phone, companyID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT email, name, phone, coalesce(company_id, '') FROM contacts WHERE workspace_id = $1 AND id = $2`,
		f.wsID, c.ContactID).Scan(&email, &name, &phone, &companyID); err != nil {
		t.Fatal(err)
	}
	if email != "rana@acme-labs.io" || name != "Rana Ali" || phone != "+20 100" || companyID != c.CompanyID {
		t.Errorf("contact = %q %q %q company=%q", email, name, phone, companyID)
	}
	var domain string
	_ = a.SQLDB.QueryRowContext(f.ctx, `SELECT domain FROM companies WHERE workspace_id = $1 AND id = $2`, f.wsID, c.CompanyID).Scan(&domain)
	if domain != "acme-labs.io" {
		t.Errorf("company domain = %q", domain)
	}

	// The deal: first open stage, the workspace's currency, owned by the converter.
	var stageKind, currency, owner, contactID string
	var position int64
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT st.kind, st.position, d.currency, d.owner_user_id, d.contact_id FROM deals d
		JOIN pipeline_stages st ON st.workspace_id = d.workspace_id AND st.id = d.stage_id
		WHERE d.workspace_id = $1 AND d.id = $2`, f.wsID, c.DealID).Scan(&stageKind, &position, &currency, &owner, &contactID); err != nil {
		t.Fatal(err)
	}
	var firstOpen int64
	_ = a.SQLDB.QueryRowContext(f.ctx, `SELECT min(position) FROM pipeline_stages WHERE workspace_id = $1 AND kind = 'open'`, f.wsID).Scan(&firstOpen)
	if stageKind != "open" || position != firstOpen || currency != "EUR" || owner != f.userID || contactID != c.ContactID {
		t.Errorf("deal: stage=%s pos=%d (first open %d) currency=%s owner=%s contact=%s", stageKind, position, firstOpen, currency, owner, contactID)
	}

	// The message is the first thing on the deal's timeline, dated when it was sent.
	var body string
	var at time.Time
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT body, created_at FROM activities WHERE workspace_id = $1 AND deal_id = $2 ORDER BY created_at LIMIT 1`,
		f.wsID, c.DealID).Scan(&body, &at); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "We need a CRM for 40 people.") {
		t.Errorf("first activity = %q, want the visitor's message", body)
	}
	if !at.Equal(sent) {
		t.Errorf("first activity dated %v, want the lead's %v", at, sent)
	}

	// The attribution survives on the lead.
	l, err := Get(f.ctx, a.SQLDB, f.wsID, id)
	if err != nil {
		t.Fatal(err)
	}
	if l.Attribution.UtmSource != "google" || l.Attribution.UtmCampaign != "spring" || l.Attribution.Gclid != "g-1" ||
		l.Attribution.Referrer != "https://www.google.com/" || l.Origin != "https://shop.test" || l.FormName != "Contact page" {
		t.Errorf("attribution lost: %+v origin=%q form=%q", l.Attribution, l.Origin, l.FormName)
	}

	// Converting twice makes no second deal.
	if _, err := Convert(f.ctx, a.SQLDB, f.wsID, id, f.userID); !errors.Is(err, ErrAlreadyConverted) {
		t.Fatalf("second convert: %v, want ErrAlreadyConverted", err)
	}
	if n := f.count(t, `SELECT count(*) FROM deals WHERE workspace_id = $1`); n != 1 {
		t.Fatalf("%d deals after converting twice, want 1", n)
	}
}

func TestConvertReusesAnExistingContactWithoutOverwritingIt(t *testing.T) {
	a := testApp(t)
	f := workspace(t, a, "en", "USD")
	var existing string
	if err := a.SQLDB.QueryRowContext(f.ctx, `INSERT INTO contacts (workspace_id, email, name) VALUES ($1, 'sam@globex.test', 'Samuel (typed by hand)') RETURNING id`,
		f.wsID).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	id := f.lead(t, inquiryBody{Name: "sam", Email: "SAM@globex.test", Message: "Pricing?"})
	c, err := Convert(f.ctx, a.SQLDB, f.wsID, id, f.userID)
	if err != nil {
		t.Fatal(err)
	}
	if c.ContactCreated || c.ContactID != existing {
		t.Fatalf("the existing contact must be reused: created=%v id=%s want %s", c.ContactCreated, c.ContactID, existing)
	}
	if n := f.count(t, `SELECT count(*) FROM contacts WHERE workspace_id = $1 AND email = 'sam@globex.test'`); n != 1 {
		t.Fatalf("%d contacts for one address, want 1", n)
	}
	var name string
	_ = a.SQLDB.QueryRowContext(f.ctx, `SELECT name FROM contacts WHERE workspace_id = $1 AND id = $2`, f.wsID, existing).Scan(&name)
	if name != "Samuel (typed by hand)" {
		t.Errorf("a form overwrote a name somebody typed: %q", name)
	}
}

func TestConvertFreeMailMakesNoCompany(t *testing.T) {
	a := testApp(t)
	f := workspace(t, a, "en", "USD")
	for _, addr := range []string{"someone@gmail.com", "x@outlook.sa", "y@yahoo.co.uk", "z@proton.me", "w@mail.ru"} {
		id := f.lead(t, inquiryBody{Email: addr, Company: "Typed Company", Message: "hi"})
		c, err := Convert(f.ctx, a.SQLDB, f.wsID, id, f.userID)
		if err != nil {
			t.Fatal(err)
		}
		if c.CompanyID != "" || !c.FreeMail || c.Lead.ConvertedCompany != nil {
			t.Errorf("%s made a company: %+v", addr, c)
		}
		if c.DealID == "" {
			t.Errorf("%s: a free-mail lead still gets a deal", addr)
		}
	}
	if n := f.count(t, `SELECT count(*) FROM companies WHERE workspace_id = $1`); n != 0 {
		t.Fatalf("%d companies from free-mail leads, want 0", n)
	}
}

func TestConvertTwoLeadsFromOneDomainShareTheCompany(t *testing.T) {
	a := testApp(t)
	f := workspace(t, a, "ar", "SAR")
	c1, err := Convert(f.ctx, a.SQLDB, f.wsID, f.lead(t, inquiryBody{Email: "a@initech.test", Company: "Initech", Message: "1"}), f.userID)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Convert(f.ctx, a.SQLDB, f.wsID, f.lead(t, inquiryBody{Email: "b@initech.test", Message: "2"}), f.userID)
	if err != nil {
		t.Fatal(err)
	}
	if c1.CompanyID == "" || c1.CompanyID != c2.CompanyID || c2.CompanyCreated {
		t.Fatalf("one domain must be one company: %s / %s (second created=%v)", c1.CompanyID, c2.CompanyID, c2.CompanyCreated)
	}
	// The Arabic workspace's deal is titled in Arabic.
	if c2.Lead.ConvertedDeal == nil || !strings.Contains(c2.Lead.ConvertedDeal.Name, "استفسار") {
		t.Errorf("deal title in an Arabic workspace: %+v", c2.Lead.ConvertedDeal)
	}
}

func TestStatusRules(t *testing.T) {
	a := testApp(t)
	f := workspace(t, a, "en", "USD")
	id := f.lead(t, inquiryBody{Email: "q@q.test", Message: "?"})
	if _, err := SetStatus(f.ctx, a.SQLDB, f.wsID, id, StatusConverted); !errors.Is(err, ErrStatus) {
		t.Fatalf("setting converted directly: %v", err)
	}
	if l, err := SetStatus(f.ctx, a.SQLDB, f.wsID, id, StatusSpam); err != nil || l.Status != StatusSpam {
		t.Fatalf("spam: %v %+v", err, l)
	}
	if _, err := Convert(f.ctx, a.SQLDB, f.wsID, id, f.userID); !errors.Is(err, ErrSpam) {
		t.Fatalf("converting spam: %v, want ErrSpam", err)
	}
	if _, err := SetStatus(f.ctx, a.SQLDB, f.wsID, id, StatusQualified); err != nil {
		t.Fatal(err)
	}
	if _, err := Convert(f.ctx, a.SQLDB, f.wsID, id, f.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := SetStatus(f.ctx, a.SQLDB, f.wsID, id, StatusNew); !errors.Is(err, ErrStatus) {
		t.Fatalf("a converted lead changed status: %v", err)
	}

	page, err := List(f.ctx, a.SQLDB, f.wsID, Filter{Status: StatusConverted})
	if err != nil || len(page.Leads) != 1 || page.Counts[StatusConverted] != 1 || page.Counts[StatusNew] != 0 {
		t.Fatalf("list: %v %d %v", err, len(page.Leads), page.Counts)
	}
	if page, _ := List(f.ctx, a.SQLDB, f.wsID, Filter{Q: "Q@Q"}); len(page.Leads) != 1 {
		t.Fatalf("search by email found %d", len(page.Leads))
	}
	if page, _ := List(f.ctx, a.SQLDB, f.wsID, Filter{Q: "100%_nothing"}); len(page.Leads) != 0 {
		t.Fatalf("LIKE wildcards in the query must be literal, found %d", len(page.Leads))
	}
}

func TestLeadsDoNotCrossWorkspaces(t *testing.T) {
	a := testApp(t)
	fa := workspace(t, a, "en", "USD")
	fb := workspace(t, a, "en", "USD")
	idA := fa.lead(t, inquiryBody{Email: "secret@a.test", Message: "A only"})

	// B's context, B's id, A's lead: not found, by predicate AND by RLS.
	if _, err := Get(fb.ctx, a.SQLDB, fb.wsID, idA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("B read A's lead: %v", err)
	}
	if _, err := SetStatus(fb.ctx, a.SQLDB, fb.wsID, idA, StatusSpam); !errors.Is(err, ErrNotFound) {
		t.Fatalf("B changed A's lead: %v", err)
	}
	if _, err := Convert(fb.ctx, a.SQLDB, fb.wsID, idA, fb.userID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("B converted A's lead: %v", err)
	}
	// Even naming A's workspace while scoped to B: RLS returns nothing.
	if _, err := Get(fb.ctx, a.SQLDB, fa.wsID, idA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RLS let B's connection read A's lead: %v", err)
	}
	if page, err := List(fb.ctx, a.SQLDB, fb.wsID, Filter{}); err != nil || len(page.Leads) != 0 {
		t.Fatalf("B's list shows %d leads: %v", len(page.Leads), err)
	}
	// And the app pool refuses to write a lead into another workspace.
	_, err := intake(fb.ctx, a.SQLDB, fa.form, inquiryBody{Email: "x@x.test", Message: "cross"}, "")
	if err == nil {
		t.Fatal("RLS let a lead be written into another workspace")
	}
	var status string
	if err := a.SQLDB.QueryRowContext(fa.ctx, `SELECT status FROM leads WHERE workspace_id = $1 AND id = $2`, fa.wsID, idA).Scan(&status); err != nil || status != StatusNew {
		t.Fatalf("A's lead after B's attempts: %v %q", err, status)
	}
}

// MH-464: a budget that reads as one amount in the workspace's currency becomes the deal's value;
// anything else leaves the deal without one (the board says "No value yet") and keeps the text.
func TestConvertTurnsTheBudgetIntoTheDealValue(t *testing.T) {
	a := testApp(t)
	f := workspace(t, a, "en", "USD")
	value := func(budget string) (int64, string) {
		t.Helper()
		id := f.lead(t, inquiryBody{Email: "buyer-" + budget + "@initrode.test", Message: "Quote please", Budget: budget})
		c, err := Convert(f.ctx, a.SQLDB, f.wsID, id, f.userID)
		if err != nil {
			t.Fatal(err)
		}
		var v int64
		if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT value_minor FROM deals WHERE workspace_id = $1 AND id = $2`, f.wsID, c.DealID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		var body string
		if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT body FROM activities WHERE workspace_id = $1 AND id = $2`, f.wsID, c.ActivityID).Scan(&body); err != nil {
			t.Fatal(err)
		}
		if c.Lead.Budget != budget {
			t.Errorf("lead budget %q, want %q as typed", c.Lead.Budget, budget)
		}
		return v, body
	}
	if v, body := value("$12,500"); v != 1250000 || !strings.Contains(body, "Budget: $12,500") {
		t.Errorf("$12,500 → value %d, timeline %q; want 1250000 and the budget on the timeline", v, body)
	}
	if v, _ := value("10k-20k"); v != 0 {
		t.Errorf("a range must not become a value, got %d", v)
	}
	if v, _ := value("EGP 50000"); v != 0 {
		t.Errorf("a foreign-currency budget must not become a USD value, got %d", v)
	}
	if v, body := value(""); v != 0 || strings.Contains(body, "Budget") {
		t.Errorf("no budget → value %d, timeline %q", v, body)
	}
}
