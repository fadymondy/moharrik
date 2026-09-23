package google_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/analytics"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/google/googletest"
)

/*
MH-444: the Analytics page's GA4 report against the fake Data API — the
nightly snapshot, the hour cache, 429 backoff, a property with no data, and
what a member, a client and another workspace see.
*/

type reportOut struct {
	Resource *google.Resource  `json:"resource"`
	Report   *analytics.Report `json:"report"`
	Error    string            `json:"error"`
}

func (c *client) report(t *testing.T, query string) reportOut {
	t.Helper()
	code, b, _ := c.do(t, "GET", "/api/w/analytics/report"+query, nil)
	if code != 200 {
		t.Fatalf("report%s: %d %s", query, code, b)
	}
	var out reportOut
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGA4Report(t *testing.T) {
	f := newFixture(t)
	t.Setenv("GOOGLE_SYNC_INLINE", "")

	alice := f.newUser(t)
	wsID, _ := alice.workspace(t)

	t.Run("no property enabled: said, not an error", func(t *testing.T) {
		out := alice.report(t, "")
		if out.Resource != nil || out.Report != nil || out.Error != "" {
			t.Fatalf("%+v", out)
		}
	})

	f.setAccount("sub-ga", "ga@example.test")
	if r, _ := alice.connect(t, "product=analytics"); r != google.ResultConnected {
		t.Fatal(r)
	}
	google.WaitBackground() // discovery → auto-enable → the first sync

	t.Run("the connect's first sync stored the 28-day snapshot", func(t *testing.T) {
		var n int
		var users int64
		if err := f.a.SysDB.QueryRow(`SELECT count(*), coalesce(max(users), 0) FROM ga4_reports WHERE workspace_id = $1 AND days = 28`, wsID).Scan(&n, &users); err != nil {
			t.Fatal(err)
		}
		if n != 1 || users <= 0 {
			t.Fatalf("snapshot rows %d users %d", n, users)
		}
		res := alice.resources(t, "?kind=ga4_property").Resources
		if len(res) != 1 || res[0].LastSyncedAt == nil || res[0].LastError != "" {
			t.Fatalf("%+v", res)
		}
		if res[0].NextSyncAt.Before(time.Now().Add(50 * time.Minute)) {
			t.Errorf("the next sync is nightly, not now: %v", res[0].NextSyncAt)
		}
		var days int
		_ = f.a.SysDB.QueryRow(`SELECT count(*) FROM ga4_daily WHERE workspace_id = $1`, wsID).Scan(&days)
		if days == 0 {
			t.Error("the daily history was not kept")
		}
	})

	t.Run("28 days: from the snapshot, shaped honestly", func(t *testing.T) {
		before := f.fake.Calls("api:runReport")
		out := alice.report(t, "?days=28")
		if f.fake.Calls("api:runReport") != before {
			t.Error("a fresh snapshot must be served without asking GA4")
		}
		rep := out.Report
		if rep == nil || rep.Empty || rep.Stale || out.Error != "" {
			t.Fatalf("%+v", out)
		}
		if len(rep.Daily) != 28 || rep.Daily[27].Date != rep.EndDate || rep.Daily[0].Date != rep.StartDate {
			t.Fatalf("daily %d %s..%s (%s..%s)", len(rep.Daily), rep.Daily[0].Date, rep.Daily[len(rep.Daily)-1].Date, rep.StartDate, rep.EndDate)
		}
		loc, _ := time.LoadLocation(googletest.FakeGA4TimeZone)
		yesterday := time.Now().In(loc).AddDate(0, 0, -1).Format("2006-01-02")
		if rep.EndDate != yesterday || rep.TimeZone != googletest.FakeGA4TimeZone {
			t.Errorf("window ends %s in %s, want %s", rep.EndDate, rep.TimeZone, yesterday)
		}
		var sumUsers, sumSessions int64
		quiet := 0
		for _, d := range rep.Daily {
			sumUsers += d.Users
			sumSessions += d.Sessions
			if d.Users == 0 {
				quiet++
			}
		}
		if rep.Totals.Users <= 0 || rep.Totals.Users >= sumUsers {
			t.Errorf("window users must be GA4's own total (%d), below the sum of daily users (%d)", rep.Totals.Users, sumUsers)
		}
		if rep.Totals.Sessions != sumSessions {
			t.Errorf("sessions add up across days: %d vs %d", rep.Totals.Sessions, sumSessions)
		}
		if rep.Totals.EngagementRate <= 0 || rep.Totals.EngagementRate >= 1 || rep.Totals.AvgEngagementPerUse <= 0 {
			t.Errorf("engagement: %+v", rep.Totals)
		}
		if len(rep.TopPages) != 10 || rep.TopPages[0].Path != "/" || rep.TopPages[0].Views < rep.TopPages[1].Views {
			t.Errorf("top pages: %+v", rep.TopPages)
		}
		if len(rep.Sources) == 0 || rep.Sources[0].Source != "google" || rep.Sources[0].Medium != "organic" {
			t.Errorf("sources: %+v", rep.Sources)
		}
		_ = quiet
	})

	t.Run("7 days: fetched once, then cached for the hour; refresh asks again", func(t *testing.T) {
		before := f.fake.Calls("api:runReport")
		a := alice.report(t, "?days=7")
		if f.fake.Calls("api:runReport") != before+1 || a.Report == nil || len(a.Report.Daily) != 7 {
			t.Fatalf("first read: calls %d→%d %+v", before, f.fake.Calls("api:runReport"), a)
		}
		alice.report(t, "?days=7")
		if f.fake.Calls("api:runReport") != before+1 {
			t.Error("the second read within the hour asked GA4 again")
		}
		alice.report(t, "?days=7&refresh=true")
		if f.fake.Calls("api:runReport") != before+2 {
			t.Error("refresh did not ask GA4")
		}
		if code, _, _ := alice.do(t, "GET", "/api/w/analytics/report?days=30", nil); code != 422 {
			t.Errorf("days=30: %d", code)
		}
	})

	t.Run("429: retried, then the stored answer is served stale and GA4 is left alone", func(t *testing.T) {
		f.fake.Fail("runReport", http.StatusTooManyRequests, "RESOURCE_EXHAUSTED", -1)
		defer f.fake.ClearFailures()
		before := f.fake.Calls("api:runReport")
		out := alice.report(t, "?days=28&refresh=true")
		if n := f.fake.Calls("api:runReport") - before; n != 3 {
			t.Errorf("429 should be tried 3 times in all, was %d", n)
		}
		if out.Report == nil || !out.Report.Stale || !strings.Contains(out.Report.Note, "rate limiting") {
			t.Fatalf("stale answer: %+v", out.Report)
		}
		if out.Resource.BackoffUntil == nil || out.Resource.LastError == "" {
			t.Errorf("the resource records the backoff: %+v", out.Resource)
		}
		before = f.fake.Calls("api:runReport")
		again := alice.report(t, "?days=28&refresh=true")
		if f.fake.Calls("api:runReport") != before {
			t.Error("during a backoff, a refresh must not call GA4")
		}
		if again.Report == nil || !again.Report.Stale {
			t.Errorf("%+v", again.Report)
		}
		// Nothing stored for 90 days: no numbers, an error that says why.
		none := alice.report(t, "?days=90")
		if none.Report != nil || !strings.Contains(none.Error, "rate limiting") {
			t.Errorf("no cache + backoff: %+v", none)
		}
		_, _ = f.a.SysDB.Exec(`UPDATE google_resources SET backoff_until = NULL WHERE workspace_id = $1`, wsID)
	})

	t.Run("a property with no data yet says so", func(t *testing.T) {
		bob := f.newUser(t)
		bob.workspace(t)
		f.fake.SetData("sub-empty-ga", &googletest.AccountData{GA4: []googletest.GA4Property{
			{ID: "properties/555", Name: "New", Account: "accounts/5", AccountName: "New Co", Empty: true}}})
		f.setAccount("sub-empty-ga", "empty-ga@example.test")
		if r, _ := bob.connect(t, "product=analytics"); r != google.ResultConnected {
			t.Fatal(r)
		}
		google.WaitBackground()
		out := bob.report(t, "?days=28")
		if out.Report == nil || !out.Report.Empty || out.Report.Totals.Users != 0 || len(out.Report.Daily) != 28 || len(out.Report.TopPages) != 0 {
			t.Fatalf("%+v", out.Report)
		}
		for _, d := range out.Report.Daily {
			if d.Users != 0 || d.Views != 0 {
				t.Fatalf("an empty property has invented numbers: %+v", d)
			}
		}
	})

	t.Run("a property the account cannot read is a clear error", func(t *testing.T) {
		carol := f.newUser(t)
		carol.workspace(t)
		f.fake.SetData("sub-lost", &googletest.AccountData{GA4: []googletest.GA4Property{{ID: "properties/777", Name: "Lost", Account: "accounts/7", AccountName: "L"}}})
		f.setAccount("sub-lost", "lost@example.test")
		t.Setenv("GOOGLE_SYNC_INLINE", "off")
		if r, _ := carol.connect(t, "product=analytics"); r != google.ResultConnected {
			t.Fatal(r)
		}
		carol.resources(t, "") // discovery on load → auto-enabled, no sync (inline off)
		f.fake.Set(func(*googletest.Server) {})
		d := f.fake.Data("sub-lost")
		f.fake.Set(func(*googletest.Server) { d.GA4 = nil })
		out := carol.report(t, "?days=28")
		if out.Report != nil || !strings.Contains(out.Error, "refused access") {
			t.Fatalf("%+v", out)
		}
		t.Setenv("GOOGLE_SYNC_INLINE", "")
	})

	t.Run("roles: a member reads, a client cannot; another workspace's id is 404", func(t *testing.T) {
		m := f.newUser(t)
		addMember(t, f, wsID, m.email, "member")
		m.ws = wsID
		if out := m.report(t, "?days=28"); out.Report == nil || out.Resource == nil || out.Resource.ConnectionEmail != "" {
			t.Errorf("member: %+v", out)
		}
		c := f.newUser(t)
		addMember(t, f, wsID, c.email, "client")
		c.ws = wsID
		if code, _, _ := c.do(t, "GET", "/api/w/analytics/report", nil); code != 403 {
			t.Errorf("client: %d", code)
		}
		res := alice.resources(t, "?kind=ga4_property").Resources[0]
		eve := f.newUser(t)
		eve.workspace(t)
		if code, b, _ := eve.do(t, "GET", "/api/w/analytics/report?resource="+res.ID, nil); code != 404 || strings.Contains(string(b), res.ExternalID) {
			t.Errorf("another workspace's property: %d %s", code, b)
		}
	})

	t.Run("the Overview's Traffic card reads the snapshot", func(t *testing.T) {
		code, b, _ := alice.do(t, "GET", "/api/w/overview", nil)
		if code != 200 {
			t.Fatalf("%d %s", code, b)
		}
		var ov struct {
			Traffic *struct {
				Properties []struct {
					Name     string `json:"name"`
					Users    *int64 `json:"users"`
					Sessions *int64 `json:"sessions"`
				} `json:"properties"`
			} `json:"traffic"`
		}
		_ = json.Unmarshal(b, &ov)
		if ov.Traffic == nil || len(ov.Traffic.Properties) != 1 || ov.Traffic.Properties[0].Users == nil || *ov.Traffic.Properties[0].Users <= 0 {
			t.Fatalf("traffic: %s", b)
		}
		var users int64
		_ = f.a.SysDB.QueryRow(`SELECT users FROM ga4_reports WHERE workspace_id = $1 AND days = 28`, wsID).Scan(&users)
		if *ov.Traffic.Properties[0].Users != users {
			t.Errorf("overview %d vs snapshot %d", *ov.Traffic.Properties[0].Users, users)
		}
		// A workspace with nothing enabled has no Traffic card at all.
		dan := f.newUser(t)
		dan.workspace(t)
		_, b, _ = dan.do(t, "GET", "/api/w/overview", nil)
		if !strings.Contains(string(b), `"traffic":null`) {
			t.Errorf("no property → traffic null: %s", b)
		}
	})
}
