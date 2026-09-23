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
	                        youtube_videos, youtube_video, youtube_analytics
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
	n := func(v int64) string { return commas(v) }

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
				if len(list) == 0 {
					return "Nothing is enabled. An admin connects Google and switches on properties, sites or channels in Settings → Integrations.", nil
				}
				var b strings.Builder
				for _, r := range list {
					fmt.Fprintf(&b, "- %s: %s (%s) id=%s", kindName(r.Kind), r.DisplayName, r.ExternalID, r.ID)
					if r.LastSyncedAt != nil {
						fmt.Fprintf(&b, " · synced %s", r.LastSyncedAt.UTC().Format("2006-01-02 15:04 UTC"))
					} else {
						b.WriteString(" · not synced yet")
					}
					if r.Reauth {
						b.WriteString(" · NEEDS RECONNECTING")
					}
					if r.LastError != "" {
						fmt.Fprintf(&b, "\n  last error: %s", r.LastError)
					}
					b.WriteString("\n")
				}
				return b.String(), nil
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
				var b strings.Builder
				fmt.Fprintf(&b, "GA4 · %s (%s) · %s to %s (%s)\n", r.DisplayName, r.ExternalID, rep.StartDate, rep.EndDate, rep.TimeZone)
				stale(&b, rep.Stale, rep.Note, rep.FetchedAt.UTC().Format("2006-01-02 15:04 UTC"))
				if rep.Empty {
					b.WriteString("GA4 has no data for this property in this window.\n")
					return b.String(), nil
				}
				t := rep.Totals
				fmt.Fprintf(&b, "users %s (new %s) · sessions %s · views %s · engagement rate %.1f%% · %.0fs engaged per user\n",
					n(t.Users), n(t.NewUsers), n(t.Sessions), n(t.Views), t.EngagementRate*100, t.AvgEngagementPerUse)
				b.WriteString("daily (date users/sessions/views):")
				for _, day := range rep.Daily {
					fmt.Fprintf(&b, " %s %d/%d/%d;", day.Date[5:], day.Users, day.Sessions, day.Views)
				}
				b.WriteString("\ntop pages (views, users):\n")
				for _, p := range rep.TopPages {
					fmt.Fprintf(&b, "  %s  %s, %s\n", p.Path, n(p.Views), n(p.Users))
				}
				b.WriteString("source / medium (sessions, users):\n")
				for _, x := range rep.Sources {
					fmt.Fprintf(&b, "  %s / %s  %s, %s\n", x.Source, x.Medium, n(x.Sessions), n(x.Users))
				}
				return b.String(), nil
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
				var b strings.Builder
				fmt.Fprintf(&b, "Search Console · %s\n", r.ExternalID)
				if p.NothingYet {
					if r.LastError != "" {
						return b.String() + "Nothing imported yet. The last import failed: " + r.LastError + "\n", nil
					}
					return b.String() + "Nothing imported yet: the first import is running or has not started.\n", nil
				}
				fmt.Fprintf(&b, "%s to %s (newest day with data) · %d days imported\n", p.StartDate, p.EndDate, p.Coverage.ImportedDays)
				if p.Basis == "queries" {
					b.WriteString("Filtered by query or page: these totals are sums of named queries and leave anonymised ones out.\n")
				}
				if p.Missing > 0 {
					fmt.Fprintf(&b, "%d days of this window are not imported yet: the totals are partial.\n", p.Missing)
				}
				pos := func(v *float64) string {
					if v == nil {
						return "—"
					}
					return fmt.Sprintf("%.1f", *v)
				}
				t := p.Totals
				fmt.Fprintf(&b, "clicks %s · impressions %s · CTR %.2f%% · average position %s\n", n(t.Clicks), n(t.Impressions), t.CTR*100, pos(t.Position))
				if p.Previous != nil {
					if p.PreviousMissing > 0 {
						fmt.Fprintf(&b, "before (%s to %s): still importing (%d days to go), not compared\n", p.PrevStartDate, p.PrevEndDate, p.PreviousMissing)
					} else {
						q := p.Previous
						fmt.Fprintf(&b, "before (%s to %s): clicks %s · impressions %s · CTR %.2f%% · average position %s\n",
							p.PrevStartDate, p.PrevEndDate, n(q.Clicks), n(q.Impressions), q.CTR*100, pos(q.Position))
					}
				}
				if p.Empty {
					b.WriteString("No clicks or impressions in this window.\n")
					return b.String(), nil
				}
				b.WriteString("top queries (clicks, impressions, position):\n")
				for i, x := range p.TopQueries {
					if i == 15 {
						break
					}
					fmt.Fprintf(&b, "  %s  %s, %s, %s\n", x.Key, n(x.Clicks), n(x.Impressions), pos(x.Position))
				}
				b.WriteString("top pages (clicks, impressions, position):\n")
				for i, x := range p.TopPages {
					if i == 15 {
						break
					}
					fmt.Fprintf(&b, "  %s  %s, %s, %s\n", x.Key, n(x.Clicks), n(x.Impressions), pos(x.Position))
				}
				return b.String(), nil
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
				var b strings.Builder
				fmt.Fprintf(&b, "%s · inspections today %d of %d\n", r.ExternalID, q.Used, q.Limit)
				if len(list) == 0 {
					b.WriteString("No pages inspected yet.\n")
				}
				for _, in := range list {
					fmt.Fprintf(&b, "- %s: %s — %s (inspected %s)\n", in.URL, in.Verdict, in.CoverageState, in.InspectedAt.UTC().Format("2006-01-02 15:04 UTC"))
				}
				return b.String(), nil
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
				var b strings.Builder
				fmt.Fprintf(&b, "%s\nverdict %s — %s\n", in.URL, in.Verdict, in.CoverageState)
				if in.LastCrawlTime != nil {
					fmt.Fprintf(&b, "last crawled %s · fetch %s · robots.txt %s\n", in.LastCrawlTime.UTC().Format("2006-01-02 15:04 UTC"), in.PageFetchState, in.RobotsTxtState)
				} else {
					b.WriteString("not crawled\n")
				}
				if in.GoogleCanonical != "" || in.UserCanonical != "" {
					fmt.Fprintf(&b, "Google's canonical %s · declared %s\n", firstNonEmpty(in.GoogleCanonical, "—"), firstNonEmpty(in.UserCanonical, "—"))
				}
				if in.ResultLink != "" {
					fmt.Fprintf(&b, "in Search Console: %s\n", in.ResultLink)
				}
				return b.String(), nil
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
				return sitemapText(r.ExternalID, list), nil
			},
		},
		{
			Name:        "gsc_submit_sitemap",
			Description: "Submit a sitemap of an enabled site to Google, or ask Google to read it again.",
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
					return "Submitted. The list could not be read back: " + google.Describe(err), nil
				}
				return "Submitted.\n" + sitemapText(r.ExternalID, list), nil
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
				if c == nil {
					if r.LastError != "" {
						return "The channel has not been read yet. The last sync failed: " + r.LastError, nil
					}
					return "The channel has not been read yet: its first sync is running or has not started.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%s %s (%s) · %s\n", c.Title, c.Handle, c.ChannelID, c.URL)
				if c.SubscribersHidden {
					b.WriteString("subscribers hidden")
				} else {
					fmt.Fprintf(&b, "subscribers %s", n(c.Subscribers))
				}
				fmt.Fprintf(&b, " · lifetime views %s · uploads read %d (%d public; YouTube's own counter says %d)\n", n(c.Views), c.Uploads, c.PublicUploads, c.Videos)
				fmt.Fprintf(&b, "synced %s\n", c.UpdatedAt.UTC().Format("2006-01-02 15:04 UTC"))
				for _, st := range c.Steps {
					if st.State != "ok" {
						fmt.Fprintf(&b, "step %s: %s — %s\n", st.Name, st.State, st.Error)
					}
				}
				fmt.Fprintf(&b, "YouTube quota today (%s, Pacific): %d of %d units for this workspace, %d of %d for all of Moharrik\n",
					q.Day, q.WorkspaceUsed, q.WorkspaceLimit, q.GlobalUsed, q.GlobalLimit)
				return b.String(), nil
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
				var b strings.Builder
				fmt.Fprintf(&b, "%d uploads match (public %d, unlisted %d, private %d, scheduled %d)\n", page.Total,
					page.Counts["public"], page.Counts["unlisted"], page.Counts["private"], page.Counts["scheduled"])
				for _, v := range page.Videos {
					likes, comments := "hidden", "off"
					if v.Likes != nil {
						likes = n(*v.Likes)
					}
					if v.Comments != nil {
						comments = n(*v.Comments)
					}
					when := "—"
					if v.PublishedAt != nil {
						when = v.PublishedAt.UTC().Format("2006-01-02")
					}
					fmt.Fprintf(&b, "- %s [%s] %s · %s views · %s likes · %s comments · id=%s\n", when, v.PrivacyStatus, v.Title, n(v.Views), likes, comments, v.ID)
				}
				if page.HasMore {
					fmt.Fprintf(&b, "more: cursor=%s\n", page.NextCursor)
				}
				return b.String(), nil
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
				var b strings.Builder
				fmt.Fprintf(&b, "%s [%s] · %s views · %s\n", d.Title, d.PrivacyStatus, n(d.Views), d.URL)
				switch d.CommentNote {
				case "":
				case "comments_disabled":
					b.WriteString("Comments are turned off for this video.\n")
				case "comments_unavailable":
					b.WriteString("Comments cannot be read on this server: it has no YouTube API key (YOUTUBE_API_KEY).\n")
				case "comments_not_public":
					b.WriteString("This video is not public, so its comments cannot be read with read-only access.\n")
				default:
					fmt.Fprintf(&b, "Comments may be old: %s\n", d.CommentNote)
				}
				for _, c := range d.TopComments {
					ans := ""
					if c.Answered {
						ans = " (answered)"
					}
					fmt.Fprintf(&b, "- %s%s · %d likes · %d replies: %s\n  reply: %s\n", c.Author, ans, c.Likes, c.Replies, firstLine(c.Text), c.ReplyURL)
				}
				return b.String(), nil
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
				var b strings.Builder
				fmt.Fprintf(&b, "YouTube Analytics · %s · %s to %s\n", an.Scope, an.StartDate, an.EndDate)
				stale(&b, an.Stale, an.Note, an.FetchedAt.UTC().Format("2006-01-02 15:04 UTC"))
				if an.Empty {
					b.WriteString("YouTube Analytics has nothing for this window.\n")
					return b.String(), nil
				}
				t := an.Totals
				fmt.Fprintf(&b, "views %.0f · watch time %.1f h · average view %.0fs · subscribers +%.0f −%.0f · likes %.0f · comments %.0f\n",
					t.Views, t.WatchMinutes/60, t.AvgViewDurationSec, t.SubscribersGained, t.SubscribersLost, t.Likes, t.Comments)
				pending := 0
				b.WriteString("daily views:")
				for _, day := range an.Daily {
					if day.Views == nil {
						pending++
						continue
					}
					fmt.Fprintf(&b, " %s %.0f;", day.Day[5:], *day.Views)
				}
				b.WriteString("\n")
				if pending > 0 {
					fmt.Fprintf(&b, "(the last %d days are not published by YouTube yet)\n", pending)
				}
				for _, v := range an.TopVideos {
					fmt.Fprintf(&b, "top: %s — %.0f views · id=%s\n", firstNonEmpty(v.Title, v.VideoID), v.Views, v.VideoID)
				}
				for _, x := range an.Traffic {
					fmt.Fprintf(&b, "traffic: %s %.0f\n", x.Source, x.Views)
				}
				return b.String(), nil
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

func stale(b *strings.Builder, isStale bool, note, fetched string) {
	if isStale {
		fmt.Fprintf(b, "STALE: Google could not be asked just now (%s); these are the numbers fetched %s.\n", note, fetched)
	} else {
		fmt.Fprintf(b, "fetched %s\n", fetched)
	}
}

func sitemapText(site string, list []searchconsole.Sitemap) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %d sitemap(s)\n", site, len(list))
	for _, m := range list {
		state := "read"
		if m.IsPending {
			state = "pending"
		}
		urls := "—"
		if m.Submitted != nil {
			urls = commas(*m.Submitted)
		}
		fmt.Fprintf(&b, "- %s · %s · %s URLs", m.Path, state, urls)
		if m.Errors != nil && *m.Errors > 0 {
			fmt.Fprintf(&b, " · %d errors", *m.Errors)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// commas formats 12345 as 12,345.
func commas(v int64) string {
	s := fmt.Sprint(v)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}
