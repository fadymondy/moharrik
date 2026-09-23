package google_test

import (
	"context"
	"encoding/json"
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
