package google_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/google/googletest"
	"github.com/fadymondy/moharrik/internal/youtube"
)

/*
MH-441: the YouTube channel, uploads and comments against the fake Data API,
the two-level quota ledger, and new comments in the notification center.
*/

type channelOut struct {
	Resource *google.Resource   `json:"resource"`
	Channel  *youtube.YTChannel `json:"channel"`
	Quota    youtube.YTQuota    `json:"quota"`
}

func getJSON[T any](t *testing.T, c *client, path string) T {
	t.Helper()
	code, b, _ := c.do(t, "GET", path, nil)
	if code != 200 {
		t.Fatalf("%s: %d %s", path, code, b)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func dataCalls(f *fixture) int {
	n := 0
	for _, api := range []string{"channels", "playlistItems", "videos", "commentThreads"} {
		n += f.fake.Calls("api:" + api)
	}
	return n
}

func TestYouTube(t *testing.T) {
	f := newFixture(t)
	t.Setenv("GOOGLE_SYNC_INLINE", "")
	t.Setenv("YOUTUBE_QUOTA_PER_WORKSPACE", "")
	t.Setenv("YOUTUBE_QUOTA_PER_DAY", "")
	// Comments are read with an API key: Google refuses them to a read-only
	// user token (see TestYouTubeWithoutAPIKey).
	t.Setenv("YOUTUBE_API_KEY", f.fake.APIKey)
	ctx := context.Background()

	alice := f.newUser(t)
	wsID, _ := alice.workspace(t)
	const sub = "sub-yt"
	f.setAccount(sub, "yt@example.test")
	// Page the uploads two at a time, so the walk is exercised.
	googletest.YTPageSize = 2
	defer func() { googletest.YTPageSize = 50 }()
	d := f.fake.Data(sub)
	f.fake.Set(func(*googletest.Server) {
		d.Channel.Videos[2].Likes = -1                 // likes hidden
		d.Channel.Videos[3].CommentsDisabled = true    // comments off
		d.Channel.Videos[4].Title = "Pricing workshop" // for the search
	})

	if r, _ := alice.connect(t, "product=youtube"); r != google.ResultConnected {
		t.Fatal(r)
	}
	google.WaitBackground()
	ch := alice.resources(t, "?kind=yt_channel").Resources[0]

	t.Run("the channel, and every unit of the Data API on the ledger", func(t *testing.T) {
		out := getJSON[channelOut](t, alice, "/api/w/youtube/channel")
		if out.Channel == nil || out.Channel.Title != "Demo Channel" || out.Channel.Subscribers != 12840 || out.Channel.Videos != 6 || !out.Channel.UploadsComplete {
			t.Fatalf("%+v", out.Channel)
		}
		if ch.LastError != "" || ch.LastSyncedAt == nil {
			t.Errorf("%+v", ch)
		}
		if out.Channel.Uploads != 6 || out.Channel.PublicUploads != 6 {
			t.Errorf("uploads read: %d (%d public)", out.Channel.Uploads, out.Channel.PublicUploads)
		}
		for _, st := range out.Channel.Steps {
			if st.State != "ok" {
				t.Errorf("step %+v", st)
			}
		}
		if len(out.Channel.Steps) != 5 {
			t.Errorf("five steps: %+v", out.Channel.Steps)
		}
		if int(out.Quota.WorkspaceUsed) != dataCalls(f) || out.Quota.WorkspaceLimit != 2000 || out.Quota.GlobalLimit != 9000 {
			t.Errorf("ledger %+v vs %d Data API calls", out.Quota, dataCalls(f))
		}
	})

	t.Run("every upload, newest first, with Google's gaps kept as gaps", func(t *testing.T) {
		page := getJSON[youtube.YTVideoPage](t, alice, "/api/w/youtube/videos")
		if len(page.Videos) != 6 || page.Total != 6 || page.Counts["public"] != 6 {
			t.Fatalf("%+v", page)
		}
		for i := 1; i < len(page.Videos); i++ {
			if page.Videos[i].PublishedAt.After(*page.Videos[i-1].PublishedAt) {
				t.Error("not newest first")
			}
		}
		if page.Videos[2].Likes != nil || page.Videos[3].Comments != nil || page.Videos[0].Likes == nil || page.Videos[0].DurationSeconds == 0 {
			t.Errorf("hidden likes / comments off must be null, not 0: %+v %+v", page.Videos[2].Likes, page.Videos[3].Comments)
		}
		s := getJSON[youtube.YTVideoPage](t, alice, "/api/w/youtube/videos?q=pricing")
		if len(s.Videos) != 2 { // "Five pricing mistakes" and "Pricing workshop"
			t.Errorf("search: %d", len(s.Videos))
		}
		p1 := getJSON[youtube.YTVideoPage](t, alice, "/api/w/youtube/videos?limit=4")
		p2 := getJSON[youtube.YTVideoPage](t, alice, "/api/w/youtube/videos?limit=4&cursor="+p1.NextCursor)
		if !p1.HasMore || len(p1.Videos) != 4 || len(p2.Videos) != 2 || p2.HasMore || p2.Videos[0].ID == p1.Videos[3].ID {
			t.Errorf("paging: %d %v %d %v", len(p1.Videos), p1.HasMore, len(p2.Videos), p2.HasMore)
		}
	})

	t.Run("a video's top comments; comments off is said, not an error", func(t *testing.T) {
		page := getJSON[youtube.YTVideoPage](t, alice, "/api/w/youtube/videos")
		first := getJSON[youtube.YTVideoDetail](t, alice, "/api/w/youtube/videos/"+page.Videos[0].ID)
		if len(first.TopComments) != 2 || first.TopComments[0].Likes < first.TopComments[1].Likes || !strings.Contains(first.TopComments[0].ReplyURL, "&lc=") {
			t.Fatalf("%+v", first.TopComments)
		}
		off := getJSON[youtube.YTVideoDetail](t, alice, "/api/w/youtube/videos/"+page.Videos[3].ID)
		if off.CommentNote != "comments_disabled" || len(off.TopComments) != 0 {
			t.Errorf("%+v", off)
		}
		if code, _, _ := alice.do(t, "GET", "/api/w/youtube/videos/nope", nil); code != 404 {
			t.Errorf("unknown video: %d", code)
		}
	})

	t.Run("the first sync seeds the watermark; a new comment is announced once", func(t *testing.T) {
		var n int
		_ = f.a.SysDB.QueryRow(`SELECT count(*) FROM notification_items WHERE workspace_id = $1 AND source_name = 'YouTube'`, wsID).Scan(&n)
		if n != 0 {
			t.Fatalf("connecting must not flood the center: %d", n)
		}
		vid := d.Channel.Videos[0]
		f.fake.AddComment(sub, googletest.YTComment{ID: "UgwNEW1", VideoID: vid.ID, Author: "Nour", AuthorChannelID: "UCnour",
			Text: "When is the next one?", PublishedAt: time.Now().UTC()})
		f.fake.AddComment(sub, googletest.YTComment{ID: "UgwOWN1", VideoID: vid.ID, Author: "Demo Channel", AuthorChannelID: d.Channel.ID,
			Text: "Pinned: links below", PublishedAt: time.Now().UTC()})
		for i := 0; i < 2; i++ {
			if err := google.RunSync(ctx, f.a, wsID, ch.ID); err != nil {
				t.Fatal(err)
			}
		}
		var title, body, actor, url, kind string
		_ = f.a.SysDB.QueryRow(`SELECT count(*) FROM notification_items WHERE workspace_id = $1 AND source_name = 'YouTube'`, wsID).Scan(&n)
		if err := f.a.SysDB.QueryRow(`SELECT title, body, actor, url, kind FROM notification_items WHERE workspace_id = $1 AND source_name = 'YouTube'`,
			wsID).Scan(&title, &body, &actor, &url, &kind); err != nil || n != 1 {
			t.Fatalf("one item, not the channel's own comment, not twice: %d %v", n, err)
		}
		if !strings.Contains(title, vid.Title) || body != "When is the next one?" || actor != "Nour" || !strings.Contains(url, "lc=UgwNEW1") || kind != "youtube_comment" {
			t.Errorf("%q %q %q %q %q", title, body, actor, url, kind)
		}
	})

	t.Run("a channel with no uploads is zero videos, not an error", func(t *testing.T) {
		bob := f.newUser(t)
		bob.workspace(t)
		f.fake.SetData("sub-yt-empty", googletest.Empty(googletest.Account{Sub: "sub-yt-empty"}))
		f.setAccount("sub-yt-empty", "yt-empty@example.test")
		if r, _ := bob.connect(t, "product=youtube"); r != google.ResultConnected {
			t.Fatal(r)
		}
		google.WaitBackground()
		out := getJSON[channelOut](t, bob, "/api/w/youtube/channel")
		if out.Channel == nil || out.Channel.Videos != 0 || !out.Channel.UploadsComplete || out.Resource.LastError != "" {
			t.Fatalf("%+v %+v", out.Channel, out.Resource)
		}
		if page := getJSON[youtube.YTVideoPage](t, bob, "/api/w/youtube/videos"); len(page.Videos) != 0 {
			t.Errorf("%+v", page)
		}
	})

	t.Run("quota: a workspace's share, then the project's cap, refuse before calling", func(t *testing.T) {
		used := getJSON[channelOut](t, alice, "/api/w/youtube/channel").Quota.WorkspaceUsed
		t.Setenv("YOUTUBE_QUOTA_PER_WORKSPACE", itoa(used+2))
		before := dataCalls(f)
		err := google.RunSync(ctx, f.a, wsID, ch.ID)
		if err == nil || !strings.Contains(err.Error(), "this workspace has used its YouTube quota") {
			t.Fatalf("workspace cap: %v", err)
		}
		if got := dataCalls(f) - before; got != 2 {
			t.Errorf("exactly the reserved units were spent: %d calls", got)
		}
		r := alice.resources(t, "?kind=yt_channel").Resources[0]
		if !strings.Contains(r.LastError, "quota") || r.BackoffUntil == nil {
			t.Errorf("recorded and backed off: %+v", r)
		}
		q := getJSON[channelOut](t, alice, "/api/w/youtube/channel").Quota
		if q.WorkspaceUsed != used+2 {
			t.Errorf("the ledger holds exactly what was spent: %d, want %d", q.WorkspaceUsed, used+2)
		}
		// The project's cap binds even with a workspace's share left.
		t.Setenv("YOUTUBE_QUOTA_PER_WORKSPACE", "")
		global := q.GlobalUsed
		t.Setenv("YOUTUBE_QUOTA_PER_DAY", itoa(global))
		_, _ = f.a.SysDB.Exec(`UPDATE google_resources SET backoff_until = NULL WHERE id = $1`, ch.ID)
		before = dataCalls(f)
		if err := google.RunSync(ctx, f.a, wsID, ch.ID); err == nil || !strings.Contains(err.Error(), "shared YouTube quota") {
			t.Fatalf("global cap: %v", err)
		}
		if dataCalls(f) != before {
			t.Error("a refused reservation must not call YouTube")
		}
	})

	t.Run("roles and isolation", func(t *testing.T) {
		m := f.newUser(t)
		addMember(t, f, wsID, m.email, "member")
		m.ws = wsID
		if out := getJSON[channelOut](t, m, "/api/w/youtube/channel"); out.Channel == nil {
			t.Error("a member reads the channel")
		}
		eve := f.newUser(t)
		eve.workspace(t)
		for _, p := range []string{"/api/w/youtube/videos?resource=" + ch.ID, "/api/w/youtube/videos/" + d.Channel.Videos[0].ID + "?resource=" + ch.ID} {
			if code, b, _ := eve.do(t, "GET", p, nil); code != 404 || strings.Contains(string(b), d.Channel.Videos[0].ID) {
				t.Errorf("%s from another workspace: %d %s", p, code, b)
			}
		}
		var ov struct {
			YouTube []struct {
				Subscribers *int64 `json:"subscribers"`
				Views28     *int64 `json:"views_28"`
			} `json:"youtube"`
		}
		ov = getJSON[struct {
			YouTube []struct {
				Subscribers *int64 `json:"subscribers"`
				Views28     *int64 `json:"views_28"`
			} `json:"youtube"`
		}](t, alice, "/api/w/overview")
		if len(ov.YouTube) != 1 || ov.YouTube[0].Subscribers == nil || *ov.YouTube[0].Subscribers != 12840 {
			t.Errorf("overview youtube: %+v", ov.YouTube)
		}
		if _, b, _ := eve.do(t, "GET", "/api/w/overview", nil); !strings.Contains(string(b), `"youtube":null`) {
			t.Errorf("another workspace's overview has no YouTube card: %s", b)
		}
		if out := getJSON[channelOut](t, eve, "/api/w/youtube/channel"); out.Resource != nil || out.Quota.WorkspaceUsed != 0 {
			t.Errorf("another workspace's quota and channel: %+v", out)
		}
	})
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

type ytAnalyticsOut struct {
	Resource  *google.Resource     `json:"resource"`
	Analytics *youtube.YTAnalytics `json:"analytics"`
	Error     string               `json:"error"`
}

// MH-442: YouTube Analytics for the channel and one video, cached six hours.
func TestYouTubeAnalytics(t *testing.T) {
	f := newFixture(t)
	t.Setenv("GOOGLE_SYNC_INLINE", "")
	alice := f.newUser(t)
	alice.workspace(t)
	f.setAccount("sub-yta", "yta@example.test")
	if r, _ := alice.connect(t, "product=youtube"); r != google.ResultConnected {
		t.Fatal(r)
	}
	google.WaitBackground()
	d := f.fake.Data("sub-yta")

	t.Run("the sync kept the 28-day snapshot the Overview reads", func(t *testing.T) {
		ov := getJSON[struct {
			YouTube []struct {
				Views28   *int64  `json:"views_28"`
				Views28To *string `json:"views_28_to"`
			} `json:"youtube"`
		}](t, alice, "/api/w/overview")
		if len(ov.YouTube) != 1 || ov.YouTube[0].Views28 == nil || *ov.YouTube[0].Views28 <= 0 || ov.YouTube[0].Views28To == nil {
			t.Fatalf("%+v", ov.YouTube)
		}
	})

	t.Run("the channel over 28 days: totals, a series that adds up, top videos, traffic", func(t *testing.T) {
		calls := f.fake.Calls("api:ytAnalytics")
		out := getJSON[ytAnalyticsOut](t, alice, "/api/w/youtube/analytics?days=28")
		if f.fake.Calls("api:ytAnalytics") != calls {
			t.Error("the snapshot is fresh: no call")
		}
		a := out.Analytics
		if a == nil || a.Empty || a.Stale || len(a.Daily) != 28 || a.Totals.Views <= 0 {
			t.Fatalf("%+v", out)
		}
		last := a.Daily[27]
		if last.Views != nil {
			t.Errorf("yesterday is not published yet: it must be unknown, not a number: %v", *last.Views)
		}
		var sum float64
		for _, day := range a.Daily {
			if day.Views != nil {
				sum += *day.Views
			}
		}
		if sum != a.Totals.Views {
			t.Errorf("the series adds up to the total: %v vs %v", sum, a.Totals.Views)
		}
		if len(a.TopVideos) != 6 || a.TopVideos[0].Title == "" || a.TopVideos[0].Views < a.TopVideos[1].Views {
			t.Errorf("top videos %+v", a.TopVideos)
		}
		if len(a.Traffic) != 7 || a.Traffic[0].Source != "YT_SEARCH" {
			t.Errorf("traffic %+v", a.Traffic)
		}
	})

	t.Run("7 days: fetched, then cached; refresh asks again", func(t *testing.T) {
		calls := f.fake.Calls("api:ytAnalytics")
		getJSON[ytAnalyticsOut](t, alice, "/api/w/youtube/analytics?days=7")
		getJSON[ytAnalyticsOut](t, alice, "/api/w/youtube/analytics?days=7")
		if n := f.fake.Calls("api:ytAnalytics") - calls; n != 4 {
			t.Errorf("one fetch is four reports, then the cache: %d calls", n)
		}
		getJSON[ytAnalyticsOut](t, alice, "/api/w/youtube/analytics?days=7&refresh=true")
		if n := f.fake.Calls("api:ytAnalytics") - calls; n != 8 {
			t.Errorf("refresh: %d calls", n)
		}
	})

	t.Run("one video", func(t *testing.T) {
		v := d.Channel.Videos[1].ID
		a := getJSON[ytAnalyticsOut](t, alice, "/api/w/youtube/analytics?days=28&video="+v).Analytics
		if a == nil || a.Scope != v || len(a.TopVideos) != 0 || a.Totals.Views <= 0 {
			t.Fatalf("%+v", a)
		}
		if code, _, _ := alice.do(t, "GET", "/api/w/youtube/analytics?video=nope", nil); code != 404 {
			t.Errorf("unknown video: %d", code)
		}
	})

	t.Run("Google failing: the cached answer, stale, with the reason; nothing cached, the reason", func(t *testing.T) {
		f.fake.Fail("ytAnalytics", 403, "forbidden", -1)
		defer f.fake.ClearFailures()
		out := getJSON[ytAnalyticsOut](t, alice, "/api/w/youtube/analytics?days=28&refresh=true")
		if out.Analytics == nil || !out.Analytics.Stale || !strings.Contains(out.Analytics.Note, "refused access") {
			t.Fatalf("%+v", out.Analytics)
		}
		none := getJSON[ytAnalyticsOut](t, alice, "/api/w/youtube/analytics?days=90")
		if none.Analytics != nil || !strings.Contains(none.Error, "refused access") {
			t.Errorf("%+v", none)
		}
	})

	t.Run("a channel YouTube Analytics has nothing for", func(t *testing.T) {
		bob := f.newUser(t)
		bob.workspace(t)
		f.fake.SetData("sub-yta-empty", googletest.Empty(googletest.Account{Sub: "sub-yta-empty"}))
		f.setAccount("sub-yta-empty", "yta-empty@example.test")
		if r, _ := bob.connect(t, "product=youtube"); r != google.ResultConnected {
			t.Fatal(r)
		}
		google.WaitBackground()
		a := getJSON[ytAnalyticsOut](t, bob, "/api/w/youtube/analytics?days=28").Analytics
		if a == nil || !a.Empty || a.Totals.Views != 0 {
			t.Fatalf("%+v", a)
		}
		for _, day := range a.Daily {
			if day.Views != nil {
				t.Fatalf("no data must not be drawn as zeros: %+v", day)
			}
		}
	})
}

// Production, 2026-09-23: Google refused commentThreads to the owner's
// youtube.readonly token (403 insufficientPermissions). Without an API key the
// comments step says why, and nothing else fails.
func TestYouTubeWithoutAPIKey(t *testing.T) {
	f := newFixture(t)
	t.Setenv("GOOGLE_SYNC_INLINE", "")
	t.Setenv("YOUTUBE_API_KEY", "")
	alice := f.newUser(t)
	alice.workspace(t)
	f.setAccount("sub-nokey", "nokey@example.test")
	if r, _ := alice.connect(t, "product=youtube"); r != google.ResultConnected {
		t.Fatal(r)
	}
	google.WaitBackground()

	t.Run("the fake refuses comments to a read-only token, as Google does", func(t *testing.T) {
		ch := alice.resources(t, "?kind=yt_channel").Resources[0]
		conns := alice.list(t).Connections
		s, err := google.TokenSource(context.Background(), f.a, alice.ws, conns[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		err = s.Client(google.ScopeYouTube).Get(context.Background(), google.API().YouTube+"/commentThreads?part=snippet&allThreadsRelatedToChannelId="+ch.ExternalID, nil)
		var ae *google.APIError
		if !errors.As(err, &ae) || ae.Status != 403 || ae.Reason != "insufficientPermissions" {
			t.Fatalf("want 403 insufficientPermissions, got %v", err)
		}
	})

	t.Run("the sync succeeds, and the comments step says why it could not run", func(t *testing.T) {
		out := getJSON[channelOut](t, alice, "/api/w/youtube/channel")
		if out.Resource.LastError != "" || out.Channel == nil || out.Channel.Uploads != 6 {
			t.Fatalf("one step must not fail the sync: %+v %+v", out.Resource, out.Channel)
		}
		states := map[string]youtube.YTSyncStep{}
		for _, st := range out.Channel.Steps {
			states[st.Name] = st
		}
		if states["channel"].State != "ok" || states["uploads"].State != "ok" || states["analytics"].State != "ok" {
			t.Errorf("%+v", out.Channel.Steps)
		}
		if c := states["comments"]; c.State != "unavailable" || !strings.Contains(c.Error, "YOUTUBE_API_KEY") {
			t.Errorf("comments: %+v", c)
		}
		if f.fake.Calls("api:commentThreads") != 1 { // only the probe above
			t.Errorf("no comment call is spent without a key: %d", f.fake.Calls("api:commentThreads"))
		}
		page := getJSON[youtube.YTVideoPage](t, alice, "/api/w/youtube/videos")
		d := getJSON[youtube.YTVideoDetail](t, alice, "/api/w/youtube/videos/"+page.Videos[0].ID)
		if d.CommentNote != "comments_unavailable" {
			t.Errorf("the sheet says why: %q", d.CommentNote)
		}
	})

	t.Run("a failing step is failed on its own, with Google's words", func(t *testing.T) {
		t.Setenv("YOUTUBE_API_KEY", f.fake.APIKey)
		f.fake.Fail("commentThreads", 403, "forbidden", -1)
		defer f.fake.ClearFailures()
		ch := alice.resources(t, "?kind=yt_channel").Resources[0]
		if err := google.RunSync(context.Background(), f.a, alice.ws, ch.ID); err != nil {
			t.Fatalf("the sync must not fail for its comments: %v", err)
		}
		out := getJSON[channelOut](t, alice, "/api/w/youtube/channel")
		for _, st := range out.Channel.Steps {
			if st.Name == "comments" && (st.State != "failed" || !strings.Contains(st.Error, "refused access")) {
				t.Errorf("%+v", st)
			}
		}
		if out.Resource.LastError != "" {
			t.Errorf("last_error is for the channel and uploads only: %q", out.Resource.LastError)
		}
	})
}

// MH-443: YouTube Reporting — jobs created once and adopted, reports copied
// as Google publishes them, downloadable as CSV.
func TestYouTubeReporting(t *testing.T) {
	f := newFixture(t)
	t.Setenv("GOOGLE_SYNC_INLINE", "")
	t.Setenv("YOUTUBE_API_KEY", f.fake.APIKey)
	alice := f.newUser(t)
	wsID, _ := alice.workspace(t)
	const sub = "sub-rep"
	f.setAccount(sub, "rep@example.test")
	if r, _ := alice.connect(t, "product=youtube"); r != google.ResultConnected {
		t.Fatal(r)
	}
	google.WaitBackground()
	ch := alice.resources(t, "?kind=yt_channel").Resources[0]

	t.Run("the first sync creates one job per family, newest version; no reports yet, and that is said", func(t *testing.T) {
		out := getJSON[youtube.YTReports](t, alice, "/api/w/youtube/reports")
		types := []string{}
		for _, j := range out.Jobs {
			types = append(types, j.ReportTypeID)
			if !j.CreatedHere || j.CreatedAt == nil || j.Reports != 0 {
				t.Errorf("%+v", j)
			}
		}
		if strings.Join(types, ",") != "channel_basic_a3,channel_device_os_a2,channel_traffic_source_a2" {
			t.Errorf("jobs: %v", types)
		}
		if len(out.Reports) != 0 || out.CheckedAt == nil {
			t.Errorf("%+v", out)
		}
	})

	t.Run("published reports are copied, once, and download as CSV", func(t *testing.T) {
		f.fake.PublishReports(sub, 5)
		code, b, _ := alice.do(t, "POST", "/api/w/youtube/reports/refresh", nil)
		if code != 200 {
			t.Fatalf("%d %s", code, b)
		}
		var out youtube.YTReports
		_ = json.Unmarshal(b, &out)
		if len(out.Reports) != 15 {
			t.Fatalf("3 families × 5 days: %d", len(out.Reports))
		}
		var basic youtube.YTStoredReport
		for _, r := range out.Reports {
			if strings.HasPrefix(r.ReportTypeID, "channel_basic") {
				basic = r
				break
			}
		}
		if basic.RowCount != 6 || basic.StartTime == nil {
			t.Errorf("one row per video: %+v", basic)
		}
		code, csv, res := alice.do(t, "GET", "/api/w/youtube/reports/"+basic.ReportID+"/csv", nil)
		if code != 200 || !strings.HasPrefix(string(csv), "date,channel_id,video_id") || !strings.Contains(res.Header.Get("Content-Disposition"), "channel_basic_a3-") {
			t.Errorf("csv: %d %q %s", code, res.Header.Get("Content-Disposition"), csv[:min(80, len(csv))])
		}
		calls := f.fake.Calls("api:ytReporting")
		alice.do(t, "POST", "/api/w/youtube/reports/refresh", nil)
		again := getJSON[youtube.YTReports](t, alice, "/api/w/youtube/reports")
		if len(again.Reports) != 15 || f.fake.Calls("api:ytReporting")-calls != 4 { // jobs + 3 lists, no downloads
			t.Errorf("a second pass downloads nothing again: %d reports, %d calls", len(again.Reports), f.fake.Calls("api:ytReporting")-calls)
		}
	})

	t.Run("another workspace on the same channel adopts the jobs rather than duplicating them", func(t *testing.T) {
		bob := f.newUser(t)
		bob.workspace(t)
		if r, _ := bob.connect(t, "product=youtube"); r != google.ResultConnected {
			t.Fatal(r)
		}
		google.WaitBackground()
		out := getJSON[youtube.YTReports](t, bob, "/api/w/youtube/reports")
		if len(out.Jobs) != 3 || out.Jobs[0].CreatedHere || len(out.Reports) != 15 {
			t.Errorf("%+v", out)
		}
		if code, _, _ := bob.do(t, "GET", "/api/w/youtube/reports/nope/csv", nil); code != 404 {
			t.Errorf("unknown report: %d", code)
		}
	})

	t.Run("a member reads and downloads but does not act on YouTube; a failing pass does not fail the sync", func(t *testing.T) {
		m := f.newUser(t)
		addMember(t, f, wsID, m.email, "member")
		m.ws = wsID
		if out := getJSON[youtube.YTReports](t, m, "/api/w/youtube/reports"); len(out.Reports) != 15 {
			t.Errorf("member list: %d", len(out.Reports))
		}
		if code, _, _ := m.do(t, "POST", "/api/w/youtube/reports/refresh", nil); code != 403 {
			t.Errorf("member refresh: %d", code)
		}
		_, _ = f.a.SysDB.Exec(`UPDATE youtube_channels SET reports_checked_at = NULL WHERE resource_id = $1`, ch.ID)
		f.fake.Fail("ytReporting", 403, "forbidden", -1)
		defer f.fake.ClearFailures()
		if err := google.RunSync(context.Background(), f.a, wsID, ch.ID); err != nil {
			t.Fatalf("a failing reporting pass must not fail the sync: %v", err)
		}
		out := getJSON[channelOut](t, alice, "/api/w/youtube/channel")
		found := false
		for _, st := range out.Channel.Steps {
			if st.Name == "reports" {
				found = true
				if st.State != "failed" || !strings.Contains(st.Error, "refused access") {
					t.Errorf("%+v", st)
				}
			}
		}
		if !found || out.Resource.LastError != "" {
			t.Errorf("steps %+v, last_error %q", out.Channel.Steps, out.Resource.LastError)
		}
	})
}
