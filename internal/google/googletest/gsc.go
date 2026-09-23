package googletest

import (
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

/*
Search Console's Search Analytics (MH-445):

	POST /webmasters/v3/sites/{siteUrl, percent-encoded}/searchAnalytics/query
	     {startDate, endDate, dimensions, rowLimit, startRow, type, dataState}
	→ {"rows":[{"keys":[…], "clicks", "impressions", "ctr", "position"}], "responseAggregationType"}

Like Google's: data lags — nothing for today or yesterday; rows come sorted
by clicks; rowLimit is capped (25,000, or GSCPageCap in a test) and paged
with startRow; a site with no data answers with no "rows" at all; and a query
WITHOUT the query dimension counts anonymised queries that the query-level
rows leave out, so query rows add up to less than the totals.
*/

var gscQueries = []string{
	"launch checklist", "product launch plan", "pricing mistakes", "saas pricing", "consulting cairo",
	"business consultant egypt", "how to price a service", "annual vs monthly pricing", "launch plan template",
	"demo co", "demo co pricing", "استشارات أعمال", "خطة إطلاق منتج", "تسعير الخدمات", "go to market plan",
	"startup launch", "marketing plan egypt", "customer story", "getting started guide", "careers cairo",
}

var gscPagePaths = []string{"/", "/pricing", "/blog/launch-checklist", "/blog/pricing-mistakes", "/services/consulting", "/ar/", "/about", "/careers"}

var gscCountries = []string{"egy", "sau", "are", "usa", "gbr"}

var gscDevices = []string{"DESKTOP", "MOBILE", "TABLET"}

type gscRow struct {
	date, query, page, country, device string
	clicks, impressions                float64
	posSum                             float64 // position × impressions
}

func siteOrigin(site string) string {
	if strings.HasPrefix(site, "sc-domain:") {
		return "https://" + strings.TrimPrefix(site, "sc-domain:")
	}
	return strings.TrimRight(site, "/")
}

// gscDay is every (query, page, country, device) row of one day, plus the
// anonymised remainder per (country, device).
func gscDay(site, date string) (rows []gscRow, anon map[[2]string]gscRow) {
	anon = map[[2]string]gscRow{}
	origin := siteOrigin(site)
	for qi, q := range gscQueries {
		pages := []string{gscPagePaths[qi%len(gscPagePaths)]}
		if qi%3 == 0 {
			pages = append(pages, gscPagePaths[(qi+3)%len(gscPagePaths)])
		}
		for _, p := range pages {
			for ci, c := range gscCountries {
				for di, d := range gscDevices {
					key := site + date + q + p + c + d
					if hashN(key, 10) >= 4 { // sparse, like real data
						continue
					}
					weight := 1.0 / float64(1+qi/4) / float64(1+ci) / float64(1+di)
					impr := math.Round(weight*120) + float64(hashN(key+"i", 25))
					if impr == 0 {
						continue
					}
					pos := 1.5 + float64(qi%9) + float64(hashN(key+"p", 30))/10
					ctr := 0.32 / pos
					clicks := math.Round(impr * ctr)
					rows = append(rows, gscRow{date: date, query: q, page: origin + p, country: c, device: d,
						clicks: clicks, impressions: impr, posSum: pos * impr})
				}
			}
		}
	}
	for _, c := range gscCountries {
		for _, d := range gscDevices {
			key := site + date + c + d + "anon"
			impr := float64(3 + hashN(key, 20))
			anon[[2]string{c, d}] = gscRow{date: date, country: c, device: d, impressions: impr,
				clicks: float64(hashN(key+"c", 3)), posSum: impr * (8 + float64(hashN(key+"p", 40))/5)}
		}
	}
	return rows, anon
}

func (s *Server) gsc(w http.ResponseWriter, r *http.Request, p string) bool {
	const prefix = "/webmasters/v3/sites/"
	if !strings.HasPrefix(p, prefix) || !strings.HasSuffix(p, "/searchAnalytics/query") || r.Method != http.MethodPost {
		return false
	}
	site := unescapeSegment(strings.TrimSuffix(strings.TrimPrefix(p, prefix), "/searchAnalytics/query"))
	_, d, ok := s.begin(w, r, "searchAnalytics")
	if !ok {
		return true
	}
	s.mu.Lock()
	var st *GSCSite
	for i := range d.Sites {
		if d.Sites[i].URL == site && d.Sites[i].Permission != "siteUnverifiedUser" {
			cp := d.Sites[i]
			st = &cp
		}
	}
	pageCap := s.GSCPageCap
	s.mu.Unlock()
	if st == nil {
		writeAPIError(w, "searchAnalytics", http.StatusForbidden, "forbidden", "User does not have sufficient permission for site '"+site+"'. See also: https://support.google.com/webmasters/answer/2451999.")
		return true
	}
	var req struct {
		StartDate  string   `json:"startDate"`
		EndDate    string   `json:"endDate"`
		Dimensions []string `json:"dimensions"`
		RowLimit   int      `json:"rowLimit"`
		StartRow   int      `json:"startRow"`
		Type       string   `json:"type"`
	}
	if err := readJSONBody(r, &req); err != nil {
		writeAPIError(w, "searchAnalytics", http.StatusBadRequest, "badRequest", "Invalid JSON payload received.")
		return true
	}
	start, err1 := time.Parse("2006-01-02", req.StartDate)
	end, err2 := time.Parse("2006-01-02", req.EndDate)
	if err1 != nil || err2 != nil || end.Before(start) {
		writeAPIError(w, "searchAnalytics", http.StatusBadRequest, "badRequest", "Invalid date range.")
		return true
	}
	if req.RowLimit <= 0 {
		req.RowLimit = 1000
	}
	if req.RowLimit > 25000 {
		writeAPIError(w, "searchAnalytics", http.StatusBadRequest, "badRequest", "rowLimit must be at most 25000.")
		return true
	}
	if pageCap > 0 && req.RowLimit > pageCap {
		req.RowLimit = pageCap
	}
	dims := map[string]bool{}
	for _, x := range req.Dimensions {
		switch x {
		case "date", "query", "page", "country", "device":
			dims[x] = true
		default:
			writeAPIError(w, "searchAnalytics", http.StatusBadRequest, "badRequest", "Invalid dimension: "+x)
			return true
		}
	}
	out := map[string]any{"responseAggregationType": "byProperty"}
	if dims["page"] {
		out["responseAggregationType"] = "byPage"
	}
	if st.Empty {
		writeJSON(w, http.StatusOK, out)
		return true
	}
	// The newest final day is the day before yesterday (Pacific, like Google).
	pt, _ := time.LoadLocation("America/Los_Angeles")
	if pt == nil {
		pt = time.UTC
	}
	now := time.Now().In(pt)
	lastFinal := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -2)

	agg := map[string]*gscRow{}
	order := []string{}
	add := func(x gscRow) {
		keys := []string{}
		for _, dname := range req.Dimensions {
			switch dname {
			case "date":
				keys = append(keys, x.date)
			case "query":
				keys = append(keys, x.query)
			case "page":
				keys = append(keys, x.page)
			case "country":
				keys = append(keys, x.country)
			case "device":
				keys = append(keys, x.device)
			}
		}
		k := strings.Join(keys, "\x00")
		a, ok := agg[k]
		if !ok {
			a = &gscRow{}
			agg[k] = a
			order = append(order, k)
		}
		a.clicks += x.clicks
		a.impressions += x.impressions
		a.posSum += x.posSum
	}
	for day := start; !day.After(end) && !day.After(lastFinal); day = day.AddDate(0, 0, 1) {
		ds := day.Format("2006-01-02")
		rows, anon := gscDay(site, ds)
		for _, x := range rows {
			add(x)
		}
		if !dims["query"] && !dims["page"] {
			// Totals include the anonymised queries.
			for _, x := range anon {
				add(x)
			}
		}
	}
	type outRow struct {
		keys                           []string
		clicks, impressions, ctr, posn float64
	}
	list := []outRow{}
	for _, k := range order {
		a := agg[k]
		if a.impressions == 0 {
			continue
		}
		keys := []string{}
		if k != "" || len(req.Dimensions) > 0 {
			keys = strings.Split(k, "\x00")
		}
		list = append(list, outRow{keys: keys, clicks: a.clicks, impressions: a.impressions,
			ctr: a.clicks / a.impressions, posn: a.posSum / a.impressions})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].clicks != list[j].clicks {
			return list[i].clicks > list[j].clicks
		}
		if list[i].impressions != list[j].impressions {
			return list[i].impressions > list[j].impressions
		}
		return strings.Join(list[i].keys, "|") < strings.Join(list[j].keys, "|")
	})
	if req.StartRow >= len(list) {
		list = nil
	} else {
		list = list[req.StartRow:]
	}
	if len(list) > req.RowLimit {
		list = list[:req.RowLimit]
	}
	if len(list) > 0 {
		rows := []map[string]any{}
		for _, x := range list {
			row := map[string]any{"clicks": x.clicks, "impressions": x.impressions, "ctr": x.ctr, "position": x.posn}
			if len(req.Dimensions) > 0 {
				row["keys"] = x.keys
			}
			rows = append(rows, row)
		}
		out["rows"] = rows
	}
	writeJSON(w, http.StatusOK, out)
	return true
}
