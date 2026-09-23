/*
Package searchconsole imports a workspace's Search Console performance (MH-445)
and answers the Search Console page from what was imported.

Import, daily per enabled site (the Google collector runs it):

	POST webmasters/v3/sites/{site}/searchAnalytics/query, one day at a time:
	  totals   dimensions [country, device]                → gsc_search_totals_daily
	  detail   dimensions [query, page, country, device],
	           25,000 rows a page, at most MaxDetailPages  → gsc_search_analytics_daily

Days are Search Console's (Pacific). The first import walks back Backfill days,
newest first, so the page has recent numbers within the first minute; after
that each run imports the days never imported plus the ones younger than
Settle, which Google may still be finalising. A day is replaced whole, in one
transaction, so a re-import never double counts. gsc_import_days records each
day, which is how an imported day with no data reads as 0 rather than unknown.
*/
package searchconsole

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/tenant"
)

const (
	// Backfill is how far back the first import reaches (Search Console keeps 16 months).
	Backfill = 90
	// Settle is how long after a day it is imported again: Google finalises over ~3 days.
	Settle = 4 * 24 * time.Hour
	// MaxDaysPerSync bounds one run; the rest waits for the next.
	MaxDaysPerSync = Backfill + 10
)

// RowLimit is Search Analytics' own maximum per request (tests lower it to
// exercise paging).
var RowLimit = 25000

// MaxDetailPages is how many 25,000-row pages of query-level rows one day may
// keep. A day beyond it is marked truncated (the page says so).
var MaxDetailPages = 2

// pacific is Search Console's day.
var pacific = func() *time.Location {
	if l, err := time.LoadLocation("America/Los_Angeles"); err == nil {
		return l
	}
	return time.UTC
}()

func day(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC) }

type saRow struct {
	Keys        []string `json:"keys"`
	Clicks      float64  `json:"clicks"`
	Impressions float64  `json:"impressions"`
	CTR         float64  `json:"ctr"`
	Position    float64  `json:"position"`
}

func query(ctx context.Context, c *google.Client, site string, d string, dims []string, startRow int) ([]saRow, error) {
	var out struct {
		Rows []saRow `json:"rows"`
	}
	u := google.API().Webmasters + "/sites/" + url.PathEscape(site) + "/searchAnalytics/query"
	err := c.Post(ctx, u, map[string]any{
		"startDate": d, "endDate": d, "dimensions": dims, "rowLimit": RowLimit, "startRow": startRow, "type": "web",
	}, &out)
	return out.Rows, err
}

// Sync imports what is due for one site. ctx carries the workspace.
func Sync(ctx context.Context, a *app.App, r google.Resource) error {
	ws := tenant.WorkspaceID(ctx)
	src, err := google.SourceFor(ctx, a, r, ws)
	if err != nil {
		return err
	}
	return Import(ctx, a, ws, r, src.Client(), time.Now())
}

// Import imports the due days of one site, newest first.
func Import(ctx context.Context, a *app.App, ws string, r google.Resource, c *google.Client, now time.Time) error {
	today := day(now.In(pacific))
	first := today.AddDate(0, 0, -Backfill)
	imported := map[string]time.Time{}
	rows, err := a.SQLDB.QueryContext(ctx, `SELECT to_char(date, 'YYYY-MM-DD'), imported_at FROM gsc_import_days
		WHERE workspace_id = $1 AND resource_id = $2 AND date >= $3`, ws, r.ID, first.Format("2006-01-02"))
	if err != nil {
		return err
	}
	for rows.Next() {
		var d string
		var at time.Time
		if rows.Scan(&d, &at) == nil {
			imported[d] = at
		}
	}
	rows.Close()

	done := 0
	for d := today.AddDate(0, 0, -1); !d.Before(first) && done < MaxDaysPerSync; d = d.AddDate(0, 0, -1) {
		ds := d.Format("2006-01-02")
		if at, ok := imported[ds]; ok && at.Sub(d) > Settle+24*time.Hour {
			continue // imported once it had settled: final
		}
		if err := importDay(ctx, a, ws, r, c, ds); err != nil {
			return fmt.Errorf("importing %s: %w", ds, err)
		}
		done++
	}
	return nil
}

func importDay(ctx context.Context, a *app.App, ws string, r google.Resource, c *google.Client, d string) error {
	totals, err := query(ctx, c, r.ExternalID, d, []string{"country", "device"}, 0)
	if err != nil {
		return err
	}
	var detail []saRow
	truncated := false
	for page := 0; page < MaxDetailPages; page++ {
		got, err := query(ctx, c, r.ExternalID, d, []string{"query", "page", "country", "device"}, page*RowLimit)
		if err != nil {
			return err
		}
		detail = append(detail, got...)
		if len(got) < RowLimit {
			break
		}
		if page == MaxDetailPages-1 {
			truncated = true
		}
	}
	tx, err := a.SQLDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, t := range []string{"gsc_search_totals_daily", "gsc_search_analytics_daily"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+t+` WHERE workspace_id = $1 AND resource_id = $2 AND date = $3`, ws, r.ID, d); err != nil {
			return err
		}
	}
	if err := insertRows(ctx, tx, ws, r.ID, d, totals, false); err != nil {
		return err
	}
	if err := insertRows(ctx, tx, ws, r.ID, d, detail, true); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO gsc_import_days (workspace_id, resource_id, date, detail_rows, truncated, imported_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (workspace_id, resource_id, date) DO UPDATE SET detail_rows = EXCLUDED.detail_rows,
			truncated = EXCLUDED.truncated, imported_at = now()`, ws, r.ID, d, len(detail), truncated); err != nil {
		return err
	}
	return tx.Commit()
}

func key(r saRow, i int) string {
	if i < len(r.Keys) {
		return r.Keys[i]
	}
	return ""
}

// insertRows writes a day's rows in batches. detail rows carry query and
// page; totals rows only country and device.
func insertRows(ctx context.Context, tx *sql.Tx, ws, resourceID, d string, rows []saRow, detail bool) error {
	const batch = 700
	for start := 0; start < len(rows); start += batch {
		end := start + batch
		if end > len(rows) {
			end = len(rows)
		}
		var sb strings.Builder
		args := []any{ws, resourceID, d}
		if detail {
			sb.WriteString(`INSERT INTO gsc_search_analytics_daily (workspace_id, resource_id, date, query, page, country, device, clicks, impressions, ctr, position) VALUES `)
		} else {
			sb.WriteString(`INSERT INTO gsc_search_totals_daily (workspace_id, resource_id, date, country, device, clicks, impressions, ctr, position) VALUES `)
		}
		for i, r := range rows[start:end] {
			if i > 0 {
				sb.WriteString(",")
			}
			n := len(args)
			if detail {
				fmt.Fprintf(&sb, "($1,$2,$3,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)", n+1, n+2, n+3, n+4, n+5, n+6, n+7, n+8)
				args = append(args, clipText(key(r, 0), 1000), clipText(key(r, 1), 2000), key(r, 2), key(r, 3),
					int64(r.Clicks+0.5), int64(r.Impressions+0.5), r.CTR, r.Position)
			} else {
				fmt.Fprintf(&sb, "($1,$2,$3,$%d,$%d,$%d,$%d,$%d,$%d)", n+1, n+2, n+3, n+4, n+5, n+6)
				args = append(args, key(r, 0), key(r, 1), int64(r.Clicks+0.5), int64(r.Impressions+0.5), r.CTR, r.Position)
			}
		}
		if !detail {
			sb.WriteString(` ON CONFLICT (workspace_id, resource_id, date, country, device) DO UPDATE SET clicks = EXCLUDED.clicks,
				impressions = EXCLUDED.impressions, ctr = EXCLUDED.ctr, position = EXCLUDED.position`)
		}
		if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
			return err
		}
	}
	return nil
}

func clipText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// RegisterSync plugs Search Console into the Google collector: daily.
func RegisterSync() { google.RegisterSyncer(google.KindGSC, Sync, google.Nightly) }
