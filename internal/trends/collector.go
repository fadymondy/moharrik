package trends

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// Topic is the minimum a keyword match needs.
type Topic struct {
	ID       string
	Name     string
	Keywords []string
}

// Source is one source due for collection.
type Source struct {
	ID          string
	WorkspaceID string
	Name        string
	URL         string
	Provider    string
	Params      map[string]string
}

// client is deliberately impatient: a source that will not answer in 20
// seconds should not hold the collector up behind every other workspace's.
var client = &http.Client{Timeout: 20 * time.Second}

const userAgent = "Moharrik/1.0 (+https://moharrik.com)"

// Intervals a source may be collected at, in minutes (MH-387).
var Intervals = []int{360, 720, 1440}

// DefaultBudget is a new workspace's daily fetch budget: 12 sources at a
// six-hour interval is 48, so the default leaves room for fetching by hand.
const DefaultBudget = 60

// MaxBudget caps what a workspace may set itself.
const MaxBudget = 500

// ErrOverBudget: today's fetches are spent.
var ErrOverBudget = errors.New("today's collection budget is spent")

func validInterval(m int) bool {
	for _, v := range Intervals {
		if v == m {
			return true
		}
	}
	return false
}

func paramsOf(raw string) map[string]string {
	out := map[string]string{}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

/*
takeBudget spends one fetch of the workspace's daily budget, or reports that
it is spent. The day is the UTC day; the first fetch of a new day resets the
count. One statement, so two collectors cannot both take the last fetch.
*/
func takeBudget(ctx context.Context, db *sql.DB, wsID string) (bool, error) {
	if _, err := db.ExecContext(ctx, `INSERT INTO trend_settings (workspace_id, fetch_budget_per_day, budget_day)
		VALUES ($1, $2, (now() AT TIME ZONE 'UTC')::date) ON CONFLICT (workspace_id) DO NOTHING`, wsID, DefaultBudget); err != nil {
		return false, err
	}
	var used int
	err := db.QueryRowContext(ctx, `
		UPDATE trend_settings SET
		  fetches_today = CASE WHEN budget_day = (now() AT TIME ZONE 'UTC')::date THEN fetches_today + 1 ELSE 1 END,
		  budget_day = (now() AT TIME ZONE 'UTC')::date, updated_at = now()
		WHERE workspace_id = $1
		  AND (budget_day <> (now() AT TIME ZONE 'UTC')::date OR fetches_today < fetch_budget_per_day)
		RETURNING fetches_today`, wsID).Scan(&used)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// fetchItems reads a source by its provider.
func fetchItems(ctx context.Context, src Source) (items []Item, kind string, err error) {
	switch src.Provider {
	case ProviderHN:
		items, err = CollectHN(ctx, src.Params)
		return items, "api", err
	case ProviderDevto:
		items, err = CollectDevto(ctx, src.Params)
		return items, "api", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("that URL is not usable: %s", err.Error())
	}
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml;q=0.9, */*;q=0.8")
	req.Header.Set("User-Agent", userAgent)
	res, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("could not fetch the feed: %s", err.Error())
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, "", fmt.Errorf("the feed answered %d", res.StatusCode)
	}
	feed, err := Parse(res.Body)
	if err != nil {
		return nil, "", err
	}
	return feed.Items, feed.Kind, nil
}

// splitOutlet takes the publisher off an aggregator's headline:
// "Title here - The Verge" -> ("Title here", "The Verge").
func splitOutlet(title string) (string, string) {
	i := strings.LastIndex(title, " - ")
	if i <= 0 || i >= len(title)-3 {
		return title, ""
	}
	outlet := strings.TrimSpace(title[i+3:])
	if outlet == "" || len([]rune(outlet)) > 60 {
		return title, ""
	}
	return strings.TrimSpace(title[:i]), outlet
}

/*
Collect fetches one source and files what it finds. It does NOT recluster:
callers collect every due source of a workspace and then call Recluster once.

It runs on the app (RLS) pool inside tenant.WithWorkspace for that source's
workspace, so the collector is confined exactly as a request is — a bug here
cannot file another workspace's items. The source row itself is read on the
owner pool by the caller, which is the same "resolve, then scope" shape the
public webhook and chat routes use.

Every fetch spends one of the workspace's daily budget (MH-387). A fetch that
fails, or is refused for budget, is recorded on the row (last_status,
last_error) rather than only logged: "why is nothing coming in" is a question
the sources page should be able to answer.
*/
func Collect(ctx context.Context, a *app.App, src Source) (int, error) {
	sctx := tenant.WithWorkspace(ctx, src.WorkspaceID)
	db := a.SQLDB
	if src.Provider == "" {
		src.Provider = ProviderFeed
	}

	// fail records why this source came back empty and returns the reason. The
	// optional sentinel is what lets a caller tell "this URL is not a feed at
	// all" — permanent, worth refusing outright — apart from "the host was down
	// this minute", which is worth keeping the source for and retrying.
	fail := func(msg string, sentinel ...error) (int, error) {
		_, _ = db.ExecContext(sctx, `
			UPDATE feed_sources SET last_status = 'failed', last_error = $3, last_error_at = now(), last_fetched_at = now(),
			  last_added = 0, updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, src.WorkspaceID, src.ID, trimTo(msg, 300))
		if len(sentinel) > 0 {
			return 0, fmt.Errorf("%w", sentinel[0])
		}
		return 0, fmt.Errorf("%s", msg)
	}

	ok, err := takeBudget(sctx, db, src.WorkspaceID)
	if err != nil {
		return 0, fmt.Errorf("could not read the collection budget: %w", err)
	}
	if !ok {
		// Not a failure of the source, and not a fetch: last_fetched_at stays,
		// so the source is due again the moment the budget allows.
		_, _ = db.ExecContext(sctx, `
			UPDATE feed_sources SET last_status = 'over_budget', last_error = $3, last_error_at = now(), updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, src.WorkspaceID, src.ID, ErrOverBudget.Error())
		return 0, ErrOverBudget
	}

	items, kind, err := fetchItems(ctx, src)
	if err != nil {
		if errors.Is(err, ErrNotAFeed) {
			return fail(err.Error(), err)
		}
		return fail(err.Error())
	}

	topics, err := loadTopics(sctx, db, src.WorkspaceID)
	if err != nil {
		return fail("could not read the topics: " + err.Error())
	}

	added := 0
	for _, item := range items {
		title, outlet := item.Title, ""
		if src.Params["outlet_suffix"] == "1" {
			title, outlet = splitOutlet(title)
			item.Title = title
		}
		if src.Provider == ProviderHN {
			outlet = "Hacker News"
		} else if src.Provider == ProviderDevto {
			outlet = "DEV Community"
		}
		if outlet == "" {
			outlet = src.Name
		}
		ids := Match(topics, item)
		// ON CONFLICT with no target: a second poll of the same entry (the
		// identity index) and the same article under another guid or from
		// another feed (the canonical index) are both "already have it".
		res, err := db.ExecContext(sctx, `
			INSERT INTO news_items (workspace_id, source_id, source_name, external_id, title, url, summary, author, topic_ids,
			  published_at, canonical_url, outlet, rank, points, comments, provider)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			ON CONFLICT DO NOTHING`,
			src.WorkspaceID, src.ID, src.Name, item.ExternalID, trimTo(item.Title, 400), item.URL,
			item.Summary, trimTo(item.Author, 200), pgtext.Literal(ids), clampFuture(item.PublishedAt),
			CanonicalURL(item.URL), trimTo(outlet, 200), item.Rank, item.Points, item.Comments, src.Provider)
		if err != nil {
			return fail("could not store an item: " + err.Error())
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		} else if src.Provider != ProviderFeed {
			// An API list moves: a story seen again keeps its row, but its
			// position and points are today's, which is what velocity reads.
			// The row may be this source's own, or the same article another feed
			// filed first (HN's RSS mirror, say): either way the API's numbers land.
			_, _ = db.ExecContext(sctx, `
				UPDATE news_items SET rank = $4, points = $5, comments = $6, provider = $7
				WHERE workspace_id = $1 AND ((source_id = $2 AND external_id = $3) OR ($8 <> '' AND canonical_url = $8))`,
				src.WorkspaceID, src.ID, item.ExternalID, item.Rank, item.Points, item.Comments, src.Provider, CanonicalURL(item.URL))
		}
	}

	_, _ = db.ExecContext(sctx, `
		UPDATE feed_sources SET last_fetched_at = now(), last_status = 'ok', last_error = '', last_error_at = NULL,
		  item_count = item_count + $3, last_added = $3, kind = $4, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, src.WorkspaceID, src.ID, int64(added), kind)
	return added, nil
}

// clampFuture keeps a feed's typo'd future date from pinning an item to the
// top of every list for a year.
func clampFuture(t time.Time) time.Time {
	if now := time.Now(); t.After(now.Add(time.Hour)) {
		return now
	}
	return t
}

func loadTopics(ctx context.Context, db *sql.DB, wsID string) ([]Topic, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, keywords FROM topics WHERE workspace_id = $1 AND enabled`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Topic
	for rows.Next() {
		var t Topic
		var kw pgtext.Array
		if err := rows.Scan(&t.ID, &t.Name, &kw); err != nil {
			return nil, err
		}
		t.Keywords = kw
		out = append(out, t)
	}
	return out, rows.Err()
}

// publish tells the workspace's open tabs that its trends changed.
func publish(a *app.App, wsID string) {
	if a != nil && a.Events != nil {
		a.Events.Publish(wsID, realtime.Event{Kind: "trends", Permission: authz.TrendsRead})
	}
}

/*
CollectDue polls the sources that are due and returns how many it polled and
how many items it filed.

"Due" is per source now (MH-387): last fetched more than its own interval ago
(6, 12 or 24 hours), or never. A workspace whose budget for the day is spent
is skipped in the query itself, so its sources do not crowd the queue for
everyone else's.

The queue is read on the OWNER pool on purpose: "which workspaces have a
source due" is a cross-tenant question by definition, the same one tenant
resolution asks. Everything after that runs scoped, per source, and each
workspace that filed anything is reclustered once at the end.
*/
func CollectDue(ctx context.Context, a *app.App, limit int) (int, int) {
	rows, err := a.SysDB.QueryContext(ctx, `
		SELECT s.id, s.workspace_id, s.name, s.url, s.provider, s.params::text
		FROM feed_sources s
		LEFT JOIN trend_settings b ON b.workspace_id = s.workspace_id
		WHERE s.enabled
		  AND (s.last_fetched_at IS NULL OR s.last_fetched_at + make_interval(mins => s.interval_minutes) <= now())
		  AND (b.workspace_id IS NULL OR b.budget_day <> (now() AT TIME ZONE 'UTC')::date OR b.fetches_today < b.fetch_budget_per_day)
		ORDER BY s.last_fetched_at NULLS FIRST, s.id LIMIT $1`, limit)
	if err != nil {
		a.Log.Warn("trends: could not read the source queue", "err", err)
		return 0, 0
	}
	var due []Source
	for rows.Next() {
		var s Source
		var params string
		if rows.Scan(&s.ID, &s.WorkspaceID, &s.Name, &s.URL, &s.Provider, &params) == nil {
			s.Params = paramsOf(params)
			due = append(due, s)
		}
	}
	rows.Close()

	items := 0
	touched := map[string]bool{}
	for _, s := range due {
		n, err := Collect(ctx, a, s)
		if err != nil {
			a.Log.Info("trends: source failed", "workspace", s.WorkspaceID, "source", s.ID, "err", err)
			continue
		}
		items += n
		touched[s.WorkspaceID] = true
	}
	ws := make([]string, 0, len(touched))
	for id := range touched {
		ws = append(ws, id)
	}
	sort.Strings(ws)
	for _, id := range ws {
		if _, err := Recluster(ctx, a, id); err != nil {
			a.Log.Warn("trends: clustering failed", "workspace", id, "err", err)
		}
		publish(a, id)
	}
	return len(due), items
}

/*
StartCollector runs CollectDue on a ticker for the life of the process.

The tick is only how often the queue is looked at; when each source is due is
its own interval. One in-process ticker, not a job queue: Moharrik runs as a
single container (deploy/lxc/docker-compose.yml). When it runs on more than
one replica this needs a lock — the ON CONFLICT in Collect makes a double poll
harmless and the budget counter is atomic, but it is still wasted work.
*/
func StartCollector(a *app.App, tick time.Duration, perTick int) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// A small delay so a restart does not hammer every source at boot.
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		backfilled := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			if !backfilled {
				if n, err := BackfillCanonical(ctx, a); err != nil {
					a.Log.Warn("trends: canonical backfill failed", "err", err)
				} else {
					backfilled = true
					if n > 0 {
						a.Log.Info("trends: canonical URLs backfilled", "workspaces", n)
					}
				}
			}
			sources, items := CollectDue(ctx, a, perTick)
			if sources > 0 {
				a.Log.Info("trends: collected", "sources", sources, "new_items", items)
			}
			timer.Reset(tick)
		}
	}()
	return cancel
}

/*
BackfillCanonical gives items collected before MH-385 their canonical URL,
removing the duplicates that were only duplicates by URL (the oldest row of
each article stays), and then clusters each workspace it touched. Returns how
many workspaces it touched. Idempotent: rows that have a canonical URL, or no
URL at all, are never looked at again.
*/
func BackfillCanonical(ctx context.Context, a *app.App) (int, error) {
	rows, err := a.SysDB.QueryContext(ctx, `SELECT DISTINCT workspace_id FROM news_items WHERE canonical_url = '' AND url <> ''`)
	if err != nil {
		return 0, err
	}
	var workspaces []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			workspaces = append(workspaces, id)
		}
	}
	rows.Close()
	for _, wsID := range workspaces {
		sctx := tenant.WithWorkspace(ctx, wsID)
		if err := backfillWorkspace(sctx, a.SQLDB, wsID); err != nil {
			return 0, err
		}
		if _, err := Recluster(ctx, a, wsID); err != nil {
			return 0, err
		}
	}
	return len(workspaces), nil
}

func backfillWorkspace(ctx context.Context, db *sql.DB, wsID string) error {
	seen := map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT canonical_url FROM news_items WHERE workspace_id = $1 AND canonical_url <> ''`, wsID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var c string
		if rows.Scan(&c) == nil {
			seen[c] = true
		}
	}
	rows.Close()

	type row struct{ id, url, outlet string }
	var todo []row
	rows, err = db.QueryContext(ctx, `SELECT id, url, source_name FROM news_items
		WHERE workspace_id = $1 AND canonical_url = '' AND url <> '' ORDER BY created_at, id`, wsID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var r row
		if rows.Scan(&r.id, &r.url, &r.outlet) == nil {
			todo = append(todo, r)
		}
	}
	rows.Close()
	for _, r := range todo {
		c := CanonicalURL(r.url)
		switch {
		case c == "":
			continue
		case seen[c]:
			if _, err := db.ExecContext(ctx, `DELETE FROM news_items WHERE workspace_id = $1 AND id = $2`, wsID, r.id); err != nil {
				return err
			}
		default:
			seen[c] = true
			if _, err := db.ExecContext(ctx, `UPDATE news_items SET canonical_url = $3,
				outlet = CASE WHEN outlet = '' THEN $4 ELSE outlet END WHERE workspace_id = $1 AND id = $2`, wsID, r.id, c, r.outlet); err != nil {
				return err
			}
		}
	}
	return nil
}
