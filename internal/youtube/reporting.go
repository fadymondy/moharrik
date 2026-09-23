package youtube

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
YouTube Reporting API v1 (MH-443), ported from fadymondy.com's reporting.go:

	GET  youtubereporting/v1/reportTypes
	GET  youtubereporting/v1/jobs
	POST youtubereporting/v1/jobs                 {reportTypeId, name}
	GET  youtubereporting/v1/jobs/{jobId}/reports → reports[]{id, startTime, endTime, createTime, downloadUrl}
	GET  <downloadUrl>                            → the CSV (bearer)

Scope: yt-analytics.readonly, which the YouTube connect already asks for.
Each report covers one day and Google keeps them 60 days, so they are copied
into youtube_reports as they appear. A job is created once per report family
at the newest version Google offers that is not deprecated; jobs already on
the channel (from another workspace, or made by hand) are adopted rather than
duplicated. Google starts producing a new job's reports about two days after
it is created, and the page says so rather than showing an empty list as if
nothing would come. The download URL is followed only on the Reporting API's
own host — the bearer token never goes anywhere else.

The reporting pass runs with a channel's sync, at most every ReportsEvery; it
is its own step and never fails the sync.
*/

// ReportFamilies are the channel reports kept.
var ReportFamilies = []string{"channel_basic", "channel_traffic_source", "channel_device_os"}

// ReportsEvery is how often a reporting pass runs.
const ReportsEvery = 12 * time.Hour

const (
	maxReportBytes     = 20 << 20
	maxDownloadsPerRun = 60
)

type apiReportType struct {
	ID            string `json:"id"`
	DeprecateTime string `json:"deprecateTime"`
	SystemManaged bool   `json:"systemManaged"`
}

// pickReportTypes chooses, per family, the newest non-deprecated version.
func pickReportTypes(types []apiReportType, families []string) map[string]string {
	out := map[string]string{}
	for _, fam := range families {
		var candidates []string
		for _, t := range types {
			if t.DeprecateTime != "" || t.SystemManaged {
				continue
			}
			if rest, ok := strings.CutPrefix(t.ID, fam+"_a"); ok && rest != "" && !strings.Contains(rest, "_") {
				candidates = append(candidates, t.ID)
			}
		}
		if len(candidates) == 0 {
			continue
		}
		sort.Slice(candidates, func(i, j int) bool {
			if len(candidates[i]) != len(candidates[j]) { // a10 after a9
				return len(candidates[i]) < len(candidates[j])
			}
			return candidates[i] < candidates[j]
		})
		out[fam] = candidates[len(candidates)-1]
	}
	return out
}

func familyOf(reportTypeID string) string {
	if i := strings.LastIndex(reportTypeID, "_a"); i > 0 {
		return reportTypeID[:i]
	}
	return reportTypeID
}

type apiJob struct {
	ID           string     `json:"id"`
	ReportTypeID string     `json:"reportTypeId"`
	Name         string     `json:"name"`
	CreateTime   *time.Time `json:"createTime"`
}

func storeJob(ctx context.Context, db *sql.DB, ws, resourceID string, j apiJob, here bool) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO youtube_report_jobs (workspace_id, resource_id, job_id, report_type_id, name, google_created_at, created_here)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (workspace_id, resource_id, job_id) DO UPDATE SET report_type_id = EXCLUDED.report_type_id, name = EXCLUDED.name`,
		ws, resourceID, j.ID, j.ReportTypeID, j.Name, j.CreateTime, here)
	return err
}

// ensureJobs adopts the channel's jobs and creates the missing families.
func ensureJobs(ctx context.Context, a *app.App, ws string, r google.Resource, c *google.Client) ([]string, error) {
	root := google.API().YouTubeReporting
	var jobs struct {
		Jobs []apiJob `json:"jobs"`
	}
	if err := c.Get(ctx, root+"/jobs", &jobs); err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, j := range jobs.Jobs {
		if !contains(ReportFamilies, familyOf(j.ReportTypeID)) {
			continue
		}
		have[familyOf(j.ReportTypeID)] = true
		if err := storeJob(ctx, a.SQLDB, ws, r.ID, j, false); err != nil {
			return nil, err
		}
	}
	missing := []string{}
	for _, f := range ReportFamilies {
		if !have[f] {
			missing = append(missing, f)
		}
	}
	created := []string{}
	if len(missing) == 0 {
		return created, nil
	}
	var types struct {
		ReportTypes []apiReportType `json:"reportTypes"`
	}
	if err := c.Get(ctx, root+"/reportTypes", &types); err != nil {
		return nil, err
	}
	for fam, typeID := range pickReportTypes(types.ReportTypes, missing) {
		var job apiJob
		if err := c.Post(ctx, root+"/jobs", map[string]string{"reportTypeId": typeID, "name": "Moharrik " + fam}, &job); err != nil {
			return created, err
		}
		if job.ID == "" {
			return created, fmt.Errorf("reporting: creating %s returned no job id", typeID)
		}
		if job.ReportTypeID == "" {
			job.ReportTypeID = typeID
		}
		if err := storeJob(ctx, a.SQLDB, ws, r.ID, job, true); err != nil {
			return created, err
		}
		created = append(created, typeID)
	}
	sort.Strings(created)
	return created, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// sameHost reports whether u is on the Reporting API's host.
func sameHost(u string) bool {
	target, err := url.Parse(u)
	if err != nil {
		return false
	}
	root, err := url.Parse(google.API().YouTubeReporting)
	if err != nil {
		return false
	}
	return strings.EqualFold(target.Scheme, root.Scheme) && strings.EqualFold(target.Host, root.Host)
}

// downloadReports copies every report not stored yet.
func downloadReports(ctx context.Context, a *app.App, ws string, r google.Resource, c *google.Client) (int, error) {
	rows, err := a.SQLDB.QueryContext(ctx, `SELECT job_id, report_type_id FROM youtube_report_jobs
		WHERE workspace_id = $1 AND resource_id = $2 ORDER BY job_id`, ws, r.ID)
	if err != nil {
		return 0, err
	}
	type job struct{ id, typeID string }
	var jobs []job
	for rows.Next() {
		var j job
		if rows.Scan(&j.id, &j.typeID) == nil {
			jobs = append(jobs, j)
		}
	}
	rows.Close()

	downloaded := 0
	for _, j := range jobs {
		page := ""
		for pages := 0; pages < 10; pages++ {
			u := google.API().YouTubeReporting + "/jobs/" + url.PathEscape(j.id) + "/reports"
			if page != "" {
				u += "?" + url.Values{"pageToken": {page}}.Encode()
			}
			var list struct {
				Reports []struct {
					ID          string     `json:"id"`
					StartTime   *time.Time `json:"startTime"`
					EndTime     *time.Time `json:"endTime"`
					CreateTime  *time.Time `json:"createTime"`
					DownloadURL string     `json:"downloadUrl"`
				} `json:"reports"`
				NextPageToken string `json:"nextPageToken"`
			}
			if err := c.Get(ctx, u, &list); err != nil {
				return downloaded, err
			}
			for _, rep := range list.Reports {
				if downloaded >= maxDownloadsPerRun {
					return downloaded, nil
				}
				if rep.ID == "" {
					continue
				}
				var exists bool
				_ = a.SQLDB.QueryRowContext(ctx, `SELECT true FROM youtube_reports WHERE workspace_id = $1 AND resource_id = $2 AND report_id = $3`,
					ws, r.ID, rep.ID).Scan(&exists)
				if exists {
					continue
				}
				if !sameHost(rep.DownloadURL) {
					return downloaded, fmt.Errorf("reporting: report %s has a download URL off the Reporting API's host; not followed", rep.ID)
				}
				body, err := c.GetRaw(ctx, rep.DownloadURL, maxReportBytes)
				if err != nil {
					return downloaded, err
				}
				lines := int64(0)
				if trimmed := bytes.TrimRight(body, "\n"); len(trimmed) > 0 {
					lines = int64(bytes.Count(trimmed, []byte("\n"))) // rows after the header
				}
				if _, err := a.SQLDB.ExecContext(ctx, `
					INSERT INTO youtube_reports (workspace_id, resource_id, report_id, job_id, report_type_id, start_time, end_time,
						google_created_at, content, row_count, bytes)
					VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (workspace_id, resource_id, report_id) DO NOTHING`,
					ws, r.ID, rep.ID, j.id, j.typeID, rep.StartTime, rep.EndTime, rep.CreateTime,
					strings.ToValidUTF8(string(body), ""), lines, len(body)); err != nil {
					return downloaded, err
				}
				downloaded++
			}
			if list.NextPageToken == "" {
				break
			}
			page = list.NextPageToken
		}
	}
	return downloaded, nil
}

// refreshReports is the reporting step of a channel's sync: at most every
// ReportsEvery (force skips the wait), jobs ensured, new reports downloaded.
func refreshReports(ctx context.Context, a *app.App, ws string, r google.Resource, force bool) error {
	if !force {
		var last *time.Time
		_ = a.SQLDB.QueryRowContext(ctx, `SELECT reports_checked_at FROM youtube_channels WHERE workspace_id = $1 AND resource_id = $2`,
			ws, r.ID).Scan(&last)
		if last != nil && time.Since(*last) < ReportsEvery {
			return nil
		}
	}
	src, err := google.SourceFor(ctx, a, r, ws)
	if err != nil {
		return err
	}
	c := src.Client(google.ScopeYTAnalytics)
	if _, err := ensureJobs(ctx, a, ws, r, c); err != nil {
		return err
	}
	if _, err := downloadReports(ctx, a, ws, r, c); err != nil {
		return err
	}
	_, err = a.SQLDB.ExecContext(ctx, `UPDATE youtube_channels SET reports_checked_at = now() WHERE workspace_id = $1 AND resource_id = $2`, ws, r.ID)
	return err
}

// YTReportJob is one report job on the channel.
type YTReportJob struct {
	JobID        string     `json:"job_id"`
	ReportTypeID string     `json:"report_type_id"`
	Name         string     `json:"name"`
	CreatedAt    *time.Time `json:"created_at" doc:"When Google created the job; its first reports come about two days later."`
	CreatedHere  bool       `json:"created_here"`
	Reports      int64      `json:"reports"`
	LatestDay    *time.Time `json:"latest_day" doc:"The newest day a stored report covers."`
}

// YTStoredReport is a downloaded report without its content.
type YTStoredReport struct {
	ReportID     string     `json:"report_id"`
	ReportTypeID string     `json:"report_type_id"`
	StartTime    *time.Time `json:"start_time"`
	EndTime      *time.Time `json:"end_time"`
	RowCount     int64      `json:"row_count"`
	Bytes        int64      `json:"bytes"`
	DownloadedAt time.Time  `json:"downloaded_at"`
}

// YTReports is the reports panel.
type YTReports struct {
	Jobs      []YTReportJob    `json:"jobs"`
	Reports   []YTStoredReport `json:"reports"`
	CheckedAt *time.Time       `json:"checked_at" doc:"The last reporting pass; null when none has run."`
}

// ListReports lists the channel's jobs and its newest stored reports.
func ListReports(ctx context.Context, db *sql.DB, ws, resourceID string, limit int) (*YTReports, error) {
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	out := &YTReports{Jobs: []YTReportJob{}, Reports: []YTStoredReport{}}
	_ = db.QueryRowContext(ctx, `SELECT reports_checked_at FROM youtube_channels WHERE workspace_id = $1 AND resource_id = $2`,
		ws, resourceID).Scan(&out.CheckedAt)
	rows, err := db.QueryContext(ctx, `
		SELECT j.job_id, j.report_type_id, j.name, j.google_created_at, j.created_here, count(r.report_id), max(r.start_time)
		FROM youtube_report_jobs j
		LEFT JOIN youtube_reports r ON r.workspace_id = j.workspace_id AND r.resource_id = j.resource_id AND r.job_id = j.job_id
		WHERE j.workspace_id = $1 AND j.resource_id = $2
		GROUP BY j.job_id, j.report_type_id, j.name, j.google_created_at, j.created_here ORDER BY j.report_type_id`, ws, resourceID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var j YTReportJob
		if err := rows.Scan(&j.JobID, &j.ReportTypeID, &j.Name, &j.CreatedAt, &j.CreatedHere, &j.Reports, &j.LatestDay); err != nil {
			rows.Close()
			return nil, err
		}
		out.Jobs = append(out.Jobs, j)
	}
	rows.Close()
	rows, err = db.QueryContext(ctx, `
		SELECT report_id, report_type_id, start_time, end_time, row_count, bytes, downloaded_at
		FROM youtube_reports WHERE workspace_id = $1 AND resource_id = $2
		ORDER BY start_time DESC NULLS LAST, report_type_id LIMIT $3`, ws, resourceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r YTStoredReport
		if err := rows.Scan(&r.ReportID, &r.ReportTypeID, &r.StartTime, &r.EndTime, &r.RowCount, &r.Bytes, &r.DownloadedAt); err != nil {
			return nil, err
		}
		out.Reports = append(out.Reports, r)
	}
	return out, rows.Err()
}

// ReportCSV is one stored report's content and a file name for it.
func ReportCSV(ctx context.Context, db *sql.DB, ws, resourceID, id string) (string, string, error) {
	var content, typeID string
	var start *time.Time
	err := db.QueryRowContext(ctx, `SELECT content, report_type_id, start_time FROM youtube_reports
		WHERE workspace_id = $1 AND resource_id = $2 AND report_id = $3`, ws, resourceID, id).Scan(&content, &typeID, &start)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	day := "undated"
	if start != nil {
		day = start.UTC().Format("2006-01-02")
	}
	return content, fmt.Sprintf("%s-%s.csv", typeID, day), nil
}

// RefreshReports runs a reporting pass now (the page's "Check now").
func RefreshReports(ctx context.Context, a *app.App, ws string, r google.Resource) error {
	return refreshReports(tenant.WithWorkspace(ctx, ws), a, ws, r, true)
}
