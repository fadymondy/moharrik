package searchconsole

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
URL inspection and sitemaps (MH-446).

	POST searchconsole/v1/urlInspection/index:inspect   {inspectionUrl, siteUrl, languageCode}
	GET  webmasters/v3/sites/{site}/sitemaps
	PUT  webmasters/v3/sites/{site}/sitemaps/{feedpath}

Google allows 2,000 inspections a day per property for the whole OAuth
project, so every inspection is reserved against a ledger per (Pacific day,
site) BEFORE the call, summed across every workspace that reads the site
(GSC_INSPECTIONS_PER_DAY, default 2,000). The ledger is on the owner pool —
it is a cross-tenant sum by definition — with the site and workspace named in
every statement.

Sitemaps are read live (they are few, and the list is what Google has now).
Submitting needs the full webmasters scope, which the Search Console connect
asks for.
*/

// InspectionCap is the per-site daily inspection budget.
func InspectionCap() int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("GSC_INSPECTIONS_PER_DAY"))); err == nil && v > 0 {
		return v
	}
	return 2000
}

const inspectionLockKey = 4_460_446

// ErrInspectionQuota is a refused reservation.
type ErrInspectionQuota struct{ Used, Cap int }

func (e *ErrInspectionQuota) Error() string {
	return fmt.Sprintf("this site's URL inspections for today are used up (%d of %d); Google's budget resets at midnight Pacific time", e.Used, e.Cap)
}

func (e *ErrInspectionQuota) Is(t error) bool { return t == google.ErrQuota }

// ErrNotInProperty: the URL is not under the site.
var ErrNotInProperty = errors.New("that URL is not part of this Search Console site")

// InspectionQuota is today's use of a site's inspection budget.
type InspectionQuota struct {
	Day   string `json:"day"`
	Used  int    `json:"used" doc:"Across every workspace that reads this site (Google's budget is per site)."`
	Limit int    `json:"limit"`
}

// QuotaFor reads today's use for a site.
func QuotaFor(ctx context.Context, db *sql.DB, site string) (InspectionQuota, error) {
	q := InspectionQuota{Day: time.Now().In(pacific).Format("2006-01-02"), Limit: InspectionCap()}
	err := db.QueryRowContext(ctx, `SELECT coalesce(sum(used), 0) FROM gsc_inspection_quota WHERE day = $1 AND site_url = $2`,
		q.Day, site).Scan(&q.Used)
	return q, err
}

func reserveInspection(ctx context.Context, a *app.App, ws, site string) error {
	day := time.Now().In(pacific).Format("2006-01-02")
	tx, err := a.SysDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, inspectionLockKey); err != nil {
		return err
	}
	var used int
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(used), 0) FROM gsc_inspection_quota WHERE day = $1 AND site_url = $2`,
		day, site).Scan(&used); err != nil {
		return err
	}
	if cap := InspectionCap(); used+1 > cap {
		return &ErrInspectionQuota{Used: used, Cap: cap}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO gsc_inspection_quota (day, site_url, workspace_id, used) VALUES ($1, $2, $3, 1)
		ON CONFLICT (day, site_url, workspace_id) DO UPDATE SET used = gsc_inspection_quota.used + 1, updated_at = now()`,
		day, site, ws); err != nil {
		return err
	}
	return tx.Commit()
}

// InProperty reports whether u is under the site (a URL-prefix property, or
// any host of a domain property).
func InProperty(site, u string) bool {
	x, err := url.Parse(strings.TrimSpace(u))
	if err != nil || (x.Scheme != "http" && x.Scheme != "https") || x.Host == "" {
		return false
	}
	if strings.HasPrefix(site, "sc-domain:") {
		dom := strings.ToLower(strings.TrimPrefix(site, "sc-domain:"))
		h := strings.ToLower(x.Hostname())
		return h == dom || strings.HasSuffix(h, "."+dom)
	}
	return strings.HasPrefix(strings.TrimSpace(u), site)
}

// Inspection is one URL's latest inspection.
type Inspection struct {
	URL             string          `json:"url"`
	Verdict         string          `json:"verdict" doc:"PASS, PARTIAL, FAIL or NEUTRAL, as Google says."`
	CoverageState   string          `json:"coverage_state" doc:"Google's own words, e.g. 'Submitted and indexed'."`
	IndexingState   string          `json:"indexing_state"`
	RobotsTxtState  string          `json:"robots_txt_state"`
	PageFetchState  string          `json:"page_fetch_state"`
	LastCrawlTime   *time.Time      `json:"last_crawl_time"`
	GoogleCanonical string          `json:"google_canonical"`
	UserCanonical   string          `json:"user_canonical"`
	MobileVerdict   string          `json:"mobile_verdict"`
	RichVerdict     string          `json:"rich_results_verdict"`
	ResultLink      string          `json:"result_link" doc:"The inspection in Search Console."`
	Result          json.RawMessage `json:"result"`
	InspectedAt     time.Time       `json:"inspected_at"`
}

// Inspect inspects one URL of an enabled site and stores the answer.
func Inspect(ctx context.Context, a *app.App, ws string, r google.Resource, pageURL, locale, userID string) (*Inspection, error) {
	pageURL = strings.TrimSpace(pageURL)
	if !InProperty(r.ExternalID, pageURL) {
		return nil, ErrNotInProperty
	}
	if err := reserveInspection(ctx, a, ws, r.ExternalID); err != nil {
		return nil, err
	}
	src, err := google.SourceFor(ctx, a, r, ws)
	if err != nil {
		return nil, err
	}
	lang := "en-US"
	if strings.HasPrefix(locale, "ar") {
		lang = "ar"
	}
	var out struct {
		InspectionResult json.RawMessage `json:"inspectionResult"`
	}
	if err := src.Client().Post(ctx, google.API().SearchConsole+"/urlInspection/index:inspect",
		map[string]string{"inspectionUrl": pageURL, "siteUrl": r.ExternalID, "languageCode": lang}, &out); err != nil {
		return nil, err
	}
	var res struct {
		InspectionResultLink string `json:"inspectionResultLink"`
		IndexStatusResult    struct {
			Verdict         string     `json:"verdict"`
			CoverageState   string     `json:"coverageState"`
			RobotsTxtState  string     `json:"robotsTxtState"`
			IndexingState   string     `json:"indexingState"`
			LastCrawlTime   *time.Time `json:"lastCrawlTime"`
			PageFetchState  string     `json:"pageFetchState"`
			GoogleCanonical string     `json:"googleCanonical"`
			UserCanonical   string     `json:"userCanonical"`
		} `json:"indexStatusResult"`
		MobileUsabilityResult struct {
			Verdict string `json:"verdict"`
		} `json:"mobileUsabilityResult"`
		RichResultsResult struct {
			Verdict string `json:"verdict"`
		} `json:"richResultsResult"`
	}
	if len(out.InspectionResult) == 0 {
		out.InspectionResult = json.RawMessage("{}")
	}
	if err := json.Unmarshal(out.InspectionResult, &res); err != nil {
		return nil, fmt.Errorf("google api: unreadable inspection: %w", err)
	}
	ix := res.IndexStatusResult
	in := &Inspection{URL: pageURL, Verdict: ix.Verdict, CoverageState: ix.CoverageState, IndexingState: ix.IndexingState,
		RobotsTxtState: ix.RobotsTxtState, PageFetchState: ix.PageFetchState, LastCrawlTime: ix.LastCrawlTime,
		GoogleCanonical: ix.GoogleCanonical, UserCanonical: ix.UserCanonical, MobileVerdict: res.MobileUsabilityResult.Verdict,
		RichVerdict: res.RichResultsResult.Verdict, ResultLink: res.InspectionResultLink, Result: out.InspectionResult}
	err = a.SQLDB.QueryRowContext(tenant.WithWorkspace(ctx, ws), `
		INSERT INTO gsc_url_inspections (workspace_id, resource_id, url, verdict, coverage_state, indexing_state, robots_txt_state,
			page_fetch_state, last_crawl_time, google_canonical, user_canonical, mobile_verdict, rich_verdict, result_link, result,
			inspected_by, inspected_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,now())
		ON CONFLICT (workspace_id, resource_id, url) DO UPDATE SET verdict = EXCLUDED.verdict,
			coverage_state = EXCLUDED.coverage_state, indexing_state = EXCLUDED.indexing_state,
			robots_txt_state = EXCLUDED.robots_txt_state, page_fetch_state = EXCLUDED.page_fetch_state,
			last_crawl_time = EXCLUDED.last_crawl_time, google_canonical = EXCLUDED.google_canonical,
			user_canonical = EXCLUDED.user_canonical, mobile_verdict = EXCLUDED.mobile_verdict,
			rich_verdict = EXCLUDED.rich_verdict, result_link = EXCLUDED.result_link, result = EXCLUDED.result,
			inspected_by = EXCLUDED.inspected_by, inspected_at = now()
		RETURNING inspected_at`,
		ws, r.ID, pageURL, in.Verdict, in.CoverageState, in.IndexingState, in.RobotsTxtState, in.PageFetchState,
		in.LastCrawlTime, in.GoogleCanonical, in.UserCanonical, in.MobileVerdict, in.RichVerdict, in.ResultLink,
		string(in.Result), userID).Scan(&in.InspectedAt)
	return in, err
}

// ListInspections is a site's latest inspections, newest first.
func ListInspections(ctx context.Context, db *sql.DB, ws, resourceID string, limit int) ([]Inspection, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx, `
		SELECT url, verdict, coverage_state, indexing_state, robots_txt_state, page_fetch_state, last_crawl_time,
		       google_canonical, user_canonical, mobile_verdict, rich_verdict, result_link, result, inspected_at
		FROM gsc_url_inspections WHERE workspace_id = $1 AND resource_id = $2
		ORDER BY inspected_at DESC LIMIT $3`, ws, resourceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Inspection{}
	for rows.Next() {
		var in Inspection
		var raw []byte
		if err := rows.Scan(&in.URL, &in.Verdict, &in.CoverageState, &in.IndexingState, &in.RobotsTxtState, &in.PageFetchState,
			&in.LastCrawlTime, &in.GoogleCanonical, &in.UserCanonical, &in.MobileVerdict, &in.RichVerdict, &in.ResultLink,
			&raw, &in.InspectedAt); err != nil {
			return nil, err
		}
		in.Result = raw
		out = append(out, in)
	}
	return out, rows.Err()
}

// Sitemap is one sitemap as Google lists it. Counts Google does not give are null.
type Sitemap struct {
	Path           string     `json:"path"`
	LastSubmitted  *time.Time `json:"last_submitted"`
	LastDownloaded *time.Time `json:"last_downloaded"`
	IsPending      bool       `json:"is_pending"`
	IsIndex        bool       `json:"is_sitemaps_index"`
	Warnings       *int64     `json:"warnings"`
	Errors         *int64     `json:"errors"`
	Submitted      *int64     `json:"submitted" doc:"URLs submitted (web content), when Google has read it."`
	Indexed        *int64     `json:"indexed" doc:"Google deprecated this and often sends 0 or nothing; shown as given."`
}

func parseCount(s *string) *int64 {
	if s == nil {
		return nil
	}
	n, err := strconv.ParseInt(*s, 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

// Sitemaps lists a site's sitemaps, live from Google.
func Sitemaps(ctx context.Context, a *app.App, ws string, r google.Resource) ([]Sitemap, error) {
	src, err := google.SourceFor(ctx, a, r, ws)
	if err != nil {
		return nil, err
	}
	var out struct {
		Sitemap []struct {
			Path            string     `json:"path"`
			LastSubmitted   *time.Time `json:"lastSubmitted"`
			LastDownloaded  *time.Time `json:"lastDownloaded"`
			IsPending       bool       `json:"isPending"`
			IsSitemapsIndex bool       `json:"isSitemapsIndex"`
			Warnings        *string    `json:"warnings"`
			Errors          *string    `json:"errors"`
			Contents        []struct {
				Type      string  `json:"type"`
				Submitted *string `json:"submitted"`
				Indexed   *string `json:"indexed"`
			} `json:"contents"`
		} `json:"sitemap"`
	}
	if err := src.Client().Get(ctx, google.API().Webmasters+"/sites/"+url.PathEscape(r.ExternalID)+"/sitemaps", &out); err != nil {
		return nil, err
	}
	list := []Sitemap{}
	for _, s := range out.Sitemap {
		m := Sitemap{Path: s.Path, LastSubmitted: s.LastSubmitted, LastDownloaded: s.LastDownloaded, IsPending: s.IsPending,
			IsIndex: s.IsSitemapsIndex, Warnings: parseCount(s.Warnings), Errors: parseCount(s.Errors)}
		for _, c := range s.Contents {
			if c.Type == "web" {
				m.Submitted, m.Indexed = parseCount(c.Submitted), parseCount(c.Indexed)
			}
		}
		list = append(list, m)
	}
	return list, nil
}

// SubmitSitemap tells Google about a sitemap (or asks it to read one again).
func SubmitSitemap(ctx context.Context, a *app.App, ws string, r google.Resource, feed string) error {
	feed = strings.TrimSpace(feed)
	if !InProperty(r.ExternalID, feed) {
		return ErrNotInProperty
	}
	src, err := google.SourceFor(ctx, a, r, ws)
	if err != nil {
		return err
	}
	return src.Client(google.ScopeWebmasters).Do(ctx, "PUT",
		google.API().Webmasters+"/sites/"+url.PathEscape(r.ExternalID)+"/sitemaps/"+url.PathEscape(feed), nil, nil)
}
