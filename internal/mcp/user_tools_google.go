package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/moharrik/internal/analytics"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/searchconsole"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/youtube"
)

/*
GoogleTools (MH-447): the workspace's Google data over MCP — the same
functions the Analytics, Search Console and YouTube pages call, on the app
pool inside the token's workspace, reading only what the workspace has
switched on in Settings → Integrations.

	analytics:read          google_resources, ga4_report, gsc_performance,
	                        gsc_inspections, gsc_sitemaps, youtube_channel,
	                        youtube_videos, youtube_video, youtube_analytics,
	                        youtube_reports
	search_console:write    gsc_inspect_url, gsc_submit_sitemap (manager: they
	                        spend the site's shared inspection budget and act
	                        on Google)

Every answer says where its numbers came from and when, and says so plainly
when there is no data, a stored answer is stale, or Google refused — never a
guessed number. A resource argument is optional: without it the first enabled
property, site or channel is used.
*/
func GoogleTools() []Tool {
	resourceProp := prop("string", "Resource id (from google_resources); default the first enabled one")
	daysProp := map[string]any{"type": "integer", "enum": []int{7, 28, 90}, "description": "Window in days (default 28)"}
	days := func(args map[string]any) (int, error) {
		d := intArg(args, "days", 28)
		if d != 7 && d != 28 && d != 90 {
			return 0, errors.New("days must be 7, 28 or 90")
		}
		return d, nil
	}
	resolve := func(ctx context.Context, a *app.App, kind google.Kind, args map[string]any) (*tenant.Scope, *google.Resource, error) {
		s, ok := tenant.From(ctx)
		if !ok {
			return nil, nil, errNotFound
		}
		r, err := google.ResolveEnabled(ctx, a, s.WorkspaceID, kind, str(args, "resource"))
		if errors.Is(err, google.ErrResourceNotFound) {
			return nil, nil, fmt.Errorf("that %s is not enabled in this workspace (see google_resources)", kindName(kind))
		}
		if err != nil {
			return nil, nil, err
		}
		if r == nil {
			return nil, nil, fmt.Errorf("no %s is enabled in this workspace; an admin switches one on in Settings → Integrations", kindName(kind))
		}
		return s, r, nil
	}
	return []Tool{
		{
			Name:        "google_resources",
			Description: "The GA4 properties, Search Console sites and YouTube channels this workspace reads (enabled ones), with their ids, last sync and any error.",
			Scope:       "analytics:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				list, err := google.ListResources(ctx, a, s.WorkspaceID, google.Filter{EnabledOnly: true})
				if err != nil {
					return "", err
				}
				items := make([]map[string]any, 0, len(list))
				for _, r := range list {
					items = append(items, map[string]any{
						"id": r.ID, "kind": kindName(r.Kind), "name": r.DisplayName, "external_id": r.ExternalID,
						"last_synced_at": r.LastSyncedAt, "needs_reconnecting": r.Reauth, "last_error": r.LastError,
					})
				}
				return jsonResult(map[string]any{"count": len(items), "items": items})
			},
		},
		{
			Name:        "ga4_report",
			Description: "GA4 for an enabled property over 7, 28 or 90 days (ending yesterday, property time zone): users, sessions, views, engagement, the daily trend, top pages and source / medium. Cached an hour.",
			Scope:       "analytics:read",
			InputSchema: schema(nil, map[string]any{"resource": resourceProp, "days": daysProp}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				d, err := days(args)
				if err != nil {
					return "", err
				}
				s, r, err := resolve(ctx, a, google.KindGA4, args)
				if err != nil {
					return "", err
				}
				rep, err := analytics.Get(ctx, a, s.WorkspaceID, r, d, false)
				if err != nil {
					return "", fmt.Errorf("GA4 could not be read: %s", google.Describe(err))
				}
				out := map[string]any{
					"resource": map[string]any{"id": r.ID, "name": r.DisplayName, "external_id": r.ExternalID},
					"start_date": rep.StartDate, "end_date": rep.EndDate, "timezone": rep.TimeZone,
					"stale": rep.Stale, "note": rep.Note, "fetched_at": rep.FetchedAt, "empty": rep.Empty,
				}
				if !rep.Empty {
					t := rep.Totals
					out["totals"] = map[string]any{
						"users": t.Users, "new_users": t.NewUsers, "sessions": t.Sessions, "views": t.Views,
						"engagement_rate": t.EngagementRate, "avg_engagement_per_user_sec": t.AvgEngagementPerUse,
					}
					out["daily"] = rep.Daily
					out["top_pages"] = rep.TopPages
					out["sources"] = rep.Sources
				}
				return jsonResult(out)
			},
		},
		{
			Name: "gsc_performance",
			Description: "Search Console for an enabled site over 7, 28 or 90 days ending on the newest day with data (it lags 2–3 days): clicks, impressions, CTR, average position, " +
				"optionally against the period before, with top queries and pages. Filters: query and page (contains), country (alpha-3, e.g. egy), device (DESKTOP, MOBILE, TABLET).",
			Scope: "analytics:read",
			InputSchema: schema(nil, map[string]any{"resource": resourceProp, "days": daysProp,
				"compare": prop("boolean", "Also the same length of time just before"),
				"query":   prop("string", "Query contains"), "page": prop("string", "Page URL contains"),
				"country": prop("string", "ISO 3166-1 alpha-3, lower case"),
				"device":  map[string]any{"type": "string", "enum": []string{"DESKTOP", "MOBILE", "TABLET"}}}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				d, err := days(args)
				if err != nil {
					return "", err
				}
				s, r, err := resolve(ctx, a, google.KindGSC, args)
				if err != nil {
					return "", err
				}
				compare, _ := args["compare"].(bool)
				dev := strings.ToUpper(str(args, "device"))
				if dev != "" && dev != "DESKTOP" && dev != "MOBILE" && dev != "TABLET" {
					return "", errors.New("device must be DESKTOP, MOBILE or TABLET")
				}
				p, err := searchconsole.Report(tenant.WithWorkspace(ctx, s.WorkspaceID), a, s.WorkspaceID, r.ID, d, compare,
					searchconsole.Filters{Query: str(args, "query"), Page: str(args, "page"), Country: str(args, "country"), Device: dev})
				if err != nil {
					return "", err
				}
				out := map[string]any{"site": r.ExternalID, "nothing_yet": p.NothingYet}
				if p.NothingYet {
					out["last_error"] = r.LastError
					return jsonResult(out)
				}
				out["start_date"], out["end_date"], out["imported_days"] = p.StartDate, p.EndDate, p.Coverage.ImportedDays
				out["basis"], out["missing_days"], out["empty"] = p.Basis, p.Missing, p.Empty
				out["totals"] = map[string]any{"clicks": p.Totals.Clicks, "impressions": p.Totals.Impressions, "ctr": p.Totals.CTR, "position": p.Totals.Position}
				if p.Previous != nil {
					prev := map[string]any{"start_date": p.PrevStartDate, "end_date": p.PrevEndDate, "missing_days": p.PreviousMissing}
					if p.PreviousMissing == 0 {
						prev["clicks"], prev["impressions"], prev["ctr"], prev["position"] = p.Previous.Clicks, p.Previous.Impressions, p.Previous.CTR, p.Previous.Position
					}
					out["previous"] = prev
				}
				if !p.Empty {
					top := func(list []searchconsole.SearchRow) []searchconsole.SearchRow {
						if len(list) > 15 {
							return list[:15]
						}
						return list
					}
					out["top_queries"] = top(p.TopQueries)
					out["top_pages"] = top(p.TopPages)
				}
				return jsonResult(out)
			},
		},
		{
			Name:        "gsc_inspections",
			Description: "The latest URL inspections of an enabled site and today's inspection budget (2,000 a day per site, shared by every workspace reading it).",
			Scope:       "analytics:read",
			InputSchema: schema(nil, map[string]any{"resource": resourceProp}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, r, err := resolve(ctx, a, google.KindGSC, decodeArgs(raw))
				if err != nil {
					return "", err
				}
				list, err := searchconsole.ListInspections(tenant.WithWorkspace(ctx, s.WorkspaceID), a.SQLDB, s.WorkspaceID, r.ID, 30)
				if err != nil {
					return "", err
				}
				q, err := searchconsole.QuotaFor(ctx, a.SysDB, r.ExternalID)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{
					"site": r.ExternalID, "quota_used_today": q.Used, "quota_limit": q.Limit,
					"count": len(list), "items": list,
				})
			},
		},
		{
			Name:        "gsc_inspect_url",
			Description: "Ask Google's URL Inspection how it sees one page of an enabled site: indexed or not, coverage, last crawl, canonical. Spends one of the site's 2,000 daily inspections.",
			Scope:       "search_console:write",
			InputSchema: schema([]string{"url"}, map[string]any{"resource": resourceProp, "url": prop("string", "An absolute URL under the site")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				s, r, err := resolve(ctx, a, google.KindGSC, args)
				if err != nil {
					return "", err
				}
				in, err := searchconsole.Inspect(ctx, a, s.WorkspaceID, *r, str(args, "url"), "en", s.UserID)
				if err != nil {
					return "", errors.New(google.Describe(err))
				}
				return jsonResult(map[string]any{"inspection": in})
			},
		},
		{
			Name:        "gsc_sitemaps",
			Description: "An enabled site's sitemaps as Google lists them now.",
			Scope:       "analytics:read",
			InputSchema: schema(nil, map[string]any{"resource": resourceProp}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, r, err := resolve(ctx, a, google.KindGSC, decodeArgs(raw))
				if err != nil {
					return "", err
				}
				list, err := searchconsole.Sitemaps(ctx, a, s.WorkspaceID, *r)
				if err != nil {
					return "", errors.New(google.Describe(err))
				}
				return jsonResult(map[string]any{"site": r.ExternalID, "count": len(list), "items": list})
			},
		},
		{
			Name:        "gsc_submit_sitemap",
			Description: "Submit a sitemap of an enabled site to Google, or ask Google to read it again. Returns the site's sitemap list read back.",
			Scope:       "search_console:write",
			InputSchema: schema([]string{"url"}, map[string]any{"resource": resourceProp, "url": prop("string", "The sitemap's absolute URL, under the site")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				s, r, err := resolve(ctx, a, google.KindGSC, args)
				if err != nil {
					return "", err
				}
				if err := searchconsole.SubmitSitemap(ctx, a, s.WorkspaceID, *r, str(args, "url")); err != nil {
					return "", errors.New(google.Describe(err))
				}
				list, err := searchconsole.Sitemaps(ctx, a, s.WorkspaceID, *r)
				if err != nil {
					return jsonResult(map[string]any{"ok": true, "site": r.ExternalID, "read_back_error": google.Describe(err)})
				}
				return jsonResult(map[string]any{"ok": true, "site": r.ExternalID, "count": len(list), "items": list})
			},
		},
		{
			Name:        "youtube_channel",
			Description: "An enabled YouTube channel as last synced: subscribers, lifetime views, uploads read, each sync step's state, and today's YouTube quota.",
			Scope:       "analytics:read",
			InputSchema: schema(nil, map[string]any{"resource": resourceProp}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, r, err := resolve(ctx, a, google.KindYT, decodeArgs(raw))
				if err != nil {
					return "", err
				}
				c, err := youtube.GetChannel(tenant.WithWorkspace(ctx, s.WorkspaceID), a.SQLDB, s.WorkspaceID, r.ID)
				if err != nil {
					return "", err
				}
				q, err := youtube.QuotaFor(ctx, a.SysDB, s.WorkspaceID)
				if err != nil {
					return "", err
				}
				quota := map[string]any{
					"day": q.Day, "workspace_used": q.WorkspaceUsed, "workspace_limit": q.WorkspaceLimit,
					"global_used": q.GlobalUsed, "global_limit": q.GlobalLimit,
				}
				if c == nil {
					out := map[string]any{"synced": false, "quota": quota}
					if r.LastError != "" {
						out["last_error"] = r.LastError
					}
					return jsonResult(out)
				}
				return jsonResult(map[string]any{
					"synced": true, "channel": c, "quota": quota,
				})
			},
		},
		{
			Name:        "youtube_videos",
			Description: "An enabled channel's uploads, newest first. Optional q (title, description, tags), privacy (public, unlisted, private, scheduled), limit (max 50) and cursor (from the previous page).",
			Scope:       "analytics:read",
			InputSchema: schema(nil, map[string]any{"resource": resourceProp, "q": prop("string", "Search"),
				"privacy": map[string]any{"type": "string", "enum": []string{"public", "unlisted", "private", "scheduled"}},
				"limit":   prop("integer", "At most this many (default 24, max 50)"), "cursor": prop("string", "next_cursor of the previous page")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				s, r, err := resolve(ctx, a, google.KindYT, args)
				if err != nil {
					return "", err
				}
				page, err := youtube.ListVideos(tenant.WithWorkspace(ctx, s.WorkspaceID), a.SQLDB, s.WorkspaceID, r.ID, youtube.YTVideoFilter{
					Query: str(args, "q"), Privacy: str(args, "privacy"), Cursor: str(args, "cursor"), Limit: intArg(args, "limit", 24)})
				var inv youtube.ErrInvalid
				if errors.As(err, &inv) {
					return "", inv
				}
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{
					"total": page.Total, "counts": page.Counts, "count": len(page.Videos), "items": page.Videos, "next_cursor": page.NextCursor,
				})
			},
		},
		{
			Name:        "youtube_video",
			Description: "One upload of an enabled channel with its top comments (re-read from YouTube when older than an hour and the quota allows).",
			Scope:       "analytics:read",
			InputSchema: schema([]string{"id"}, map[string]any{"resource": resourceProp, "id": prop("string", "The video id")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				s, r, err := resolve(ctx, a, google.KindYT, args)
				if err != nil {
					return "", err
				}
				d, err := youtube.GetVideo(tenant.WithWorkspace(ctx, s.WorkspaceID), a, s.WorkspaceID, *r, str(args, "id"))
				if errors.Is(err, youtube.ErrNotFound) {
					return "", errNotFound
				}
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"video": d})
			},
		},
		{
			Name:        "youtube_reports",
			Description: "YouTube Reporting for an enabled channel: its daily report jobs and the reports copied so far (newest first). A report's CSV is at GET /api/w/youtube/reports/{report_id}/csv.",
			Scope:       "analytics:read",
			InputSchema: schema(nil, map[string]any{"resource": resourceProp, "limit": prop("integer", "At most this many reports (default 30, max 200)")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				s, r, err := resolve(ctx, a, google.KindYT, args)
				if err != nil {
					return "", err
				}
				out, err := youtube.ListReports(tenant.WithWorkspace(ctx, s.WorkspaceID), a.SQLDB, s.WorkspaceID, r.ID, intArg(args, "limit", 30))
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{
					"checked_at": out.CheckedAt, "jobs": out.Jobs, "count": len(out.Reports), "items": out.Reports,
				})
			},
		},
		{
			Name:        "youtube_analytics",
			Description: "YouTube Analytics for an enabled channel, or one of its videos (video), over 7, 28 or 90 days ending yesterday (Pacific): views, watch time, subscribers, likes, comments, top videos, traffic sources. Cached 6 hours.",
			Scope:       "analytics:read",
			InputSchema: schema(nil, map[string]any{"resource": resourceProp, "days": daysProp, "video": prop("string", "A video id; empty for the channel")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				args := decodeArgs(raw)
				d, err := days(args)
				if err != nil {
					return "", err
				}
				s, r, err := resolve(ctx, a, google.KindYT, args)
				if err != nil {
					return "", err
				}
				an, err := youtube.GetAnalytics(ctx, a, s.WorkspaceID, *r, str(args, "video"), d, false)
				if errors.Is(err, youtube.ErrNotFound) {
					return "", errNotFound
				}
				if err != nil {
					return "", fmt.Errorf("YouTube Analytics could not be read: %s", google.Describe(err))
				}
				out := map[string]any{
					"scope": an.Scope, "start_date": an.StartDate, "end_date": an.EndDate,
					"stale": an.Stale, "note": an.Note, "fetched_at": an.FetchedAt, "empty": an.Empty,
				}
				if !an.Empty {
					t := an.Totals
					out["totals"] = map[string]any{
						"views": t.Views, "watch_minutes": t.WatchMinutes, "avg_view_duration_sec": t.AvgViewDurationSec,
						"subscribers_gained": t.SubscribersGained, "subscribers_lost": t.SubscribersLost, "likes": t.Likes, "comments": t.Comments,
					}
					out["daily"] = an.Daily
					out["top_videos"] = an.TopVideos
					out["traffic"] = an.Traffic
				}
				return jsonResult(out)
			},
		},
	}
}

func kindName(k google.Kind) string {
	switch k {
	case google.KindGA4:
		return "GA4 property"
	case google.KindGSC:
		return "Search Console site"
	}
	return "YouTube channel"
}
