package searchconsole

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
)

// Windows the page offers.
var Windows = []int{7, 28, 90}

// ValidDays reports whether days is a window the page offers.
func ValidDays(days int) bool {
	for _, w := range Windows {
		if w == days {
			return true
		}
	}
	return false
}

// Filters narrow the report. Query and Page match as "contains", case-blind.
type Filters struct {
	Query   string
	Page    string
	Country string // ISO 3166-1 alpha-3, lower case, as Search Console writes it
	Device  string // DESKTOP | MOBILE | TABLET
}

func (f Filters) detail() bool {
	return strings.TrimSpace(f.Query) != "" || strings.TrimSpace(f.Page) != ""
}

// SearchTotals are a period's sums. Position is null when there were no impressions.
type SearchTotals struct {
	Clicks      int64    `json:"clicks"`
	Impressions int64    `json:"impressions"`
	CTR         float64  `json:"ctr" doc:"clicks / impressions, 0..1."`
	Position    *float64 `json:"position" doc:"Impression-weighted average position; null without impressions."`
}

// SearchPoint is one day. A nil field is a day that was never imported.
type SearchPoint struct {
	Date        string   `json:"date"`
	Clicks      *int64   `json:"clicks"`
	Impressions *int64   `json:"impressions"`
	CTR         *float64 `json:"ctr"`
	Position    *float64 `json:"position"`
}

// SearchRow is one query or page.
type SearchRow struct {
	Key         string   `json:"key"`
	Clicks      int64    `json:"clicks"`
	Impressions int64    `json:"impressions"`
	CTR         float64  `json:"ctr"`
	Position    *float64 `json:"position"`
}

// SearchCoverage says what has been imported.
type SearchCoverage struct {
	ImportedDays int        `json:"imported_days"`
	FirstDate    *string    `json:"first_date" doc:"The oldest imported day."`
	LastDataDate *string    `json:"last_data_date" doc:"The newest day Search Console had data for."`
	LastImportAt *time.Time `json:"last_import_at"`
	Truncated    int        `json:"truncated_days" doc:"Days with more query rows than one import keeps."`
}

// Performance is the Search Console page.
type Performance struct {
	Days      int    `json:"days"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date" doc:"The newest day with data: Search Console lags two to three days."`
	// Totals and the trend come from the site's totals (anonymised queries
	// included) unless a query or page filter is set; then they are sums of
	// query-level rows, which leave anonymised queries out.
	Basis         string         `json:"basis" enum:"site,queries"`
	Totals        SearchTotals   `json:"totals"`
	Previous      *SearchTotals  `json:"previous" doc:"The same length of time just before, when compare is on."`
	PrevStartDate string         `json:"previous_start_date,omitempty"`
	PrevEndDate   string         `json:"previous_end_date,omitempty"`
	Daily         []SearchPoint  `json:"daily"`
	PreviousDaily []SearchPoint  `json:"previous_daily"`
	TopQueries    []SearchRow    `json:"top_queries"`
	TopPages      []SearchRow    `json:"top_pages"`
	Countries     []string       `json:"countries" doc:"Countries with data in the window, for the filter."`
	Devices       []string       `json:"devices"`
	Coverage      SearchCoverage `json:"coverage"`
	Empty         bool           `json:"empty" doc:"Imported, and Search Console has nothing in this window."`
	// Days of each window not imported (yet): their numbers are unknown, so
	// totals over them are partial and a comparison against them is not made.
	Missing         int  `json:"missing_days"`
	PreviousMissing int  `json:"previous_missing_days"`
	NothingYet      bool `json:"nothing_yet" doc:"No day has been imported yet (the first import is running or failed)."`
}

type q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Report builds the page for one site. ctx carries the workspace.
func Report(ctx context.Context, a *app.App, ws, resourceID string, days int, compare bool, f Filters) (*Performance, error) {
	db := a.SQLDB
	p := &Performance{Days: days, Basis: "site", Daily: []SearchPoint{}, PreviousDaily: []SearchPoint{}, TopQueries: []SearchRow{},
		TopPages: []SearchRow{}, Countries: []string{}, Devices: []string{}}
	if f.detail() {
		p.Basis = "queries"
	}
	var first, lastData sql.NullString
	var lastAt sql.NullTime
	if err := db.QueryRowContext(ctx, `
		SELECT count(*), to_char(min(date), 'YYYY-MM-DD'), max(imported_at), count(*) FILTER (WHERE truncated),
		       (SELECT to_char(max(date), 'YYYY-MM-DD') FROM gsc_search_totals_daily t WHERE t.workspace_id = $1 AND t.resource_id = $2)
		FROM gsc_import_days WHERE workspace_id = $1 AND resource_id = $2`, ws, resourceID).
		Scan(&p.Coverage.ImportedDays, &first, &lastAt, &p.Coverage.Truncated, &lastData); err != nil {
		return nil, err
	}
	if first.Valid {
		p.Coverage.FirstDate = &first.String
	}
	if lastData.Valid {
		p.Coverage.LastDataDate = &lastData.String
	}
	if lastAt.Valid {
		t := lastAt.Time.UTC()
		p.Coverage.LastImportAt = &t
	}
	if p.Coverage.ImportedDays == 0 {
		p.NothingYet = true
		return p, nil
	}

	// The window ends on the newest day with data (Search Console lags), or,
	// with none at all, on the newest imported day.
	end := lastData.String
	if !lastData.Valid {
		if err := db.QueryRowContext(ctx, `SELECT to_char(max(date), 'YYYY-MM-DD') FROM gsc_import_days
			WHERE workspace_id = $1 AND resource_id = $2`, ws, resourceID).Scan(&end); err != nil {
			return nil, err
		}
	}
	endT, _ := time.Parse("2006-01-02", end)
	startT := endT.AddDate(0, 0, -(days - 1))
	p.StartDate, p.EndDate = startT.Format("2006-01-02"), end

	var err error
	if p.Totals, p.Daily, err = period(ctx, db, ws, resourceID, startT, endT, f); err != nil {
		return nil, err
	}
	p.Missing = missing(p.Daily)
	p.Empty = p.Totals.Impressions == 0
	if compare {
		pe := startT.AddDate(0, 0, -1)
		ps := pe.AddDate(0, 0, -(days - 1))
		p.PrevStartDate, p.PrevEndDate = ps.Format("2006-01-02"), pe.Format("2006-01-02")
		prev, daily, err := period(ctx, db, ws, resourceID, ps, pe, f)
		if err != nil {
			return nil, err
		}
		p.Previous, p.PreviousDaily = &prev, daily
		p.PreviousMissing = missing(daily)
	}
	if p.TopQueries, err = top(ctx, db, ws, resourceID, "query", startT, endT, f); err != nil {
		return nil, err
	}
	if p.TopPages, err = top(ctx, db, ws, resourceID, "page", startT, endT, f); err != nil {
		return nil, err
	}
	for _, col := range []string{"country", "device"} {
		rows, err := db.QueryContext(ctx, `SELECT `+col+`, sum(clicks) FROM gsc_search_totals_daily
			WHERE workspace_id = $1 AND resource_id = $2 AND date BETWEEN $3 AND $4 AND `+col+` <> ''
			GROUP BY 1 ORDER BY 2 DESC, 1`, ws, resourceID, p.StartDate, p.EndDate)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var v string
			var n int64
			if rows.Scan(&v, &n) == nil {
				if col == "country" {
					p.Countries = append(p.Countries, v)
				} else {
					p.Devices = append(p.Devices, v)
				}
			}
		}
		rows.Close()
	}
	return p, nil
}

func missing(days []SearchPoint) int {
	n := 0
	for _, d := range days {
		if d.Impressions == nil {
			n++
		}
	}
	return n
}

// where builds the filter predicate for the totals table (site basis) or the
// query-level table.
func where(f Filters, detail bool, args *[]any) string {
	var sb strings.Builder
	add := func(cond string, v any) {
		*args = append(*args, v)
		sb.WriteString(" AND " + strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(*args))))
	}
	if f.Country != "" {
		add("country = ?", strings.ToLower(f.Country))
	}
	if f.Device != "" {
		add("device = ?", strings.ToUpper(f.Device))
	}
	if detail {
		if s := strings.TrimSpace(f.Query); s != "" {
			add(`query ILIKE ? ESCAPE '\'`, "%"+likeEscape(s)+"%")
		}
		if s := strings.TrimSpace(f.Page); s != "" {
			add(`page ILIKE ? ESCAPE '\'`, "%"+likeEscape(s)+"%")
		}
	}
	return sb.String()
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func period(ctx context.Context, db q, ws, resourceID string, start, end time.Time, f Filters) (SearchTotals, []SearchPoint, error) {
	table := "gsc_search_totals_daily"
	if f.detail() {
		table = "gsc_search_analytics_daily"
	}
	args := []any{ws, resourceID, start.Format("2006-01-02"), end.Format("2006-01-02")}
	cond := where(f, f.detail(), &args)
	var t SearchTotals
	var pos sql.NullFloat64
	if err := db.QueryRowContext(ctx, `
		SELECT coalesce(sum(clicks), 0), coalesce(sum(impressions), 0),
		       sum(position * impressions) / nullif(sum(impressions), 0)
		FROM `+table+` WHERE workspace_id = $1 AND resource_id = $2 AND date BETWEEN $3 AND $4`+cond, args...).
		Scan(&t.Clicks, &t.Impressions, &pos); err != nil {
		return t, nil, err
	}
	if t.Impressions > 0 {
		t.CTR = float64(t.Clicks) / float64(t.Impressions)
	}
	if pos.Valid {
		v := pos.Float64
		t.Position = &v
	}

	byDay := map[string]SearchPoint{}
	rows, err := db.QueryContext(ctx, `
		SELECT to_char(date, 'YYYY-MM-DD'), sum(clicks), sum(impressions), sum(position * impressions) / nullif(sum(impressions), 0)
		FROM `+table+` WHERE workspace_id = $1 AND resource_id = $2 AND date BETWEEN $3 AND $4`+cond+`
		GROUP BY date`, args...)
	if err != nil {
		return t, nil, err
	}
	for rows.Next() {
		var d string
		var c, i int64
		var ps sql.NullFloat64
		if err := rows.Scan(&d, &c, &i, &ps); err != nil {
			rows.Close()
			return t, nil, err
		}
		pt := SearchPoint{Date: d, Clicks: &c, Impressions: &i}
		if i > 0 {
			ctr := float64(c) / float64(i)
			pt.CTR = &ctr
		}
		if ps.Valid {
			v := ps.Float64
			pt.Position = &v
		}
		byDay[d] = pt
	}
	rows.Close()
	imported := map[string]bool{}
	rows, err = db.QueryContext(ctx, `SELECT to_char(date, 'YYYY-MM-DD') FROM gsc_import_days
		WHERE workspace_id = $1 AND resource_id = $2 AND date BETWEEN $3 AND $4`, ws, resourceID, args[2], args[3])
	if err != nil {
		return t, nil, err
	}
	for rows.Next() {
		var d string
		if rows.Scan(&d) == nil {
			imported[d] = true
		}
	}
	rows.Close()
	daily := []SearchPoint{}
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		ds := d.Format("2006-01-02")
		if pt, ok := byDay[ds]; ok {
			daily = append(daily, pt)
			continue
		}
		pt := SearchPoint{Date: ds}
		if imported[ds] {
			// Imported, and Search Console had nothing: a real zero.
			var zero int64
			z2 := zero
			pt.Clicks, pt.Impressions = &zero, &z2
		}
		daily = append(daily, pt)
	}
	return t, daily, nil
}

func top(ctx context.Context, db q, ws, resourceID, col string, start, end time.Time, f Filters) ([]SearchRow, error) {
	args := []any{ws, resourceID, start.Format("2006-01-02"), end.Format("2006-01-02")}
	cond := where(f, true, &args)
	rows, err := db.QueryContext(ctx, `
		SELECT `+col+`, sum(clicks), sum(impressions), sum(position * impressions) / nullif(sum(impressions), 0)
		FROM gsc_search_analytics_daily
		WHERE workspace_id = $1 AND resource_id = $2 AND date BETWEEN $3 AND $4`+cond+`
		GROUP BY 1 ORDER BY 2 DESC, 3 DESC, 1 LIMIT 25`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SearchRow{}
	for rows.Next() {
		var r SearchRow
		var ps sql.NullFloat64
		if err := rows.Scan(&r.Key, &r.Clicks, &r.Impressions, &ps); err != nil {
			return nil, err
		}
		if r.Impressions > 0 {
			r.CTR = float64(r.Clicks) / float64(r.Impressions)
		}
		if ps.Valid {
			v := ps.Float64
			r.Position = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
