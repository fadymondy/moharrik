package trends

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
)

/*
The trends center end to end, against a real database with RLS on (MH-385,
MH-387): the real handlers, the real collector, and the feeds exactly as they
answered on 2026-09-23 (testdata/), served from a local server so the test
never reaches the internet.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/trends/ -v
*/

// captured is just after the newest item in testdata/. Clustering only looks
// at the last Window, so the feeds are served as if they were fetched now:
// every date moves forward by the same amount and keeps its order and gaps.
var (
	captured  = time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
	pubDateRE = regexp.MustCompile(`<pubDate>([^<]+)</pubDate>`)
	dcDateRE  = regexp.MustCompile(`<dc:date>([^<]+)</dc:date>`)
)

func shiftDates(b []byte) []byte {
	shift := time.Since(captured)
	b = pubDateRE.ReplaceAllFunc(b, func(m []byte) []byte {
		s := string(pubDateRE.FindSubmatch(m)[1])
		for _, layout := range []string{time.RFC1123Z, time.RFC1123, "Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST"} {
			if t, err := time.Parse(layout, s); err == nil {
				return []byte("<pubDate>" + t.Add(shift).UTC().Format(time.RFC1123Z) + "</pubDate>")
			}
		}
		return m
	})
	return dcDateRE.ReplaceAllFunc(b, func(m []byte) []byte {
		t, err := time.Parse(time.RFC3339, string(dcDateRE.FindSubmatch(m)[1]))
		if err != nil {
			return m
		}
		return []byte("<dc:date>" + t.Add(shift).UTC().Format(time.RFC3339) + "</dc:date>")
	})
}

type fixture struct {
	a     *app.App
	api   humatest.TestAPI
	ws    string
	ctx   context.Context // the owner, through the API
	feeds *httptest.Server
}

func setup(t *testing.T) *fixture {
	t.Helper()
	owner, appURL := testdb.Setup(t, "trends", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "trends-test-secret-trends-test-secret-trends")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("SCHEDULER", "off")
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	k := togo.New()
	a := app.New(context.Background(), k)
	t.Cleanup(func() { k.Close() })
	if a.SQLDB == nil || a.SysDB == nil {
		t.Fatal("both pools are required")
	}

	f := &fixture{a: a}
	ctx := context.Background()
	suffix := strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	user := "u-trends-" + suffix
	if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', 'now')`,
		user, "owner-"+suffix+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRowContext(ctx, `INSERT INTO workspaces (name, slug, created_by) VALUES ('Trends', $1, $2) RETURNING id`,
		"trends-"+suffix, user).Scan(&f.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')`,
		f.ws, user); err != nil {
		t.Fatal(err)
	}
	f.ctx = tenant.With(ctx, &tenant.Scope{WorkspaceID: f.ws, UserID: user, Role: "owner", Via: "session"})

	// The captured feeds, and a Hacker News that answers like the real API.
	f.feeds = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		switch {
		case name == "bbc-business-again.xml": // another address carrying the same articles
			name = "bbc-business.xml"
		case name == "hn-mirror.xml": // an RSS mirror carrying one story the HN API also lists
			fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>HN mirror</title><item><title>Ineos halts UK chemical plants over gas prices, says Ratcliffe</title><link>https://example.test/hn/9001</link><guid>mirror-9001</guid></item></channel></rss>`)
			return
		case name == "not-a-feed":
			_, _ = w.Write([]byte("<html><body>hello</body></html>"))
			return
		case name == "v0/topstories.json":
			_, _ = w.Write([]byte(`[9001, 9002]`))
			return
		case strings.HasPrefix(name, "v0/item/"):
			id := strings.TrimSuffix(strings.TrimPrefix(name, "v0/item/"), ".json")
			fmt.Fprintf(w, `{"id":%s,"type":"story","title":"Ineos halts UK chemical plants over gas prices, says Ratcliffe","url":"https://example.test/hn/%s","score":%s0,"descendants":12,"time":%d}`,
				id, id, id[3:], time.Now().Add(-time.Hour).Unix())
			return
		}
		b, err := os.ReadFile(filepath.Join("testdata", filepath.Base(name)))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write(shiftDates(b))
	}))
	t.Cleanup(f.feeds.Close)
	old := HNBase
	HNBase = f.feeds.URL + "/v0"
	t.Cleanup(func() { HNBase = old })

	_, f.api = humatest.New(t)
	Register(f.api, a)
	return f
}

func (f *fixture) do(t *testing.T, method, path string, body any, want int) map[string]any {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	var res *httptest.ResponseRecorder
	switch method {
	case http.MethodGet:
		res = f.api.GetCtx(f.ctx, path)
	case http.MethodPost:
		res = f.api.PostCtx(f.ctx, path, body)
	case http.MethodPatch:
		res = f.api.PatchCtx(f.ctx, path, body)
	}
	if res.Code != want {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, res.Code, want, res.Body)
	}
	out := map[string]any{}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	return out
}

func (f *fixture) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.a.SysDB.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type topic struct {
	ID            string        `json:"id"`
	Day           string        `json:"day"`
	Title         string        `json:"title"`
	Summary       string        `json:"summary"`
	SummarySource string        `json:"summary_source"`
	Outlets       []string      `json:"outlets"`
	Score         float64       `json:"score"`
	Reasons       []TrendReason `json:"reasons"`
	State         string        `json:"state"`
	Items         []NewsOut     `json:"items"`
}

type page struct {
	Topics     []topic     `json:"topics"`
	HasMore    bool        `json:"has_more"`
	NextCursor string      `json:"next_cursor"`
	Counts     TrendCounts `json:"counts"`
	Days       []TrendDay  `json:"days"`
}

func (f *fixture) topics(t *testing.T, query string) page {
	t.Helper()
	res := f.api.GetCtx(f.ctx, "/api/w/trends?"+query)
	if res.Code != 200 {
		t.Fatalf("GET /api/w/trends?%s: %d %s", query, res.Code, res.Body)
	}
	var p page
	if err := json.Unmarshal(res.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *fixture) allTopics(t *testing.T, query string) []topic {
	t.Helper()
	var all []topic
	seen := map[string]bool{}
	cursor := ""
	for i := 0; i < 100; i++ {
		p := f.topics(t, query+"&limit=7&cursor="+cursor)
		for _, tp := range p.Topics {
			if seen[tp.ID] {
				t.Fatalf("page %d repeats topic %s", i, tp.ID)
			}
			seen[tp.ID] = true
			all = append(all, tp)
		}
		if !p.HasMore {
			return all
		}
		cursor = p.NextCursor
	}
	t.Fatal("pagination never ended")
	return nil
}

func findTopic(list []topic, fragment string) *topic {
	for i := range list {
		for _, it := range list[i].Items {
			if strings.Contains(it.Title, fragment) {
				return &list[i]
			}
		}
		if strings.Contains(list[i].Title, fragment) {
			return &list[i]
		}
	}
	return nil
}

func hasReason(tp *topic, kind string) *TrendReason {
	for i := range tp.Reasons {
		if tp.Reasons[i].Kind == kind {
			return &tp.Reasons[i]
		}
	}
	return nil
}

func TestTrendsCenterEndToEnd(t *testing.T) {
	f := setup(t)

	// A keyword topic first: it becomes the relevance input, not a filter.
	f.do(t, http.MethodPost, "/api/w/topics", map[string]any{"name": "energy prices", "keywords": []string{"gas prices", "energy"}}, 201)

	// ---- sources: feeds, dedupe, a catalog API source, and a URL that is not a feed.
	add := func(name, file string) int {
		r := f.do(t, http.MethodPost, "/api/w/feed_sources", map[string]any{"name": name, "url": f.feeds.URL + "/" + file}, 201)
		return int(r["added"].(float64))
	}
	if n := add("BBC News: Business", "bbc-business.xml"); n != 52 {
		// 55 entries, three of them the same article twice under another guid.
		t.Fatalf("BBC business added %d, want 52 (55 entries, 3 duplicate URLs)", n)
	}
	if n := add("BBC again", "bbc-business-again.xml"); n != 0 {
		t.Fatalf("a second source carrying the same articles added %d, want 0", n)
	}
	add("The Guardian: Business", "guardian-business.xml")
	add("بي بي سي عربي", "bbc-arabic.xml")
	add("CNN بالعربية", "cnn-ar.xml")
	if n := f.count(t, `SELECT count(*) - count(DISTINCT canonical_url) FROM news_items WHERE workspace_id = $1`, f.ws); n != 0 {
		t.Fatalf("%d items share a canonical URL", n)
	}

	// The RSS mirror files one story first; the API then finds it already here.
	add("HN mirror", "hn-mirror.xml")
	hn := f.do(t, http.MethodPost, "/api/w/feed_sources", map[string]any{"catalog_key": "hackernews"}, 201)
	src := hn["source"].(map[string]any)
	if src["provider"] != "hn" || src["kind"] != "api" || src["catalog_key"] != "hackernews" || hn["added"].(float64) != 1 ||
		src["interval_minutes"].(float64) != 360 || src["last_status"] != "ok" {
		t.Fatalf("hacker news from the catalog: %v", hn)
	}
	f.do(t, http.MethodPost, "/api/w/feed_sources", map[string]any{"catalog_key": "hackernews"}, 409)
	// ...and its position and points land on the row the mirror filed.
	if n := f.count(t, `SELECT count(*) FROM news_items WHERE workspace_id = $1 AND url = 'https://example.test/hn/9001' AND provider = 'hn' AND rank = 1 AND points = 10`, f.ws); n != 1 {
		t.Fatal("the API's rank and points did not reach the row another feed filed first")
	}
	f.do(t, http.MethodPost, "/api/w/feed_sources", map[string]any{"url": f.feeds.URL + "/not-a-feed"}, 422)
	if n := f.count(t, `SELECT count(*) FROM feed_sources WHERE workspace_id = $1 AND url LIKE '%not-a-feed'`, f.ws); n != 0 {
		t.Fatal("a URL that is not a feed was kept")
	}
	cat := f.do(t, http.MethodGet, "/api/w/feed_catalog", nil, 200)
	added := 0
	for _, e := range cat["entries"].([]any) {
		if e.(map[string]any)["source_id"] != nil {
			added++
		}
	}
	if added != 1 {
		t.Fatalf("the catalog shows %d entries as added, want 1 (hackernews)", added)
	}

	// ---- the list: every topic once, across pages, best first within a day.
	all := f.allTopics(t, "state=all")
	p := f.topics(t, "state=all&limit=7")
	if !p.HasMore || len(p.Topics) != 7 || int(p.Counts.All) != len(all) || p.Counts.Open != p.Counts.All {
		t.Fatalf("first page: %d topics, has_more %v, counts %+v, walked %d", len(p.Topics), p.HasMore, p.Counts, len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].Day > all[i-1].Day || (all[i].Day == all[i-1].Day && all[i].Score > all[i-1].Score) {
			t.Fatalf("out of order at %d: %s %v after %s %v", i, all[i].Day, all[i].Score, all[i-1].Day, all[i-1].Score)
		}
	}
	items := f.count(t, `SELECT count(*) FROM news_items WHERE workspace_id = $1`, f.ws)
	placed := f.count(t, `SELECT count(*) FROM news_items WHERE workspace_id = $1 AND cluster_id IS NOT NULL`, f.ws)
	if placed != items || len(all) >= items {
		t.Fatalf("%d of %d items placed into %d topics", placed, items, len(all))
	}

	// ---- one English story, two outlets and Hacker News, with its reasons.
	ratcliffe := findTopic(all, "Ratcliffe")
	if ratcliffe == nil {
		t.Fatal("no Ratcliffe topic")
	}
	if len(ratcliffe.Outlets) < 2 {
		t.Fatalf("the Ratcliffe story has outlets %v, want the BBC and the Guardian at least", ratcliffe.Outlets)
	}
	if r := hasReason(ratcliffe, "sources"); r == nil || r.Count != len(ratcliffe.Outlets) {
		t.Fatalf("sources reason: %+v", ratcliffe.Reasons)
	}
	if r := hasReason(ratcliffe, "relevance"); r == nil || strings.Join(r.Detail, ",") != "energy prices" {
		t.Fatalf("the keyword topic did not become relevance: %+v", ratcliffe.Reasons)
	}
	if ratcliffe.Summary == "" || !strings.HasPrefix(ratcliffe.SummarySource, "extractive") {
		t.Fatalf("summary %q (%s)", ratcliffe.Summary, ratcliffe.SummarySource)
	}
	sum := 0.0
	for _, r := range ratcliffe.Reasons {
		sum += r.Points
	}
	if fmt.Sprintf("%.1f", sum) != fmt.Sprintf("%.1f", ratcliffe.Score) {
		t.Fatalf("score %v is not the sum of its reasons %v", ratcliffe.Score, sum)
	}

	// ---- one Arabic story from two outlets.
	shooting := findTopic(all, "مدرسة")
	if shooting == nil || len(shooting.Outlets) != 2 {
		t.Fatalf("the Turkish school shooting (BBC عربي + CNN بالعربية) is not one topic: %+v", shooting)
	}

	// ---- detail: every item and outlet.
	var detail struct {
		Topic   topic         `json:"topic"`
		Outlets []TrendOutlet `json:"outlets"`
	}
	res := f.api.GetCtx(f.ctx, "/api/w/trends/"+ratcliffe.ID)
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &detail) != nil || len(detail.Outlets) != len(ratcliffe.Outlets) ||
		len(detail.Topic.Items) < len(ratcliffe.Outlets) {
		t.Fatalf("detail: %d %s", res.Code, res.Body)
	}

	// ---- the review workflow.
	open := p.Counts.Open
	f.do(t, http.MethodPatch, "/api/w/trends/"+ratcliffe.ID, map[string]any{"reviewed": true}, 200)
	f.do(t, http.MethodPatch, "/api/w/trends/"+shooting.ID, map[string]any{"dismissed": true}, 200)
	got := f.topics(t, "state=open")
	if got.Counts.Open != open-2 || got.Counts.Reviewed != 1 || got.Counts.Dismissed != 1 || findTopic(got.Topics, "Ratcliffe") != nil {
		t.Fatalf("after review + dismiss: %+v", got.Counts)
	}
	saved := f.do(t, http.MethodPatch, "/api/w/trends/"+shooting.ID, map[string]any{"saved": true}, 200)["topic"].(map[string]any)
	if saved["state"] != "saved" || saved["dismissed_at"] != nil {
		t.Fatalf("saving a dismissed topic: %v", saved)
	}
	if got := f.topics(t, "state=saved"); len(got.Topics) != 1 || got.Topics[0].ID != shooting.ID {
		t.Fatalf("saved list: %+v", got.Topics)
	}
	f.do(t, http.MethodPatch, "/api/w/trends/"+ratcliffe.ID, map[string]any{}, 422)
	f.do(t, http.MethodPatch, "/api/w/trends/not-a-topic", map[string]any{"saved": true}, 404)

	// ---- the date filter.
	day := all[0].Day
	byDay := f.allTopics(t, "state=all&day="+day)
	if len(byDay) == 0 {
		t.Fatal("no topics on the newest day")
	}
	for _, tp := range byDay {
		if tp.Day != day {
			t.Fatalf("day filter %s returned a topic from %s", day, tp.Day)
		}
	}
	f.do(t, http.MethodGet, "/api/w/trends?day=yesterday", nil, 422)

	// ---- the raw list pages too, newest first.
	n1 := f.do(t, http.MethodGet, "/api/w/news?limit=10", nil, 200)
	if n1["has_more"] != true || len(n1["items"].([]any)) != 10 {
		t.Fatalf("news page 1: %v", n1["has_more"])
	}
	n2 := f.do(t, http.MethodGet, "/api/w/news?limit=10&cursor="+n1["next_cursor"].(string), nil, 200)
	last1 := n1["items"].([]any)[9].(map[string]any)
	first2 := n2["items"].([]any)[0].(map[string]any)
	if last1["id"] == first2["id"] || last1["published_at"].(string) < first2["published_at"].(string) {
		t.Fatal("news page 2 overlaps or is out of order")
	}

	// ---- editing a keyword topic rescores: off, and relevance is gone.
	var kwID string
	_ = f.a.SysDB.QueryRow(`SELECT id FROM topics WHERE workspace_id = $1`, f.ws).Scan(&kwID)
	f.do(t, http.MethodPatch, "/api/w/topics/"+kwID, map[string]any{"enabled": false}, 200)
	res = f.api.GetCtx(f.ctx, "/api/w/trends/"+ratcliffe.ID)
	_ = json.Unmarshal(res.Body.Bytes(), &detail)
	if hasReason(&detail.Topic, "relevance") != nil {
		t.Fatalf("a disabled keyword topic still scores: %+v", detail.Topic.Reasons)
	}
}

func TestSourcesScheduleAndBudget(t *testing.T) {
	f := setup(t)
	// CollectDue reads every workspace's queue; earlier runs of this suite left sources
	// behind in the shared test database, so only this workspace's may be due.
	if _, err := f.a.SysDB.Exec(`UPDATE feed_sources SET enabled = false WHERE workspace_id <> $1`, f.ws); err != nil {
		t.Fatal(err)
	}
	r := f.do(t, http.MethodPost, "/api/w/feed_sources", map[string]any{"url": f.feeds.URL + "/cnn-ar.xml", "interval_minutes": 1440}, 201)
	src := r["source"].(map[string]any)
	id := src["id"].(string)
	if src["interval_minutes"].(float64) != 1440 || src["next_run_at"] == nil {
		t.Fatalf("created source: %v", src)
	}
	next, _ := time.Parse(time.RFC3339Nano, src["next_run_at"].(string))
	if d := time.Until(next); d < 23*time.Hour || d > 25*time.Hour {
		t.Fatalf("next run in %v, want about 24h", d)
	}
	f.do(t, http.MethodPost, "/api/w/feed_sources", map[string]any{"url": f.feeds.URL + "/dw-ar.xml", "interval_minutes": 500}, 422)

	// Rename and reschedule through the API the page uses.
	up := f.do(t, http.MethodPatch, "/api/w/feed_sources/"+id, map[string]any{"name": "CNN Arabic", "interval_minutes": 360}, 200)["source"].(map[string]any)
	if up["name"] != "CNN Arabic" || up["interval_minutes"].(float64) != 360 {
		t.Fatalf("patched: %v", up)
	}

	// Just fetched: not due, whatever the tick.
	if polled, _ := CollectDue(context.Background(), f.a, 50); polled != 0 {
		t.Fatalf("a source fetched a moment ago was polled again (%d)", polled)
	}
	// Seven hours later on a six-hour interval: due.
	if _, err := f.a.SysDB.Exec(`UPDATE feed_sources SET last_fetched_at = now() - interval '7 hours' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}

	// The budget: what has been spent is shown, and a spent budget refuses.
	st := f.do(t, http.MethodGet, "/api/w/trend_settings", nil, 200)["settings"].(map[string]any)
	if st["fetches_today"].(float64) != 1 || st["fetch_budget_per_day"].(float64) != DefaultBudget || st["scheduled_per_day"].(float64) != 4 {
		t.Fatalf("settings: %v", st)
	}
	f.do(t, http.MethodPatch, "/api/w/trend_settings", map[string]any{"fetch_budget_per_day": 1}, 200)
	if polled, _ := CollectDue(context.Background(), f.a, 50); polled != 0 {
		t.Fatal("a workspace over its budget was still polled")
	}
	fr := f.do(t, http.MethodPost, "/api/w/feed_sources/"+id+"/fetch", nil, 200)
	if s := fr["source"].(map[string]any); s["last_status"] != "over_budget" || fr["added"].(float64) != 0 {
		t.Fatalf("fetch over budget: %v", fr)
	}
	f.do(t, http.MethodPatch, "/api/w/trend_settings", map[string]any{"fetch_budget_per_day": 10}, 200)
	if polled, _ := CollectDue(context.Background(), f.a, 50); polled != 1 {
		t.Fatalf("the due source was not polled once the budget allowed (%d)", polled)
	}
	var status string
	_ = f.a.SysDB.QueryRow(`SELECT last_status FROM feed_sources WHERE id = $1`, id).Scan(&status)
	if status != "ok" {
		t.Fatalf("status after the scheduled fetch: %q", status)
	}
	f.do(t, http.MethodPatch, "/api/w/trend_settings", map[string]any{"fetch_budget_per_day": 0}, 422)
}

func TestBackfillCanonicalRemovesOldDuplicates(t *testing.T) {
	f := setup(t)
	sctx := tenant.WithWorkspace(context.Background(), f.ws)
	// Three rows collected before MH-385: one article twice, one once.
	for i, u := range []string{
		"https://www.bbc.co.uk/news/articles/c6vgyq598k9po?at_medium=RSS",
		"https://www.bbc.com/news/articles/c6vgyq598k9po#1",
		"https://www.bbc.com/news/articles/other",
	} {
		if _, err := f.a.SQLDB.ExecContext(sctx, `INSERT INTO news_items (workspace_id, external_id, title, url, source_name, published_at)
			VALUES ($1, $2, $3, $4, 'BBC', now())`, f.ws, fmt.Sprint("legacy-", i), fmt.Sprint("Legacy headline number ", i), u); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := BackfillCanonical(context.Background(), f.a); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT count(*) FROM news_items WHERE workspace_id = $1`, f.ws); n != 2 {
		t.Fatalf("%d rows left, want 2", n)
	}
	if n := f.count(t, `SELECT count(*) FROM news_items WHERE workspace_id = $1 AND cluster_id IS NULL`, f.ws); n != 0 {
		t.Fatalf("%d backfilled rows were not placed into topics", n)
	}
}
