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
YouTube Analytics API v2 reports.query (MH-442):

	GET /youtubeanalytics/v2/reports?ids=channel==MINE&startDate&endDate&metrics[&dimensions&filters&sort&maxResults]
	→ {"kind":"youtubeAnalytics#resultTable",
	   "columnHeaders":[{"name","columnType":"DIMENSION|METRIC","dataType":"STRING|INTEGER|FLOAT"}],
	   "rows":[[…]]}

Dimensions first, then metrics in the order asked. A channel with nothing in
the window answers with the headers and no "rows". Supported: no dimension,
day, video, insightTrafficSourceType; filter video==ID.
*/

var ytMetricType = map[string]string{
	"views": "INTEGER", "estimatedMinutesWatched": "INTEGER", "averageViewDuration": "INTEGER",
	"subscribersGained": "INTEGER", "subscribersLost": "INTEGER", "likes": "INTEGER", "comments": "INTEGER",
	"shares": "INTEGER", "averageViewPercentage": "FLOAT",
}

var ytSources = []struct {
	name  string
	share float64
}{{"YT_SEARCH", 0.34}, {"SUGGESTED", 0.27}, {"BROWSE", 0.16}, {"EXT_URL", 0.09}, {"NO_LINK_OTHER", 0.07}, {"PLAYLIST", 0.04}, {"NOTIFICATION", 0.03}}

type ytDay struct{ views, minutes, gained, lost, likes, comments float64 }

func ytDaily(key string, day time.Time) ytDay {
	k := key + day.Format("20060102")
	base := float64(180 + hashN(key, 200))
	v := math.Round(base*(1+float64(day.YearDay()%30)/60)) + float64(hashN(k, 90))
	return ytDay{views: v, minutes: math.Round(v * (2.4 + float64(hashN(k+"m", 20))/10)), gained: float64(3 + hashN(k+"g", 9)),
		lost: float64(hashN(k+"l", 3)), likes: math.Round(v * 0.045), comments: float64(hashN(k+"c", 4))}
}

func (s *Server) ytAnalytics(w http.ResponseWriter, r *http.Request, p string) bool {
	if p != "/youtubeanalytics/v2/reports" || r.Method != http.MethodGet {
		return false
	}
	_, d, ok := s.begin(w, r, "ytAnalytics")
	if !ok {
		return true
	}
	q := r.URL.Query()
	if q.Get("ids") != "channel==MINE" {
		writeAPIError(w, "ytAnalytics", http.StatusBadRequest, "badRequest", "Unknown identifier (ids) given in field parameters.ids.")
		return true
	}
	start, e1 := time.Parse("2006-01-02", q.Get("startDate"))
	end, e2 := time.Parse("2006-01-02", q.Get("endDate"))
	if e1 != nil || e2 != nil || end.Before(start) {
		writeAPIError(w, "ytAnalytics", http.StatusBadRequest, "badRequest", "Invalid dates.")
		return true
	}
	mets := strings.Split(q.Get("metrics"), ",")
	for _, m := range mets {
		if _, ok := ytMetricType[m]; !ok {
			writeAPIError(w, "ytAnalytics", http.StatusBadRequest, "badRequest", "Unknown metric: "+m)
			return true
		}
	}
	dims := []string{}
	if x := q.Get("dimensions"); x != "" {
		dims = strings.Split(x, ",")
	}
	s.mu.Lock()
	ch := d.Channel
	var videos []YTVideo
	noData := ch == nil || ch.NoAnalytics
	if ch != nil {
		videos = append(videos, ch.Videos...)
	}
	s.mu.Unlock()
	video := strings.TrimPrefix(q.Get("filters"), "video==")
	keys := []string{}
	if video != "" {
		found := false
		for _, v := range videos {
			if v.ID == video {
				found = true
			}
		}
		if !found {
			noData = true
		}
		keys = []string{video}
	} else {
		for _, v := range videos {
			keys = append(keys, v.ID)
		}
	}
	headers := []map[string]string{}
	for _, dn := range dims {
		headers = append(headers, map[string]string{"name": dn, "columnType": "DIMENSION", "dataType": "STRING"})
	}
	for _, m := range mets {
		headers = append(headers, map[string]string{"name": m, "columnType": "METRIC", "dataType": ytMetricType[m]})
	}
	out := map[string]any{"kind": "youtubeAnalytics#resultTable", "columnHeaders": headers}
	if noData || len(keys) == 0 {
		writeJSON(w, http.StatusOK, out)
		return true
	}
	// Aggregates per (dimension value) over the window.
	type agg struct {
		ytDay
		days int
	}
	groups := map[string]*agg{}
	order := []string{}
	add := func(k string, x ytDay, share float64) {
		g, ok := groups[k]
		if !ok {
			g = &agg{}
			groups[k] = g
			order = append(order, k)
		}
		g.views += math.Round(x.views * share)
		g.minutes += math.Round(x.minutes * share)
		g.gained += math.Round(x.gained * share)
		g.lost += math.Round(x.lost * share)
		g.likes += math.Round(x.likes * share)
		g.comments += math.Round(x.comments * share)
		g.days++
	}
	dim := ""
	if len(dims) > 0 {
		dim = dims[0]
	}
	// YouTube publishes a day a day or two late: nothing after the day before yesterday (Pacific).
	pt, _ := time.LoadLocation("America/Los_Angeles")
	if pt == nil {
		pt = time.UTC
	}
	n := time.Now().In(pt)
	lastData := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -2)
	for day := start; !day.After(end) && !day.After(lastData); day = day.AddDate(0, 0, 1) {
		for _, key := range keys {
			x := ytDaily(key, day)
			switch dim {
			case "day":
				add(day.Format("2006-01-02"), x, 1)
			case "video":
				add(key, x, 1)
			case "insightTrafficSourceType":
				for _, src := range ytSources {
					add(src.name, x, src.share)
				}
			case "":
				add("", x, 1)
			default:
				writeAPIError(w, "ytAnalytics", http.StatusBadRequest, "badRequest", "Unsupported dimension: "+dim)
				return true
			}
		}
	}
	rows := [][]any{}
	for _, k := range order {
		g := groups[k]
		row := []any{}
		if dim != "" {
			row = append(row, k)
		}
		for _, m := range mets {
			var v float64
			switch m {
			case "views":
				v = g.views
			case "estimatedMinutesWatched":
				v = g.minutes
			case "averageViewDuration":
				if g.views > 0 {
					v = math.Round(g.minutes * 60 / g.views)
				}
			case "subscribersGained":
				v = g.gained
			case "subscribersLost":
				v = g.lost
			case "likes":
				v = g.likes
			case "comments":
				v = g.comments
			case "averageViewPercentage":
				v = 41.7
			}
			row = append(row, v)
		}
		rows = append(rows, row)
	}
	if sortBy := q.Get("sort"); sortBy != "" {
		desc := strings.HasPrefix(sortBy, "-")
		col := strings.TrimPrefix(sortBy, "-")
		idx := -1
		for i, h := range headers {
			if h["name"] == col {
				idx = i
			}
		}
		if idx >= 0 {
			sort.SliceStable(rows, func(i, j int) bool {
				a, b := rows[i][idx], rows[j][idx]
				var less bool
				if as, ok := a.(string); ok {
					less = as < b.(string)
				} else {
					less = a.(float64) < b.(float64)
				}
				if desc {
					return !less
				}
				return less
			})
		}
	}
	if n, err := strconv.Atoi(q.Get("maxResults")); err == nil && n > 0 && len(rows) > n {
		rows = rows[:n]
	}
	if len(rows) > 0 {
		out["rows"] = rows
	}
	writeJSON(w, http.StatusOK, out)
	return true
}
