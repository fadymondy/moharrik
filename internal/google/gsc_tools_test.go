package google_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/searchconsole"
)

/*
MH-446: URL inspection against the per-site 2,000-a-day ledger, and sitemaps
listed and submitted — against the fake Search Console.
*/

type inspectionsOut struct {
	Inspections []searchconsole.Inspection    `json:"inspections"`
	Quota       searchconsole.InspectionQuota `json:"quota"`
	CanWrite    bool                          `json:"can_write"`
}

type sitemapsOut struct {
	Sitemaps []searchconsole.Sitemap `json:"sitemaps"`
	Error    string                  `json:"error"`
	CanWrite bool                    `json:"can_write"`
}

func TestSearchConsoleTools(t *testing.T) {
	f := newFixture(t)
	t.Setenv("GOOGLE_SYNC_INLINE", "off") // no import: this is about the tools
	t.Setenv("GSC_INSPECTIONS_PER_DAY", "")

	// The ledger is per Pacific day and survives between runs of this suite on
	// the same database: start from an empty day.
	if _, err := f.a.SysDB.Exec(`DELETE FROM gsc_inspection_quota`); err != nil {
		t.Fatal(err)
	}
	alice := f.newUser(t)
	wsID, _ := alice.workspace(t)
	f.setAccount("sub-tools", "tools@example.test")
	if r, _ := alice.connect(t, "product=search_console"); r != google.ResultConnected {
		t.Fatal(r)
	}
	alice.resources(t, "") // discovery on load → the one site, auto-enabled

	inspect := func(c *client, u string) (int, searchconsole.Inspection, string) {
		code, b, _ := c.do(t, "POST", "/api/w/search-console/inspect", map[string]string{"url": u})
		var in searchconsole.Inspection
		_ = json.Unmarshal(b, &in)
		return code, in, string(b)
	}

	t.Run("an indexed page, a page Google does not know, and a URL outside the site", func(t *testing.T) {
		code, in, b := inspect(alice, "https://demo.example/pricing")
		if code != 200 || in.Verdict != "PASS" || in.CoverageState != "Submitted and indexed" || in.LastCrawlTime == nil || in.ResultLink == "" {
			t.Fatalf("%d %s", code, b)
		}
		code, in, b = inspect(alice, "https://blog.demo.example/new-post")
		if code != 200 || in.Verdict != "NEUTRAL" || in.CoverageState != "URL is unknown to Google" || in.LastCrawlTime != nil {
			t.Fatalf("a domain property covers its subdomains; an unknown page is NEUTRAL: %d %s", code, b)
		}
		calls := f.fake.Calls("api:inspect")
		if code, _, b := inspect(alice, "https://elsewhere.test/pricing"); code != 422 || !strings.Contains(b, "not part of this Search Console site") {
			t.Errorf("outside the property: %d %s", code, b)
		}
		if code, _, _ := inspect(alice, "javascript:alert(1)"); code != 422 {
			t.Errorf("not a URL: %d", code)
		}
		if f.fake.Calls("api:inspect") != calls {
			t.Error("a URL outside the site must never reach Google")
		}
		out := getJSON[inspectionsOut](t, alice, "/api/w/search-console/inspections")
		if len(out.Inspections) != 2 || out.Quota.Used != 2 || out.Quota.Limit != 2000 || !out.CanWrite {
			t.Errorf("%+v", out)
		}
		// Inspecting again updates the row rather than adding one.
		inspect(alice, "https://demo.example/pricing")
		if out := getJSON[inspectionsOut](t, alice, "/api/w/search-console/inspections"); len(out.Inspections) != 2 || out.Inspections[0].URL != "https://demo.example/pricing" {
			t.Errorf("%+v", out.Inspections)
		}
	})

	t.Run("the budget is per site, across workspaces, and refuses before calling", func(t *testing.T) {
		bob := f.newUser(t)
		bob.workspace(t)
		if r, _ := bob.connect(t, "product=search_console"); r != google.ResultConnected { // the same Google account and site
			t.Fatal(r)
		}
		bob.resources(t, "")
		q := getJSON[inspectionsOut](t, bob, "/api/w/search-console/inspections").Quota
		if q.Used != 3 {
			t.Errorf("bob's workspace sees the site's budget alice spent: %+v", q)
		}
		if n := len(getJSON[inspectionsOut](t, bob, "/api/w/search-console/inspections").Inspections); n != 0 {
			t.Errorf("but never alice's inspections: %d", n)
		}
		t.Setenv("GSC_INSPECTIONS_PER_DAY", strconv.Itoa(q.Used))
		calls := f.fake.Calls("api:inspect")
		if code, _, b := inspect(bob, "https://demo.example/about"); code != 429 || !strings.Contains(b, "used up") {
			t.Errorf("cap: %d %s", code, b)
		}
		if f.fake.Calls("api:inspect") != calls {
			t.Error("a refused inspection must not reach Google")
		}
		t.Setenv("GSC_INSPECTIONS_PER_DAY", "")
	})

	t.Run("sitemaps: listed as Google has them, submitted, and only under the site", func(t *testing.T) {
		out := getJSON[sitemapsOut](t, alice, "/api/w/search-console/sitemaps")
		if out.Error != "" || len(out.Sitemaps) != 1 || out.Sitemaps[0].Submitted == nil || *out.Sitemaps[0].Submitted != 42 || !out.CanWrite {
			t.Fatalf("%+v", out)
		}
		code, b, _ := alice.do(t, "POST", "/api/w/search-console/sitemaps", map[string]string{"url": "https://demo.example/sitemap-blog.xml"})
		var after sitemapsOut
		_ = json.Unmarshal(b, &after)
		if code != 200 || len(after.Sitemaps) != 2 {
			t.Fatalf("%d %s", code, b)
		}
		var added *searchconsole.Sitemap
		for i := range after.Sitemaps {
			if after.Sitemaps[i].Path == "https://demo.example/sitemap-blog.xml" {
				added = &after.Sitemaps[i]
			}
		}
		if added == nil || !added.IsPending || added.Submitted != nil {
			t.Errorf("a new sitemap is pending and has no counts yet: %+v", added)
		}
		if code, _, _ := alice.do(t, "POST", "/api/w/search-console/sitemaps", map[string]string{"url": "https://elsewhere.test/sitemap.xml"}); code != 422 {
			t.Errorf("a sitemap outside the site: %d", code)
		}
		f.fake.Fail("sitemapSubmit", 403, "forbidden", 1)
		if code, b, _ := alice.do(t, "POST", "/api/w/search-console/sitemaps", map[string]string{"url": "https://demo.example/sitemap-x.xml"}); code != 424 || !strings.Contains(string(b), "refused access") {
			t.Errorf("Google refusing: %d %s", code, b)
		}
	})

	t.Run("a member reads, a manager acts", func(t *testing.T) {
		m := f.newUser(t)
		addMember(t, f, wsID, m.email, "member")
		m.ws = wsID
		if out := getJSON[inspectionsOut](t, m, "/api/w/search-console/inspections"); out.CanWrite || len(out.Inspections) != 2 {
			t.Errorf("%+v", out)
		}
		if code, _, _ := inspect(m, "https://demo.example/"); code != 403 {
			t.Errorf("member inspect: %d", code)
		}
		if code, _, _ := m.do(t, "POST", "/api/w/search-console/sitemaps", map[string]string{"url": "https://demo.example/s.xml"}); code != 403 {
			t.Errorf("member submit: %d", code)
		}
		setRole(t, f, wsID, m.email, "manager")
		if code, _, b := inspect(m, "https://demo.example/"); code != 200 {
			t.Errorf("manager inspect: %d %s", code, b)
		}
	})
}
