/*
Package analytics reads a workspace's GA4 properties (MH-444): totals, a daily
trend, top pages and source / medium for a 7, 28 or 90-day window.

	POST analyticsdata/v1beta/properties/{id}:batchRunReports
	     four reports in one call (one quota charge instead of four):
	       totals        activeUsers, newUsers, sessions, screenPageViews,
	                     engagedSessions, userEngagementDuration (no dimension)
	       daily         date → activeUsers, sessions, screenPageViews
	       top pages     pagePath → screenPageViews, activeUsers (top 10)
	       sources       sessionSource, sessionMedium → sessions, activeUsers (top 10)

The window is "{days}daysAgo" to "yesterday" in the PROPERTY's time zone (GA4
resolves them itself), so today's partial day never drags the trend down.

Honesty rules, because the owner reads these numbers as fact:
  - the window's users are GA4's own total, never the sum of daily users;
  - a day GA4 returns no row for is a day with no traffic, drawn as 0 — the
    window itself comes from GA4's answer, not guessed;
  - a property with no data yet answers Empty, and the page says so;
  - engagement time per user is userEngagementDuration / activeUsers, both
    from the same totals row.

Cache: ga4_reports per (property, window), fresh for an hour; after that a
read asks GA4 again, and when GA4 fails (or asked us to slow down) the stored
answer is served marked stale with the reason. The collector refreshes the
28-day window nightly: the snapshot the Overview's Traffic card reads.
*/
package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // property time zones on a server image without zoneinfo

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// Windows the page offers.
var Windows = []int{7, 28, 90}

// Fresh is how long a stored report is served without asking GA4.
const Fresh = time.Hour

// SnapshotDays is the window the nightly sync keeps warm (the Overview's).
const SnapshotDays = 28

// ValidDays reports whether days is a window the page offers.
func ValidDays(days int) bool {
	for _, w := range Windows {
		if w == days {
			return true
		}
	}
	return false
}

// Totals are the window's sums, as GA4 computed them.
type Totals struct {
	Users               int64   `json:"users" doc:"activeUsers over the whole window (GA4's own total, not a sum of days)."`
	NewUsers            int64   `json:"new_users"`
	Sessions            int64   `json:"sessions"`
	Views               int64   `json:"views" doc:"screenPageViews."`
	EngagedSessions     int64   `json:"engaged_sessions"`
	EngagementRate      float64 `json:"engagement_rate" doc:"engaged_sessions / sessions, 0..1; 0 when there were no sessions."`
	EngagementSeconds   int64   `json:"engagement_seconds" doc:"userEngagementDuration, seconds."`
	AvgEngagementPerUse float64 `json:"avg_engagement_seconds_per_user" doc:"engagement_seconds / users; 0 when there were no users."`
}

// Day is one day of the trend.
type Day struct {
	Date     string `json:"date" doc:"YYYY-MM-DD in the property's time zone."`
	Users    int64  `json:"users"`
	Sessions int64  `json:"sessions"`
	Views    int64  `json:"views"`
}

// Page is one of the most viewed paths.
type Page struct {
	Path  string `json:"path"`
	Views int64  `json:"views"`
	Users int64  `json:"users"`
}

// Source is one source / medium.
type Source struct {
	Source   string `json:"source"`
	Medium   string `json:"medium"`
	Sessions int64  `json:"sessions"`
	Users    int64  `json:"users"`
}

// Report is a window's answer for one property.
type Report struct {
	Days      int       `json:"days"`
	StartDate string    `json:"start_date" doc:"YYYY-MM-DD, property time zone."`
	EndDate   string    `json:"end_date" doc:"YYYY-MM-DD (yesterday), property time zone."`
	TimeZone  string    `json:"time_zone"`
	Empty     bool      `json:"empty" doc:"GA4 has no data for this property in this window (a new property, or no tag firing)."`
	Totals    Totals    `json:"totals"`
	Daily     []Day     `json:"daily"`
	TopPages  []Page    `json:"top_pages"`
	Sources   []Source  `json:"sources"`
	FetchedAt time.Time `json:"fetched_at"`
	// Stale: GA4 could not be asked just now (Note says why); this is the
	// last answer it gave.
	Stale bool   `json:"stale"`
	Note  string `json:"note,omitempty"`
}

// ---- GA4's answer ------------------------------------------------------------------

type gaReport struct {
	DimensionHeaders []struct {
		Name string `json:"name"`
	} `json:"dimensionHeaders"`
	MetricHeaders []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"metricHeaders"`
	Rows []struct {
		DimensionValues []struct {
			Value string `json:"value"`
		} `json:"dimensionValues"`
		MetricValues []struct {
			Value string `json:"value"`
		} `json:"metricValues"`
	} `json:"rows"`
	RowCount int `json:"rowCount"`
	Metadata struct {
		TimeZone string `json:"timeZone"`
	} `json:"metadata"`
}

// table reads a report into rows of name → value, looked up by header name so
// the order Google answers in never matters.
func (g gaReport) table() []map[string]string {
	out := []map[string]string{}
	for _, r := range g.Rows {
		row := map[string]string{}
		for i, h := range g.DimensionHeaders {
			if i < len(r.DimensionValues) {
				row[h.Name] = r.DimensionValues[i].Value
			}
		}
		for i, h := range g.MetricHeaders {
			if i < len(r.MetricValues) {
				row[h.Name] = r.MetricValues[i].Value
			}
		}
		out = append(out, row)
	}
	return out
}

func i64(row map[string]string, k string) int64 {
	v := strings.TrimSpace(row[k])
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return n
	}
	f, _ := strconv.ParseFloat(v, 64)
	return int64(f + 0.5)
}

type dim struct {
	Name string `json:"name"`
}

type met struct {
	Name string `json:"name"`
}

type dateRange struct {
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
}

type orderBy struct {
	Desc      bool           `json:"desc,omitempty"`
	Metric    map[string]any `json:"metric,omitempty"`
	Dimension map[string]any `json:"dimension,omitempty"`
}

type runReport struct {
	DateRanges []dateRange `json:"dateRanges"`
	Dimensions []dim       `json:"dimensions,omitempty"`
	Metrics    []met       `json:"metrics"`
	OrderBys   []orderBy   `json:"orderBys,omitempty"`
	Limit      string      `json:"limit,omitempty"`
	KeepEmpty  bool        `json:"keepEmptyRows,omitempty"`
}

func metrics(names ...string) []met {
	out := make([]met, len(names))
	for i, n := range names {
		out[i] = met{Name: n}
	}
	return out
}

// Fetch asks GA4 for one window. property is "properties/123".
func Fetch(ctx context.Context, c *google.Client, property string, days int, now time.Time) (*Report, error) {
	if !strings.HasPrefix(property, "properties/") {
		return nil, fmt.Errorf("not a GA4 property: %q", property)
	}
	dr := []dateRange{{StartDate: strconv.Itoa(days) + "daysAgo", EndDate: "yesterday"}}
	req := map[string]any{"requests": []runReport{
		{DateRanges: dr, Metrics: metrics("activeUsers", "newUsers", "sessions", "screenPageViews", "engagedSessions", "userEngagementDuration")},
		{DateRanges: dr, Dimensions: []dim{{"date"}}, Metrics: metrics("activeUsers", "sessions", "screenPageViews"),
			OrderBys: []orderBy{{Dimension: map[string]any{"dimensionName": "date"}}}, Limit: "400"},
		{DateRanges: dr, Dimensions: []dim{{"pagePath"}}, Metrics: metrics("screenPageViews", "activeUsers"),
			OrderBys: []orderBy{{Desc: true, Metric: map[string]any{"metricName": "screenPageViews"}}}, Limit: "10"},
		{DateRanges: dr, Dimensions: []dim{{"sessionSource"}, {"sessionMedium"}}, Metrics: metrics("sessions", "activeUsers"),
			OrderBys: []orderBy{{Desc: true, Metric: map[string]any{"metricName": "sessions"}}}, Limit: "10"},
	}}
	var out struct {
		Reports []gaReport `json:"reports"`
	}
	u := google.API().AnalyticsData + "/" + escapeProperty(property) + ":batchRunReports"
	if err := c.Post(ctx, u, req, &out); err != nil {
		return nil, err
	}
	if len(out.Reports) != 4 {
		return nil, fmt.Errorf("GA4 answered %d reports for 4 requests", len(out.Reports))
	}
	return build(out.Reports, days, now), nil
}

func escapeProperty(p string) string {
	id := strings.TrimPrefix(p, "properties/")
	return "properties/" + url.PathEscape(id)
}

// build turns GA4's four reports into the page's Report.
func build(reps []gaReport, days int, now time.Time) *Report {
	tz := ""
	for _, r := range reps {
		if r.Metadata.TimeZone != "" {
			tz = r.Metadata.TimeZone
			break
		}
	}
	loc := time.UTC
	if l, err := time.LoadLocation(tz); err == nil && tz != "" {
		loc = l
	} else {
		tz = "UTC"
	}
	local := now.In(loc)
	end := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	start := end.AddDate(0, 0, -(days - 1))
	rep := &Report{Days: days, StartDate: start.Format("2006-01-02"), EndDate: end.Format("2006-01-02"), TimeZone: tz,
		Daily: []Day{}, TopPages: []Page{}, Sources: []Source{}, FetchedAt: now.UTC()}

	if t := reps[0].table(); len(t) > 0 {
		r := t[0]
		tot := Totals{Users: i64(r, "activeUsers"), NewUsers: i64(r, "newUsers"), Sessions: i64(r, "sessions"),
			Views: i64(r, "screenPageViews"), EngagedSessions: i64(r, "engagedSessions"), EngagementSeconds: i64(r, "userEngagementDuration")}
		if tot.Sessions > 0 {
			tot.EngagementRate = float64(tot.EngagedSessions) / float64(tot.Sessions)
		}
		if tot.Users > 0 {
			tot.AvgEngagementPerUse = float64(tot.EngagementSeconds) / float64(tot.Users)
		}
		rep.Totals = tot
	}

	byDate := map[string]Day{}
	for _, r := range reps[1].table() {
		d, err := time.Parse("20060102", r["date"])
		if err != nil {
			continue
		}
		key := d.Format("2006-01-02")
		byDate[key] = Day{Date: key, Users: i64(r, "activeUsers"), Sessions: i64(r, "sessions"), Views: i64(r, "screenPageViews")}
	}
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		day, ok := byDate[key]
		if !ok {
			day = Day{Date: key} // no row: no traffic that day
		}
		rep.Daily = append(rep.Daily, day)
	}
	for _, r := range reps[2].table() {
		rep.TopPages = append(rep.TopPages, Page{Path: r["pagePath"], Views: i64(r, "screenPageViews"), Users: i64(r, "activeUsers")})
	}
	for _, r := range reps[3].table() {
		rep.Sources = append(rep.Sources, Source{Source: r["sessionSource"], Medium: r["sessionMedium"], Sessions: i64(r, "sessions"), Users: i64(r, "activeUsers")})
	}
	// GA4 answers no rows at all for a property with nothing in the window.
	rep.Empty = len(reps[0].Rows) == 0 && len(reps[1].Rows) == 0
	return rep
}

// ---- the cache ------------------------------------------------------------------

func store(ctx context.Context, a *app.App, workspaceID, resourceID string, rep *Report) error {
	raw, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	tx, err := a.SQLDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ga4_reports (workspace_id, resource_id, days, payload, users, sessions, views, start_date, end_date, fetched_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (workspace_id, resource_id, days) DO UPDATE SET payload = EXCLUDED.payload, users = EXCLUDED.users,
			sessions = EXCLUDED.sessions, views = EXCLUDED.views, start_date = EXCLUDED.start_date,
			end_date = EXCLUDED.end_date, fetched_at = EXCLUDED.fetched_at`,
		workspaceID, resourceID, rep.Days, string(raw), rep.Totals.Users, rep.Totals.Sessions, rep.Totals.Views,
		rep.StartDate, rep.EndDate, rep.FetchedAt); err != nil {
		return err
	}
	if !rep.Empty {
		for _, d := range rep.Daily {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO ga4_daily (workspace_id, resource_id, day, users, sessions, views, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, now())
				ON CONFLICT (workspace_id, resource_id, day) DO UPDATE SET users = EXCLUDED.users,
					sessions = EXCLUDED.sessions, views = EXCLUDED.views, updated_at = now()`,
				workspaceID, resourceID, d.Date, d.Users, d.Sessions, d.Views); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func cached(ctx context.Context, a *app.App, workspaceID, resourceID string, days int) *Report {
	var raw []byte
	var fetched time.Time
	err := a.SQLDB.QueryRowContext(ctx, `SELECT payload, fetched_at FROM ga4_reports
		WHERE workspace_id = $1 AND resource_id = $2 AND days = $3`, workspaceID, resourceID, days).Scan(&raw, &fetched)
	if err != nil {
		return nil
	}
	var rep Report
	if json.Unmarshal(raw, &rep) != nil {
		return nil
	}
	rep.FetchedAt = fetched.UTC() // the row decides freshness
	return &rep
}

// ErrNoData: GA4 could not be asked and nothing is stored yet.
type ErrNoData struct{ Cause error }

func (e *ErrNoData) Error() string { return google.Describe(e.Cause) }
func (e *ErrNoData) Unwrap() error { return e.Cause }

// Get answers a window for one enabled GA4 resource: from the cache when
// fresh, else from GA4 (and stored); when GA4 fails, the stored answer marked
// stale, or ErrNoData when there is none. refresh skips the hour — but never
// a backoff Google asked for.
func Get(ctx context.Context, a *app.App, workspaceID string, r *google.Resource, days int, refresh bool) (*Report, error) {
	wctx := tenant.WithWorkspace(ctx, workspaceID)
	old := cached(wctx, a, workspaceID, r.ID, days)
	if old != nil && !refresh && time.Since(old.FetchedAt) < Fresh {
		return old, nil
	}
	if r.BackoffUntil != nil {
		cause := &google.APIError{Status: 429, Reason: "rateLimitExceeded", Message: "backing off until " + r.BackoffUntil.UTC().Format(time.RFC3339)}
		if old != nil {
			old.Stale, old.Note = true, google.Describe(cause)
			return old, nil
		}
		return nil, &ErrNoData{Cause: cause}
	}
	rep, err := refreshReport(wctx, a, workspaceID, r, days)
	if err != nil {
		google.NoteFailure(ctx, a, workspaceID, r.ID, err)
		if old != nil && !errors.Is(err, context.Canceled) {
			old.Stale, old.Note = true, google.Describe(err)
			return old, nil
		}
		return nil, &ErrNoData{Cause: err}
	}
	google.NoteSuccess(ctx, a, workspaceID, r.ID)
	return rep, nil
}

func refreshReport(ctx context.Context, a *app.App, workspaceID string, r *google.Resource, days int) (*Report, error) {
	src, err := google.SourceFor(ctx, a, *r, workspaceID)
	if err != nil {
		return nil, err
	}
	rep, err := Fetch(ctx, src.Client(google.ScopeAnalytics), r.ExternalID, days, time.Now())
	if err != nil {
		return nil, err
	}
	if err := store(ctx, a, workspaceID, r.ID, rep); err != nil {
		return nil, err
	}
	return rep, nil
}

// Sync is the nightly snapshot: the Overview's 28-day window, from GA4.
func Sync(ctx context.Context, a *app.App, r google.Resource) error {
	ws := tenant.WorkspaceID(ctx)
	_, err := refreshReport(ctx, a, ws, &r, SnapshotDays)
	return err
}

// RegisterSync plugs GA4 into the Google collector.
func RegisterSync() { google.RegisterSyncer(google.KindGA4, Sync, google.Nightly) }
