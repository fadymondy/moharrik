package googletest

import (
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

/*
The GA4 Data API (MH-444): properties/{id}:runReport and :batchRunReports.

Numbers are synthetic but deterministic per (property, date), and behave like
GA4's where it matters: activeUsers over a range is NOT the sum of the daily
active users (a returning user is one user), so a total must come from a
report without the date dimension; a day without traffic has no row; a
property with no data answers with no "rows" at all; every value is a string.
Dates are in the property's time zone (FakeGA4TimeZone).
*/

// FakeGA4TimeZone is the zone every fake property reports in.
const FakeGA4TimeZone = "Africa/Cairo"

type gaRequest struct {
	Property   string `json:"property"`
	DateRanges []struct {
		StartDate string `json:"startDate"`
		EndDate   string `json:"endDate"`
	} `json:"dateRanges"`
	Dimensions []struct {
		Name string `json:"name"`
	} `json:"dimensions"`
	Metrics []struct {
		Name string `json:"name"`
	} `json:"metrics"`
	Limit    any `json:"limit"`
	OrderBys []struct {
		Desc   bool `json:"desc"`
		Metric *struct {
			MetricName string `json:"metricName"`
		} `json:"metric"`
		Dimension *struct {
			DimensionName string `json:"dimensionName"`
		} `json:"dimension"`
	} `json:"orderBys"`
}

var gaMetricType = map[string]string{
	"activeUsers": "TYPE_INTEGER", "totalUsers": "TYPE_INTEGER", "newUsers": "TYPE_INTEGER", "sessions": "TYPE_INTEGER",
	"screenPageViews": "TYPE_INTEGER", "engagedSessions": "TYPE_INTEGER", "userEngagementDuration": "TYPE_SECONDS",
	"engagementRate": "TYPE_FLOAT", "averageSessionDuration": "TYPE_SECONDS", "bounceRate": "TYPE_FLOAT",
	"keyEvents": "TYPE_INTEGER", // MH-567: GA4's renamed "conversions" metric.
}

var gaDimensions = map[string]bool{"date": true, "pagePath": true, "pageTitle": true, "sessionSource": true, "sessionMedium": true, "sessionSourceMedium": true, "country": true, "deviceCategory": true}

func (s *Server) ga4(w http.ResponseWriter, r *http.Request, p string) bool {
	const prefix = "/analyticsdata/v1beta/properties/"
	if !strings.HasPrefix(p, prefix) || r.Method != http.MethodPost {
		return false
	}
	rest := strings.TrimPrefix(p, prefix)
	id, method, found := strings.Cut(rest, ":")
	if !found || (method != "runReport" && method != "batchRunReports") {
		return false
	}
	_, d, ok := s.begin(w, r, "runReport")
	if !ok {
		return true
	}
	prop := "properties/" + id
	s.mu.Lock()
	var gp *GA4Property
	for i := range d.GA4 {
		if d.GA4[i].ID == prop {
			cp := d.GA4[i]
			gp = &cp
		}
	}
	s.mu.Unlock()
	if gp == nil {
		writeAPIError(w, "runReport", http.StatusForbidden, "", "User does not have sufficient permissions for this property. To learn more about Property ID, see https://developers.google.com/analytics/devguides/reporting/data/v1/property-id.")
		return true
	}
	if method == "runReport" {
		var req gaRequest
		if err := readJSONBody(r, &req); err != nil {
			writeAPIError(w, "runReport", http.StatusBadRequest, "", "Invalid JSON payload received.")
			return true
		}
		out, msg := gaReport(*gp, req)
		if msg != "" {
			writeAPIError(w, "runReport", http.StatusBadRequest, "", msg)
			return true
		}
		writeJSON(w, http.StatusOK, out)
		return true
	}
	var batch struct {
		Requests []gaRequest `json:"requests"`
	}
	if err := readJSONBody(r, &batch); err != nil || len(batch.Requests) == 0 || len(batch.Requests) > 5 {
		writeAPIError(w, "runReport", http.StatusBadRequest, "", "Batch requests must contain between 1 and 5 requests.")
		return true
	}
	reports := []map[string]any{}
	for _, req := range batch.Requests {
		out, msg := gaReport(*gp, req)
		if msg != "" {
			writeAPIError(w, "runReport", http.StatusBadRequest, "", msg)
			return true
		}
		delete(out, "kind")
		reports = append(reports, out)
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": reports, "kind": "analyticsData#batchRunReports"})
	return true
}

// gaDate resolves a GA4 date expression in the property's zone.
func gaDate(expr string, now time.Time) (time.Time, bool) {
	day := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC) }
	switch {
	case expr == "today":
		return day(now), true
	case expr == "yesterday":
		return day(now).AddDate(0, 0, -1), true
	case strings.HasSuffix(expr, "daysAgo"):
		n, err := strconv.Atoi(strings.TrimSuffix(expr, "daysAgo"))
		if err != nil || n < 0 {
			return time.Time{}, false
		}
		return day(now).AddDate(0, 0, -n), true
	}
	t, err := time.Parse("2006-01-02", expr)
	return t, err == nil
}

type gaDay struct {
	date                                                          time.Time
	users, newUsers, sessions, views, engaged, seconds, keyEvents float64
}

// gaDaily is a property's synthetic day: zero on some days, as real sites have.
func gaDaily(prop string, day time.Time) gaDay {
	key := prop + day.Format("20060102")
	if hashN(key, 23) == 0 {
		return gaDay{date: day} // a quiet day: no row
	}
	base := float64(90 + hashN(prop, 160))
	factor := 1.0
	switch day.Weekday() {
	case time.Friday:
		factor = 0.62 // the Egyptian weekend
	case time.Saturday:
		factor = 0.74
	}
	trend := 1 + float64(day.YearDay()%60)/300
	users := math.Round(base*factor*trend) + float64(hashN(key, 35))
	sessions := math.Round(users * (1.25 + float64(hashN(key+"s", 20))/100))
	return gaDay{date: day, users: users, newUsers: math.Round(users * 0.61), sessions: sessions,
		views: math.Round(sessions * (2.1 + float64(hashN(key+"v", 60))/100)), engaged: math.Round(sessions * 0.56),
		seconds:   math.Round(users * (70 + float64(hashN(key+"e", 50)))),
		keyEvents: math.Round(sessions * (0.03 + float64(hashN(key+"k", 10))/1000))}
}

var gaPages = []struct {
	path, title string
	share       float64
}{
	{"/", "Home", 0.31}, {"/pricing", "Pricing", 0.14}, {"/blog/launch-checklist", "The launch checklist", 0.11},
	{"/services/consulting", "Consulting", 0.08}, {"/about", "About us", 0.06}, {"/contact", "Contact", 0.05},
	{"/blog/pricing-mistakes", "Five pricing mistakes", 0.045}, {"/careers", "Careers", 0.03},
	{"/ar/", "الرئيسية", 0.028}, {"/docs/getting-started", "Getting started", 0.022}, {"/privacy", "Privacy", 0.01},
}

var gaSources = []struct {
	source, medium string
	share          float64
}{
	{"google", "organic", 0.42}, {"(direct)", "(none)", 0.27}, {"linkedin.com", "referral", 0.09},
	{"newsletter", "email", 0.07}, {"t.co", "referral", 0.05}, {"bing", "organic", 0.04}, {"facebook.com", "referral", 0.03},
}

func gaReport(gp GA4Property, req gaRequest) (map[string]any, string) {
	loc, err := time.LoadLocation(FakeGA4TimeZone)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	if len(req.DateRanges) != 1 {
		return nil, "Exactly one date range is supported by this fake."
	}
	start, ok1 := gaDate(req.DateRanges[0].StartDate, now)
	end, ok2 := gaDate(req.DateRanges[0].EndDate, now)
	if !ok1 || !ok2 || end.Before(start) {
		return nil, "Invalid date range."
	}
	dims := []string{}
	for _, d := range req.Dimensions {
		if !gaDimensions[d.Name] {
			return nil, "Field " + d.Name + " is not a valid dimension."
		}
		dims = append(dims, d.Name)
	}
	mets := []string{}
	for _, m := range req.Metrics {
		if _, ok := gaMetricType[m.Name]; !ok {
			return nil, "Field " + m.Name + " is not a valid metric."
		}
		mets = append(mets, m.Name)
	}
	out := map[string]any{"kind": "analyticsData#runReport", "metadata": map[string]any{"currencyCode": "USD", "timeZone": FakeGA4TimeZone}}
	dh := []map[string]string{}
	for _, d := range dims {
		dh = append(dh, map[string]string{"name": d})
	}
	mh := []map[string]string{}
	for _, m := range mets {
		mh = append(mh, map[string]string{"name": m, "type": gaMetricType[m]})
	}
	out["dimensionHeaders"], out["metricHeaders"] = dh, mh
	if gp.Empty {
		return out, "" // no rows, no rowCount: exactly what GA4 answers for a property with no data
	}

	var days []gaDay
	total := gaDay{}
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		g := gaDaily(gp.ID, d)
		days = append(days, g)
		total.users += g.users
		total.newUsers += g.newUsers
		total.sessions += g.sessions
		total.views += g.views
		total.engaged += g.engaged
		total.seconds += g.seconds
		total.keyEvents += g.keyEvents
	}
	metric := func(g gaDay, share float64, m string, span int) float64 {
		users := g.users
		if span > 1 {
			// Over several days a returning visitor is one user.
			users = math.Round(users * (0.78 + 0.2/float64(span)))
		}
		switch m {
		case "activeUsers", "totalUsers":
			return math.Round(users * share)
		case "newUsers":
			return math.Round(g.newUsers * share)
		case "sessions":
			return math.Round(g.sessions * share)
		case "screenPageViews":
			return math.Round(g.views * share)
		case "engagedSessions":
			return math.Round(g.engaged * share)
		case "userEngagementDuration":
			return math.Round(g.seconds * share)
		case "keyEvents":
			return math.Round(g.keyEvents * share)
		case "engagementRate":
			if g.sessions == 0 {
				return 0
			}
			return g.engaged / g.sessions
		case "averageSessionDuration":
			if g.sessions == 0 {
				return 0
			}
			return g.seconds / g.sessions
		case "bounceRate":
			if g.sessions == 0 {
				return 0
			}
			return 1 - g.engaged/g.sessions
		}
		return 0
	}
	type row struct {
		dims []string
		mets []float64
	}
	rows := []row{}
	span := len(days)
	add := func(dimVals []string, g gaDay, share float64, sp int) {
		r := row{dims: dimVals}
		allZero := true
		for _, m := range mets {
			v := metric(g, share, m, sp)
			if v != 0 {
				allZero = false
			}
			r.mets = append(r.mets, v)
		}
		if !allZero {
			rows = append(rows, r)
		}
	}
	switch {
	case len(dims) == 0:
		add(nil, total, 1, span)
	case len(dims) == 1 && dims[0] == "date":
		for _, g := range days {
			add([]string{g.date.Format("20060102")}, g, 1, 1)
		}
	case contains(dims, "pagePath") || contains(dims, "pageTitle"):
		for _, pg := range gaPages {
			vals := []string{}
			for _, d := range dims {
				if d == "pageTitle" {
					vals = append(vals, pg.title)
				} else {
					vals = append(vals, pg.path)
				}
			}
			add(vals, total, pg.share, span)
		}
	case contains(dims, "sessionSource") || contains(dims, "sessionMedium") || contains(dims, "sessionSourceMedium"):
		for _, src := range gaSources {
			vals := []string{}
			for _, d := range dims {
				switch d {
				case "sessionSource":
					vals = append(vals, src.source)
				case "sessionMedium":
					vals = append(vals, src.medium)
				default:
					vals = append(vals, src.source+" / "+src.medium)
				}
			}
			add(vals, total, src.share, span)
		}
	default:
		return nil, "This fake does not combine those dimensions."
	}
	// orderBys: the first one wins; default is GA4's (by the first metric, desc).
	if len(req.OrderBys) > 0 {
		ob := req.OrderBys[0]
		sort.SliceStable(rows, func(i, j int) bool {
			var less bool
			if ob.Dimension != nil {
				k := indexOf(dims, ob.Dimension.DimensionName)
				less = rows[i].dims[k] < rows[j].dims[k]
			} else if ob.Metric != nil {
				k := indexOf(mets, ob.Metric.MetricName)
				less = rows[i].mets[k] < rows[j].mets[k]
			}
			if ob.Desc {
				return !less
			}
			return less
		})
	}
	rowCount := len(rows)
	if lim := limitOf(req.Limit); lim > 0 && len(rows) > lim {
		rows = rows[:lim]
	}
	if len(rows) == 0 {
		return out, ""
	}
	list := []map[string]any{}
	for _, r := range rows {
		dv := []map[string]string{}
		for _, v := range r.dims {
			dv = append(dv, map[string]string{"value": v})
		}
		mv := []map[string]string{}
		for i, v := range r.mets {
			if gaMetricType[mets[i]] == "TYPE_FLOAT" {
				mv = append(mv, map[string]string{"value": strconv.FormatFloat(v, 'f', -1, 64)})
			} else if gaMetricType[mets[i]] == "TYPE_SECONDS" && mets[i] != "userEngagementDuration" {
				mv = append(mv, map[string]string{"value": strconv.FormatFloat(v, 'f', 6, 64)})
			} else {
				mv = append(mv, map[string]string{"value": strconv.FormatInt(int64(v), 10)})
			}
		}
		item := map[string]any{"metricValues": mv}
		if len(dv) > 0 {
			item["dimensionValues"] = dv
		}
		list = append(list, item)
	}
	out["rows"], out["rowCount"] = list, rowCount
	return out, ""
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return 0
}

// limitOf reads GA4's int64-as-string (or number) limit.
func limitOf(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	}
	return 0
}
