package youtube

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
YouTube Analytics (MH-442), reports.query per window:

	GET youtubeanalytics/v2/reports ids=channel==MINE startDate endDate metrics
	    [dimensions filters=video==ID sort maxResults]
	→ {columnHeaders:[{name,columnType,dataType}], rows:[[…]]}  (no "rows" when empty)

For the channel or one video over 7, 28 or 90 days: totals, a daily series,
the top videos (channel only) and traffic sources. Days are Pacific and the
window ends yesterday. Rows are read by header NAME, never position, so the
order Google answers in never matters. Answers are cached in youtube_analytics
for Fresh; when Google fails, the cached answer is served stale with the
reason. This API has its own quota; the Data API ledger is not charged.
*/

// Windows the page offers.
var Windows = []int{7, 28, 90}

// Fresh is how long an Analytics answer is served from the cache.
const Fresh = 6 * time.Hour

// ValidDays reports whether days is offered.
func ValidDays(days int) bool {
	for _, w := range Windows {
		if w == days {
			return true
		}
	}
	return false
}

type column struct {
	Name       string `json:"name"`
	ColumnType string `json:"columnType"`
}

// table is a parsed result table: dimension values as strings, metrics as floats.
type table []map[string]any

func parseTable(body []byte) (table, error) {
	var raw struct {
		ColumnHeaders []column          `json:"columnHeaders"`
		Rows          []json.RawMessage `json:"rows"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("youtube analytics: unreadable answer: %w", err)
	}
	if len(raw.ColumnHeaders) == 0 {
		return nil, errors.New("youtube analytics: the answer has no columns")
	}
	out := table{}
	for _, rr := range raw.Rows {
		var cells []any
		d := json.NewDecoder(bytes.NewReader(rr))
		d.UseNumber()
		if err := d.Decode(&cells); err != nil || len(cells) != len(raw.ColumnHeaders) {
			return nil, errors.New("youtube analytics: a row does not match its columns")
		}
		row := map[string]any{}
		for i, c := range raw.ColumnHeaders {
			switch v := cells[i].(type) {
			case json.Number:
				if c.ColumnType == "DIMENSION" {
					row[c.Name] = v.String()
				} else {
					f, _ := v.Float64()
					row[c.Name] = f
				}
			case string:
				if c.ColumnType == "METRIC" {
					f, _ := strconv.ParseFloat(v, 64)
					row[c.Name] = f
				} else {
					row[c.Name] = v
				}
			case nil:
				if c.ColumnType == "METRIC" {
					row[c.Name] = float64(0)
				} else {
					row[c.Name] = ""
				}
			default:
				row[c.Name] = fmt.Sprint(v)
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func num(r map[string]any, k string) float64 { f, _ := r[k].(float64); return f }
func dim(r map[string]any, k string) string  { s, _ := r[k].(string); return s }

// Window is the [start, end] of a days-long window ending yesterday (Pacific).
func Window(days int, now time.Time) (string, string) {
	end := now.In(pacific).AddDate(0, 0, -1)
	start := end.AddDate(0, 0, -(days - 1))
	return start.Format("2006-01-02"), end.Format("2006-01-02")
}

// YTTotals are a window's sums.
type YTTotals struct {
	Views              float64 `json:"views"`
	WatchMinutes       float64 `json:"watch_minutes"`
	AvgViewDurationSec float64 `json:"avg_view_duration_seconds"`
	SubscribersGained  float64 `json:"subscribers_gained"`
	SubscribersLost    float64 `json:"subscribers_lost"`
	Likes              float64 `json:"likes"`
	Comments           float64 `json:"comments"`
}

// YTDay is one day. YouTube Analytics omits days without views, so a day
// inside the window with no row is 0; the days AFTER the last row are null,
// because YouTube publishes a day's numbers a day or two late and "not yet"
// is not "none".
type YTDay struct {
	Day               string   `json:"day"`
	Views             *float64 `json:"views"`
	WatchMinutes      *float64 `json:"watch_minutes"`
	SubscribersGained *float64 `json:"subscribers_gained"`
	SubscribersLost   *float64 `json:"subscribers_lost"`
}

func fp(v float64) *float64 { return &v }

// YTTopVideo is a video's share of the window.
type YTTopVideo struct {
	VideoID      string  `json:"video_id"`
	Title        string  `json:"title"`
	ThumbnailURL string  `json:"thumbnail_url"`
	Views        float64 `json:"views"`
	WatchMinutes float64 `json:"watch_minutes"`
}

// YTTraffic is views by where they came from.
type YTTraffic struct {
	Source string  `json:"source"`
	Views  float64 `json:"views"`
}

// YTAnalytics is a window's answer for the channel or one video.
type YTAnalytics struct {
	Scope     string       `json:"scope" doc:"'channel' or a video id."`
	Days      int          `json:"days"`
	StartDate string       `json:"start_date"`
	EndDate   string       `json:"end_date"`
	Empty     bool         `json:"empty" doc:"YouTube Analytics has nothing in this window."`
	Totals    YTTotals     `json:"totals"`
	Daily     []YTDay      `json:"daily"`
	TopVideos []YTTopVideo `json:"top_videos"`
	Traffic   []YTTraffic  `json:"traffic_sources"`
	FetchedAt time.Time    `json:"fetched_at"`
	Stale     bool         `json:"stale"`
	Note      string       `json:"note,omitempty"`
}

const totalMetrics = "views,estimatedMinutesWatched,averageViewDuration,subscribersGained,subscribersLost,likes,comments"

func report(ctx context.Context, c *google.Client, q url.Values) (table, error) {
	q.Set("ids", "channel==MINE")
	var raw json.RawMessage
	if err := c.Get(ctx, google.API().YouTubeAnalytics+"/reports?"+q.Encode(), &raw); err != nil {
		return nil, err
	}
	return parseTable(raw)
}

func build(ctx context.Context, a *app.App, ws, resourceID string, c *google.Client, videoID string, days int, now time.Time) (*YTAnalytics, error) {
	start, end := Window(days, now)
	base := func() url.Values {
		q := url.Values{"startDate": {start}, "endDate": {end}}
		if videoID != "" {
			q.Set("filters", "video=="+videoID)
		}
		return q
	}
	scope := "channel"
	if videoID != "" {
		scope = videoID
	}
	out := &YTAnalytics{Scope: scope, Days: days, StartDate: start, EndDate: end, FetchedAt: now.UTC(),
		Daily: []YTDay{}, TopVideos: []YTTopVideo{}, Traffic: []YTTraffic{}}

	q := base()
	q.Set("metrics", totalMetrics)
	t, err := report(ctx, c, q)
	if err != nil {
		return nil, err
	}
	if len(t) > 0 {
		r := t[0]
		out.Totals = YTTotals{Views: num(r, "views"), WatchMinutes: num(r, "estimatedMinutesWatched"),
			AvgViewDurationSec: num(r, "averageViewDuration"), SubscribersGained: num(r, "subscribersGained"),
			SubscribersLost: num(r, "subscribersLost"), Likes: num(r, "likes"), Comments: num(r, "comments")}
	}

	q = base()
	q.Set("dimensions", "day")
	q.Set("metrics", "views,estimatedMinutesWatched,subscribersGained,subscribersLost")
	q.Set("sort", "day")
	if t, err = report(ctx, c, q); err != nil {
		return nil, err
	}
	byDay := map[string]YTDay{}
	last := ""
	for _, r := range t {
		k := dim(r, "day")
		byDay[k] = YTDay{Day: k, Views: fp(num(r, "views")), WatchMinutes: fp(num(r, "estimatedMinutesWatched")),
			SubscribersGained: fp(num(r, "subscribersGained")), SubscribersLost: fp(num(r, "subscribersLost"))}
		if k > last {
			last = k
		}
	}
	s, _ := time.Parse("2006-01-02", start)
	e, _ := time.Parse("2006-01-02", end)
	for d := s; !d.After(e); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		day, ok := byDay[k]
		if !ok {
			day = YTDay{Day: k}
			if last != "" && k < last {
				day.Views, day.WatchMinutes, day.SubscribersGained, day.SubscribersLost = fp(0), fp(0), fp(0), fp(0)
			}
		}
		out.Daily = append(out.Daily, day)
	}
	out.Empty = len(byDay) == 0 && out.Totals == (YTTotals{})

	if videoID == "" {
		q = base()
		q.Set("dimensions", "video")
		q.Set("metrics", "views,estimatedMinutesWatched")
		q.Set("sort", "-views")
		q.Set("maxResults", "10")
		if t, err = report(ctx, c, q); err != nil {
			return nil, err
		}
		for _, r := range t {
			tv := YTTopVideo{VideoID: dim(r, "video"), Views: num(r, "views"), WatchMinutes: num(r, "estimatedMinutesWatched")}
			_ = a.SQLDB.QueryRowContext(ctx, `SELECT title, thumbnail_url FROM youtube_videos
				WHERE workspace_id = $1 AND resource_id = $2 AND video_id = $3`, ws, resourceID, tv.VideoID).Scan(&tv.Title, &tv.ThumbnailURL)
			out.TopVideos = append(out.TopVideos, tv)
		}
	}

	q = base()
	q.Set("dimensions", "insightTrafficSourceType")
	q.Set("metrics", "views")
	q.Set("sort", "-views")
	if t, err = report(ctx, c, q); err != nil {
		return nil, err
	}
	for _, r := range t {
		out.Traffic = append(out.Traffic, YTTraffic{Source: dim(r, "insightTrafficSourceType"), Views: num(r, "views")})
	}
	return out, nil
}

func readCached(ctx context.Context, db *sql.DB, ws, resourceID, scope string, days int) *YTAnalytics {
	var raw []byte
	var fetched time.Time
	if err := db.QueryRowContext(ctx, `SELECT payload, fetched_at FROM youtube_analytics
		WHERE workspace_id = $1 AND resource_id = $2 AND scope = $3 AND days = $4`, ws, resourceID, scope, days).Scan(&raw, &fetched); err != nil {
		return nil
	}
	var out YTAnalytics
	if json.Unmarshal(raw, &out) != nil {
		return nil
	}
	out.FetchedAt = fetched.UTC()
	return &out
}

func fetchAnalytics(ctx context.Context, a *app.App, ws string, r google.Resource, videoID string, days int) (*YTAnalytics, error) {
	src, err := google.SourceFor(ctx, a, r, ws)
	if err != nil {
		return nil, err
	}
	out, err := build(ctx, a, ws, r.ID, src.Client(google.ScopeYTAnalytics), videoID, days, time.Now())
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(out)
	if _, err := a.SQLDB.ExecContext(ctx, `
		INSERT INTO youtube_analytics (workspace_id, resource_id, scope, days, payload, fetched_at) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (workspace_id, resource_id, scope, days) DO UPDATE SET payload = EXCLUDED.payload, fetched_at = EXCLUDED.fetched_at`,
		ws, r.ID, out.Scope, days, string(raw), out.FetchedAt); err != nil {
		return nil, err
	}
	return out, nil
}

// ErrNoAnalytics: Google could not be asked and nothing is cached.
type ErrNoAnalytics struct{ Cause error }

func (e *ErrNoAnalytics) Error() string { return google.Describe(e.Cause) }
func (e *ErrNoAnalytics) Unwrap() error { return e.Cause }

// GetAnalytics answers a window for the channel (videoID "") or one of its
// videos: from the cache when fresh, else from Google; on failure the cached
// answer stale, or ErrNoAnalytics.
func GetAnalytics(ctx context.Context, a *app.App, ws string, r google.Resource, videoID string, days int, refresh bool) (*YTAnalytics, error) {
	wctx := tenant.WithWorkspace(ctx, ws)
	scope := "channel"
	if videoID != "" {
		scope = videoID
		var exists bool
		_ = a.SQLDB.QueryRowContext(wctx, `SELECT true FROM youtube_videos WHERE workspace_id = $1 AND resource_id = $2 AND video_id = $3`,
			ws, r.ID, videoID).Scan(&exists)
		if !exists {
			return nil, ErrNotFound
		}
	}
	cached := readCached(wctx, a.SQLDB, ws, r.ID, scope, days)
	if cached != nil && !refresh && time.Since(cached.FetchedAt) < Fresh {
		return cached, nil
	}
	fresh, err := fetchAnalytics(wctx, a, ws, r, videoID, days)
	if err != nil {
		if cached != nil && !errors.Is(err, context.Canceled) {
			cached.Stale, cached.Note = true, google.Describe(err)
			return cached, nil
		}
		return nil, &ErrNoAnalytics{Cause: err}
	}
	return fresh, nil
}

// refreshChannelSnapshot keeps the channel's 28-day window warm (the
// Overview's). A failure here does not fail the Data API sync.
func refreshChannelSnapshot(ctx context.Context, a *app.App, ws string, r google.Resource) {
	if c := readCached(ctx, a.SQLDB, ws, r.ID, "channel", 28); c != nil && time.Since(c.FetchedAt) < Fresh {
		return
	}
	if _, err := fetchAnalytics(ctx, a, ws, r, "", 28); err != nil && a.Log != nil {
		a.Log.Warn("youtube analytics snapshot failed", "workspace", ws, "err", err)
	}
}
