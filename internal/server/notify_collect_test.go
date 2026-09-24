package server

/*
TestNotificationCollection — collected sources, search, facets, paging and
per-source mark-all-read (MH-384), through the real stack against fake
Bluesky / YouTube / RSS servers (never the real ones).

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run NotificationCollection -v
*/

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/notifications"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/testdb"
)

// fakePublic is Bluesky's AppView, YouTube's channel pages and feeds, and an
// RSS feed, in one server. replies grows between polls to model new activity.
type fakePublic struct {
	*httptest.Server
	replies atomic.Int32
}

func newFakePublic() *fakePublic {
	f := &fakePublic{}
	f.replies.Store(2)
	mux := http.NewServeMux()
	mux.HandleFunc("/xrpc/app.bsky.actor.getProfile", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("actor") != "shop.bsky.social" {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"InvalidRequest","message":"Profile not found"}`)
			return
		}
		fmt.Fprint(w, `{"did":"did:plc:shop","handle":"shop.bsky.social","displayName":"Nile Shop"}`)
	})
	mux.HandleFunc("/xrpc/app.bsky.feed.getAuthorFeed", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"feed":[
			{"post":{"uri":"at://did:plc:shop/app.bsky.feed.post/p1","author":{"did":"did:plc:shop","handle":"shop.bsky.social"},"record":{"text":"New roast is out","createdAt":"2026-09-20T10:00:00Z"},"replyCount":3}},
			{"post":{"uri":"at://did:plc:shop/app.bsky.feed.post/p2","author":{"did":"did:plc:shop","handle":"shop.bsky.social"},"record":{"text":"No replies here","createdAt":"2026-09-19T10:00:00Z"},"replyCount":0}}]}`)
	})
	mux.HandleFunc("/xrpc/app.bsky.feed.getPostThread", func(w http.ResponseWriter, r *http.Request) {
		n := int(f.replies.Load())
		var rs []string
		for i := 1; i <= n; i++ {
			rs = append(rs, fmt.Sprintf(`{"post":{"uri":"at://did:plc:fan%d/app.bsky.feed.post/r%d","author":{"did":"did:plc:fan%d","handle":"fan%d.bsky.social","displayName":"Fan %d"},"record":{"text":"Ordering a kilo of this %d","createdAt":"2026-09-20T1%d:00:00Z"}}}`, i, i, i, i, i, i, i%10))
		}
		// The account answering its own thread is not a notification.
		rs = append(rs, `{"post":{"uri":"at://did:plc:shop/app.bsky.feed.post/self","author":{"did":"did:plc:shop","handle":"shop.bsky.social"},"record":{"text":"Thanks all!","createdAt":"2026-09-20T12:00:00Z"}}}`)
		fmt.Fprintf(w, `{"thread":{"post":{},"replies":[%s]}}`, strings.Join(rs, ","))
	})
	mux.HandleFunc("/@nilecoffee", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><script>var ytInitialData = {"metadata":{"channelMetadataRenderer":{"externalId":"UCabcdefghijklmnopqrstuv"}}}</script></html>`)
	})
	mux.HandleFunc("/feeds/videos.xml", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("channel_id") != "UCabcdefghijklmnopqrstuv" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/atom+xml")
		fmt.Fprint(w, `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><title>Nile Coffee</title>
			<entry><id>yt:video:v1</id><title>How we roast</title><link rel="alternate" href="https://www.youtube.com/watch?v=v1"/><author><name>Nile Coffee</name></author><published>2026-09-21T09:00:00Z</published></entry>
			<entry><id>yt:video:v2</id><title>Cold brew at home</title><link rel="alternate" href="https://www.youtube.com/watch?v=v2"/><author><name>Nile Coffee</name></author><published>2026-09-22T09:00:00Z</published></entry></feed>`)
	})
	mux.HandleFunc("/status.rss", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Supplier status</title>
			<item><guid>s1</guid><title>Shipping delays to Alexandria</title><link>https://status.example/1</link><description>Two days late</description><pubDate>Mon, 21 Sep 2026 08:00:00 GMT</pubDate></item>
			<item><guid>s2</guid><title>Warehouse back to normal</title><link>https://status.example/2</link><pubDate>Tue, 22 Sep 2026 08:00:00 GMT</pubDate></item>
			<item><guid>s3</guid><title>New price list</title><link>https://status.example/3</link><pubDate>Wed, 23 Sep 2026 08:00:00 GMT</pubDate></item>
			</channel></rss>`)
	})
	mux.HandleFunc("/not-a-feed", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>hello</html>") })
	f.Server = httptest.NewServer(mux)
	return f
}

func TestNotificationCollection(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "collect-test-secret-collect-test-secret-0123456")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	t.Setenv("GOOGLE_COLLECTOR", "off")
	t.Setenv("NOTIFY_COLLECTOR", "off")
	t.Setenv("NOTIFY_RUNS_PER_HOUR", "8")

	fake := newFakePublic()
	defer fake.Close()
	defer notifications.SetEndpoints(notifications.Endpoints{Bluesky: fake.URL, YouTube: fake.URL})()

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()

	alice, bob := newClient(t, srv.URL), newClient(t, srv.URL)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "col-a-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	bob.must("POST", "/api/auth/register", map[string]string{"email": "col-b-" + rnd() + "@example.test", "password": "correct-horse-2"}, 201)
	aID := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Collect A " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	bID := bob.must("POST", "/api/me/workspaces", map[string]string{"name": "Collect B " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws, bob.ws = aID, bID

	// ---- the catalog: what can be collected, and what is not configured ---------
	list := alice.must("GET", "/api/w/notification_sources", nil, 200)
	avail := map[string]bool{}
	for _, c := range list["catalog"].([]any) {
		m := c.(map[string]any)
		avail[m["kind"].(string)] = m["available"].(bool)
	}
	for kind, want := range map[string]bool{"bluesky": true, "youtube": true, "rss": true, "gmail": false, "facebook": false, "linkedin": false, "x": false, "instagram": false, "tiktok": false, "threads": false} {
		if got, ok := avail[kind]; !ok || got != want {
			t.Errorf("catalog %s: available=%v (listed %v), want %v", kind, got, ok, want)
		}
	}
	if code, b := alice.do("POST", "/api/w/notification_sources", map[string]any{"kind": "gmail", "target": "me@example.test"}); code != 422 {
		t.Errorf("adding gmail: got %d want 422: %s", code, b)
	}
	if code, b := alice.do("POST", "/api/w/notification_sources", map[string]any{"kind": "bluesky", "target": "nobody.bsky.social"}); code != 422 || !strings.Contains(string(b), "no account") {
		t.Errorf("an unknown Bluesky handle: got %d: %s", code, b)
	}
	if code, b := alice.do("POST", "/api/w/notification_sources", map[string]any{"kind": "rss", "target": fake.URL + "/not-a-feed"}); code != 422 || !strings.Contains(string(b), "not an RSS") {
		t.Errorf("a page that is not a feed: got %d: %s", code, b)
	}

	// ---- Bluesky -------------------------------------------------------------------
	bsky := alice.must("POST", "/api/w/notification_sources", map[string]any{"kind": "bluesky", "target": "@Shop.bsky.social", "interval_minutes": 30}, 201)["source"].(map[string]any)
	if bsky["target"] != "shop.bsky.social" || bsky["name"] != "Bluesky · Nile Shop" || bsky["next_run_at"] == nil {
		t.Fatalf("bluesky source: %+v", bsky)
	}
	bskyID := bsky["id"].(string)

	// Listen as another owner-level member would, to see what alerts.
	events, stop := a.Events.Subscribe(aID, realtime.Viewer{UserID: "someone-else", Role: "owner"})
	defer stop()
	alerted := func() (n int) {
		for {
			select {
			case raw := <-events:
				var ev realtime.Event
				_ = json.Unmarshal(raw, &ev)
				if ev.Alert {
					n++
				}
			default:
				return n
			}
		}
	}

	run := alice.must("POST", "/api/w/notification_sources/"+bskyID+"/collect", nil, 200)
	if run["new"].(float64) != 2 || run["source"].(map[string]any)["last_status"] != "ok" {
		t.Fatalf("first Bluesky run: %+v", run)
	}
	if n := alerted(); n != 0 {
		t.Errorf("the first run alerted %d times; it must file quietly", n)
	}
	if code, _ := alice.do("POST", "/api/w/notification_sources/"+bskyID+"/collect", nil); code != 429 {
		t.Errorf("collecting again within a minute: got %d want 429", code)
	}
	// Push on, two new replies: each alerts. (last_run_at moved back so the
	// manual gap does not refuse the run.)
	alice.must("PATCH", "/api/w/notification_sources/"+bskyID, map[string]any{"push_enabled": true, "push_kinds": "reply"}, 200)
	fake.replies.Store(4)
	back := func(id string) {
		if _, err := a.SysDB.Exec(`UPDATE notification_sources SET last_run_at = now() - interval '2 minutes' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	back(bskyID)
	run = alice.must("POST", "/api/w/notification_sources/"+bskyID+"/collect", nil, 200)
	if run["new"].(float64) != 2 {
		t.Fatalf("second Bluesky run should file only the 2 new replies: %+v", run)
	}
	if n := alerted(); n != 2 {
		t.Errorf("two new replies with push on alerted %d times, want 2", n)
	}
	// Six more: one summary alert, not six.
	fake.replies.Store(10)
	back(bskyID)
	alice.must("POST", "/api/w/notification_sources/"+bskyID+"/collect", nil, 200)
	if n := alerted(); n != 1 {
		t.Errorf("six new replies alerted %d times, want 1 summary", n)
	}

	// ---- YouTube by @handle, RSS ------------------------------------------------------
	yt := alice.must("POST", "/api/w/notification_sources", map[string]any{"kind": "youtube", "target": "https://www.youtube.com/@nilecoffee", "interval_minutes": 60}, 201)["source"].(map[string]any)
	if yt["target"] != "UCabcdefghijklmnopqrstuv" || yt["name"] != "YouTube · Nile Coffee" {
		t.Fatalf("youtube source: %+v", yt)
	}
	if r := alice.must("POST", "/api/w/notification_sources/"+yt["id"].(string)+"/collect", nil, 200); r["new"].(float64) != 2 {
		t.Fatalf("youtube run: %+v", r)
	}
	rss := alice.must("POST", "/api/w/notification_sources", map[string]any{"kind": "rss", "target": fake.URL + "/status.rss", "interval_minutes": 120}, 201)["source"].(map[string]any)
	if r := alice.must("POST", "/api/w/notification_sources/"+rss["id"].(string)+"/collect", nil, 200); r["new"].(float64) != 3 {
		t.Fatalf("rss run: %+v", r)
	}

	// ---- the budget ---------------------------------------------------------------------
	// 30 + 60 + 120 minutes = 2 + 1 + 0.5 runs an hour; the share is 8.
	if code, b := alice.do("POST", "/api/w/notification_sources", map[string]any{"kind": "rss", "target": fake.URL + "/status.rss", "interval_minutes": 15}); code != 201 {
		t.Fatalf("a 15-minute source within budget: %d %s", code, b)
	}
	if code, b := alice.do("POST", "/api/w/notification_sources", map[string]any{"kind": "rss", "target": fake.URL + "/status.rss", "interval_minutes": 15}); code != 422 || !strings.Contains(string(b), "times an hour") {
		t.Errorf("over the budget: got %d: %s", code, b)
	}
	if code, b := alice.do("PATCH", "/api/w/notification_sources/"+rss["id"].(string), map[string]any{"interval_minutes": 7}); code != 422 {
		t.Errorf("an interval that is not on the list: got %d: %s", code, b)
	}
	budget := alice.must("GET", "/api/w/notification_sources", nil, 200)["budget"].(map[string]any)
	if budget["runs_per_hour"].(float64) != 7.5 || budget["limit"].(float64) != 8 {
		t.Errorf("budget: %+v", budget)
	}

	// ---- the list: search, kind, source facets, paging, mark-all per source ----------------
	feed := alice.must("GET", "/api/w/notifications?limit=4", nil, 200)
	if feed["total"].(float64) != 15 || len(feed["items"].([]any)) != 4 || feed["next_cursor"] == "" {
		t.Fatalf("page 1: total=%v items=%d next=%v", feed["total"], len(feed["items"].([]any)), feed["next_cursor"])
	}
	seen := map[string]bool{}
	cursor := feed["next_cursor"].(string)
	for _, it := range feed["items"].([]any) {
		seen[it.(map[string]any)["id"].(string)] = true
	}
	for pages := 1; cursor != ""; pages++ {
		p := alice.must("GET", "/api/w/notifications?limit=4&cursor="+url.QueryEscape(cursor), nil, 200)
		for _, it := range p["items"].([]any) {
			id := it.(map[string]any)["id"].(string)
			if seen[id] {
				t.Fatalf("page %d repeats %s", pages+1, id)
			}
			seen[id] = true
		}
		cursor, _ = p["next_cursor"].(string)
		if pages > 10 {
			t.Fatal("paging never ends")
		}
	}
	if len(seen) != 15 {
		t.Errorf("paged through %d items, want 15", len(seen))
	}
	facets := map[string]map[string]any{}
	for _, f := range feed["sources"].([]any) {
		m := f.(map[string]any)
		facets[m["key"].(string)] = m
	}
	if f := facets[bskyID]; f == nil || f["total"].(float64) != 10 || f["origin"] != "collected" || f["name"] != "Bluesky · Nile Shop" {
		t.Errorf("the Bluesky facet: %+v", f)
	}
	kinds := map[string]float64{}
	for _, k := range feed["kinds"].([]any) {
		m := k.(map[string]any)
		kinds[m["value"].(string)] = m["total"].(float64)
	}
	if kinds["reply"] != 10 || kinds["video"] != 2 || kinds["post"] != 3 {
		t.Errorf("kind facets: %+v", kinds)
	}
	if r := alice.must("GET", "/api/w/notifications?q="+url.QueryEscape("cold BREW"), nil, 200); r["total"].(float64) != 1 {
		t.Errorf("search: %+v", r["total"])
	}
	// A search term is literal text, not a pattern.
	if r := alice.must("GET", "/api/w/notifications?q=%25", nil, 200); r["total"].(float64) != 0 {
		t.Errorf("searching %%: %v", r["total"])
	}
	if r := alice.must("GET", "/api/w/notifications?kind=video", nil, 200); r["total"].(float64) != 2 {
		t.Errorf("kind=video: %v", r["total"])
	}
	if r := alice.must("GET", "/api/w/notifications?source_id="+bskyID+"&unread=true", nil, 200); r["total"].(float64) != 10 {
		t.Errorf("source filter: %v", r["total"])
	}
	marked := alice.must("POST", "/api/w/notifications/mark_all_read?source_id="+bskyID, nil, 200)
	if marked["marked"].(float64) != 10 {
		t.Errorf("mark all read for one source: %+v", marked)
	}
	if r := alice.must("GET", "/api/w/notifications?unread=true", nil, 200); r["total"].(float64) != 5 {
		t.Errorf("the other sources must stay unread: %v", r["total"])
	}

	// ---- the item detail carries the raw payload ------------------------------------------
	one := alice.must("GET", "/api/w/notifications?kind=reply&limit=1", nil, 200)["items"].([]any)[0].(map[string]any)
	detail := alice.must("GET", "/api/w/notifications/"+one["id"].(string), nil, 200)["item"].(map[string]any)
	if !strings.Contains(detail["payload"].(string), "at://did:plc:fan") || detail["feed_source_id"] != bskyID || detail["source_key"] != bskyID {
		t.Errorf("detail: %+v", detail)
	}
	if strings.Contains(fmt.Sprint(detail["url"]), fake.URL) {
		t.Errorf("a Bluesky item should link to bsky.app, not the API: %v", detail["url"])
	}

	// ---- another workspace sees none of it -------------------------------------------------
	if r := bob.must("GET", "/api/w/notifications", nil, 200); r["total"].(float64) != 0 || len(r["sources"].([]any)) != 0 {
		t.Errorf("B sees A's items: %+v", r)
	}
	if r := bob.must("GET", "/api/w/notification_sources", nil, 200); len(r["sources"].([]any)) != 0 {
		t.Errorf("B sees A's sources")
	}
	for _, try := range []struct{ method, path string }{
		{"PATCH", "/api/w/notification_sources/" + bskyID},
		{"POST", "/api/w/notification_sources/" + bskyID + "/collect"},
		{"DELETE", "/api/w/notification_sources/" + bskyID},
	} {
		if code, b := bob.do(try.method, try.path, map[string]any{"enabled": false}); code != 404 {
			t.Errorf("B %s %s: got %d want 404: %s", try.method, try.path, code, b)
		}
	}
	if code, _ := bob.do("POST", "/api/w/notifications/mark_all_read?source_id="+bskyID, nil); code != 200 {
		t.Errorf("B's own mark-all-read failed")
	}
	if r := alice.must("GET", "/api/w/notifications?unread=true", nil, 200); r["total"].(float64) != 5 {
		t.Errorf("B's mark-all-read with A's source id touched A: %v", r["total"])
	}

	// ---- the scheduled queue runs a due source and moves it on ------------------------------
	if _, err := a.SysDB.Exec(`UPDATE notification_sources SET next_run_at = now() - interval '1 minute' WHERE id = $1`, rss["id"]); err != nil {
		t.Fatal(err)
	}
	if n, _ := notifications.CollectDue(t.Context(), a, 10); n < 1 {
		t.Errorf("CollectDue ran %d sources", n)
	}
	var next time.Time
	if err := a.SysDB.QueryRow(`SELECT next_run_at FROM notification_sources WHERE id = $1`, rss["id"]).Scan(&next); err != nil || time.Until(next) < 100*time.Minute {
		t.Errorf("after a run the next one is an interval away: %v %v", next, err)
	}

	// Disabling takes it off the queue and out of the budget; deleting keeps its items.
	alice.must("PATCH", "/api/w/notification_sources/"+bskyID, map[string]any{"enabled": false}, 200)
	alice.do("DELETE", "/api/w/notification_sources/"+bskyID, nil)
	if r := alice.must("GET", "/api/w/notifications?kind=reply", nil, 200); r["total"].(float64) != 10 {
		t.Errorf("deleting a source must keep its items: %v", r["total"])
	}
}
