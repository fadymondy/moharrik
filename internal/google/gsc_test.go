package google_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/google/googletest"
	"github.com/fadymondy/moharrik/internal/searchconsole"
)

/*
MH-445: the Search Console daily import and the page's answer, against the
fake Search Analytics API.
*/

type perfOut struct {
	Resource    *google.Resource           `json:"resource"`
	Performance *searchconsole.Performance `json:"performance"`
}

func (c *client) perf(t *testing.T, query string) perfOut {
	t.Helper()
	code, b, _ := c.do(t, "GET", "/api/w/search-console/performance"+query, nil)
	if code != 200 {
		t.Fatalf("performance%s: %d %s", query, code, b)
	}
	var out perfOut
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func pacificDay(offset int) string {
	pt, _ := time.LoadLocation("America/Los_Angeles")
	n := time.Now().In(pt)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, offset).Format("2006-01-02")
}

func TestSearchConsole(t *testing.T) {
	f := newFixture(t)
	t.Setenv("GOOGLE_SYNC_INLINE", "")
	ctx := context.Background()

	alice := f.newUser(t)
	wsID, _ := alice.workspace(t)

	t.Run("nothing enabled: said, not an error", func(t *testing.T) {
		if out := alice.perf(t, ""); out.Resource != nil || out.Performance != nil {
			t.Fatalf("%+v", out)
		}
	})

	f.setAccount("sub-gsc", "gsc@example.test")
	if r, _ := alice.connect(t, "product=search_console"); r != google.ResultConnected {
		t.Fatal(r)
	}
	google.WaitBackground() // discovery → auto-enable → the first import
	site := alice.resources(t, "?kind=gsc_site").Resources[0]

	t.Run("the first import walks back 90 days", func(t *testing.T) {
		var days, detail, totals int
		_ = f.a.SysDB.QueryRow(`SELECT count(*) FROM gsc_import_days WHERE resource_id = $1`, site.ID).Scan(&days)
		_ = f.a.SysDB.QueryRow(`SELECT count(*) FROM gsc_search_analytics_daily WHERE resource_id = $1`, site.ID).Scan(&detail)
		_ = f.a.SysDB.QueryRow(`SELECT count(*) FROM gsc_search_totals_daily WHERE resource_id = $1`, site.ID).Scan(&totals)
		if days != searchconsole.Backfill || detail == 0 || totals == 0 {
			t.Fatalf("days %d detail %d totals %d", days, detail, totals)
		}
		if site.LastSyncedAt == nil || site.LastError != "" {
			t.Errorf("%+v", site)
		}
	})

	t.Run("28 days: ends on the newest day with data, totals are the site's own", func(t *testing.T) {
		p := alice.perf(t, "?days=28").Performance
		if p == nil || p.NothingYet || p.Empty || p.Basis != "site" {
			t.Fatalf("%+v", p)
		}
		if p.EndDate != pacificDay(-2) || len(p.Daily) != 28 || p.Daily[0].Date != p.StartDate {
			t.Fatalf("window %s..%s (%d days), want the end %s", p.StartDate, p.EndDate, len(p.Daily), pacificDay(-2))
		}
		var clicks, impr int64
		for _, d := range p.Daily {
			if d.Clicks == nil || d.Impressions == nil {
				t.Fatalf("an imported day is never unknown: %+v", d)
			}
			clicks += *d.Clicks
			impr += *d.Impressions
		}
		if clicks != p.Totals.Clicks || impr != p.Totals.Impressions {
			t.Errorf("the trend adds up to the totals: %d/%d vs %+v", clicks, impr, p.Totals)
		}
		var qClicks, qImpr int64
		for _, q := range p.TopQueries {
			qClicks += q.Clicks
			qImpr += q.Impressions
		}
		if qImpr >= p.Totals.Impressions {
			t.Errorf("query rows leave anonymised queries out, so they add up to less (%d vs %d)", qImpr, p.Totals.Impressions)
		}
		if p.Totals.Position == nil || *p.Totals.Position < 1 || *p.Totals.Position > 50 || p.Totals.CTR <= 0 || p.Totals.CTR >= 1 {
			t.Errorf("totals %+v", p.Totals)
		}
		if len(p.TopQueries) == 0 || len(p.TopPages) == 0 || !strings.HasPrefix(p.TopPages[0].Key, "https://demo.example/") {
			t.Errorf("top %+v %+v", p.TopQueries, p.TopPages)
		}
		if len(p.Countries) != 5 || len(p.Devices) != 3 {
			t.Errorf("filter options %v %v", p.Countries, p.Devices)
		}
	})

	t.Run("compare: the same length just before", func(t *testing.T) {
		p := alice.perf(t, "?days=7&compare=true").Performance
		end, _ := time.Parse("2006-01-02", p.StartDate)
		if p.Previous == nil || p.PrevEndDate != end.AddDate(0, 0, -1).Format("2006-01-02") || len(p.PreviousDaily) != 7 {
			t.Fatalf("%+v %s..%s", p.Previous, p.PrevStartDate, p.PrevEndDate)
		}
		if p.Previous.Impressions == 0 || p.Missing != 0 || p.PreviousMissing != 0 {
			t.Errorf("the previous week had data in the fake: %+v missing %d/%d", p.Previous, p.Missing, p.PreviousMissing)
		}
		// A day of the previous week not imported (yet) is unknown, and said.
		_, _ = f.a.SysDB.Exec(`DELETE FROM gsc_import_days WHERE resource_id = $1 AND date = $2`, site.ID, p.PrevStartDate)
		_, _ = f.a.SysDB.Exec(`DELETE FROM gsc_search_totals_daily WHERE resource_id = $1 AND date = $2`, site.ID, p.PrevStartDate)
		q := alice.perf(t, "?days=7&compare=true").Performance
		if q.PreviousMissing != 1 || q.PreviousDaily[0].Clicks != nil {
			t.Errorf("a missing day: %d %+v", q.PreviousMissing, q.PreviousDaily[0])
		}
		if err := google.RunSync(ctx, f.a, wsID, site.ID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("filters", func(t *testing.T) {
		all := alice.perf(t, "?days=28").Performance
		q := alice.perf(t, "?days=28&query=PRICING").Performance
		if q.Basis != "queries" || q.Totals.Clicks >= all.Totals.Clicks || len(q.TopQueries) == 0 {
			t.Fatalf("query filter: %+v", q.Totals)
		}
		for _, r := range q.TopQueries {
			if !strings.Contains(strings.ToLower(r.Key), "pricing") {
				t.Errorf("%q does not match the filter", r.Key)
			}
		}
		eg := alice.perf(t, "?days=28&country=egy&device=MOBILE").Performance
		if eg.Basis != "site" || eg.Totals.Impressions == 0 || eg.Totals.Impressions >= all.Totals.Impressions {
			t.Errorf("country+device: %+v", eg.Totals)
		}
		if code, _, _ := alice.do(t, "GET", "/api/w/search-console/performance?device=WATCH", nil); code != 422 {
			t.Errorf("a bad device: %d", code)
		}
		// A LIKE wildcard in the filter is a literal.
		if w := alice.perf(t, "?days=28&query=%25").Performance; w.Totals.Clicks != 0 {
			t.Errorf("%% matched everything: %+v", w.Totals)
		}
	})

	t.Run("a re-run imports only the days Google may still change, without double counting", func(t *testing.T) {
		before := alice.perf(t, "?days=90").Performance.Totals
		calls := f.fake.Calls("api:searchAnalytics")
		if err := google.RunSync(ctx, f.a, wsID, site.ID); err != nil {
			t.Fatal(err)
		}
		n := f.fake.Calls("api:searchAnalytics") - calls
		if n == 0 || n > 20 {
			t.Errorf("a re-run made %d calls; it should re-import only the last few days", n)
		}
		if after := alice.perf(t, "?days=90").Performance.Totals; after.Clicks != before.Clicks || after.Impressions != before.Impressions {
			t.Errorf("re-import changed the totals: %+v → %+v", before, after)
		}
	})

	t.Run("query rows are paged, and a day beyond the bound says truncated", func(t *testing.T) {
		searchconsole.RowLimit, searchconsole.MaxDetailPages = 40, 2
		defer func() { searchconsole.RowLimit, searchconsole.MaxDetailPages = 25000, 2 }()
		_, _ = f.a.SysDB.Exec(`DELETE FROM gsc_import_days WHERE resource_id = $1 AND date = $2`, site.ID, pacificDay(-3))
		if err := google.RunSync(ctx, f.a, wsID, site.ID); err != nil {
			t.Fatal(err)
		}
		var rows int
		var truncated bool
		if err := f.a.SysDB.QueryRow(`SELECT detail_rows, truncated FROM gsc_import_days WHERE resource_id = $1 AND date = $2`,
			site.ID, pacificDay(-3)).Scan(&rows, &truncated); err != nil {
			t.Fatal(err)
		}
		if rows != 80 || !truncated {
			t.Errorf("two pages of 40 and truncated: %d %v", rows, truncated)
		}
		if p := alice.perf(t, "?days=7").Performance; p.Coverage.Truncated == 0 {
			t.Error("the page is told a day was truncated")
		}
	})

	t.Run("a site with no data: imported days read as zero, not unknown", func(t *testing.T) {
		bob := f.newUser(t)
		bob.workspace(t)
		f.fake.SetData("sub-gsc-empty", &googletest.AccountData{Sites: []googletest.GSCSite{{URL: "https://new.example/", Permission: "siteOwner", Empty: true}}})
		f.setAccount("sub-gsc-empty", "gsc-empty@example.test")
		if r, _ := bob.connect(t, "product=search_console"); r != google.ResultConnected {
			t.Fatal(r)
		}
		google.WaitBackground()
		p := bob.perf(t, "?days=28").Performance
		if p == nil || p.NothingYet || !p.Empty || p.Totals.Clicks != 0 || p.Totals.Position != nil || len(p.TopQueries) != 0 {
			t.Fatalf("%+v", p)
		}
		for _, d := range p.Daily {
			if d.Clicks == nil || *d.Clicks != 0 {
				t.Fatalf("%+v", d)
			}
		}
	})

	t.Run("an import that fails leaves nothing half-done and says why", func(t *testing.T) {
		carol := f.newUser(t)
		carol.workspace(t)
		f.setAccount("sub-gsc-fail", "gsc-fail@example.test")
		f.fake.Fail("searchAnalytics", http.StatusForbidden, "forbidden", -1)
		defer f.fake.ClearFailures()
		if r, _ := carol.connect(t, "product=search_console"); r != google.ResultConnected {
			t.Fatal(r)
		}
		google.WaitBackground()
		out := carol.perf(t, "?days=28")
		if out.Performance == nil || !out.Performance.NothingYet || !strings.Contains(out.Resource.LastError, "refused access") {
			t.Fatalf("%+v %+v", out.Performance, out.Resource)
		}
	})

	t.Run("roles and isolation", func(t *testing.T) {
		m := f.newUser(t)
		addMember(t, f, wsID, m.email, "member")
		m.ws = wsID
		if out := m.perf(t, "?days=28"); out.Performance == nil || out.Performance.Totals.Clicks == 0 {
			t.Errorf("member: %+v", out)
		}
		eve := f.newUser(t)
		eve.workspace(t)
		if code, b, _ := eve.do(t, "GET", "/api/w/search-console/performance?resource="+site.ID, nil); code != 404 || strings.Contains(string(b), site.ExternalID) {
			t.Errorf("another workspace's site: %d %s", code, b)
		}
	})

	t.Run("the Overview's Traffic card shows the site's clicks", func(t *testing.T) {
		_, b, _ := alice.do(t, "GET", "/api/w/overview", nil)
		var ov struct {
			Traffic *struct {
				Sites []struct {
					Clicks  *int64  `json:"clicks"`
					EndDate *string `json:"end_date"`
				} `json:"sites"`
			} `json:"traffic"`
		}
		_ = json.Unmarshal(b, &ov)
		p := alice.perf(t, "?days=28").Performance
		if ov.Traffic == nil || len(ov.Traffic.Sites) != 1 || ov.Traffic.Sites[0].Clicks == nil ||
			*ov.Traffic.Sites[0].Clicks != p.Totals.Clicks || *ov.Traffic.Sites[0].EndDate != p.EndDate {
			t.Fatalf("overview %s vs page %+v", b, p.Totals)
		}
	})
}
