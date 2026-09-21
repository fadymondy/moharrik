package trends

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// Topic is the minimum a match needs.
type Topic struct {
	ID       string
	Name     string
	Keywords []string
}

// Source is one feed due for collection.
type Source struct {
	ID          string
	WorkspaceID string
	Name        string
	URL         string
}

// client is deliberately impatient: a feed that will not answer in 20 seconds
// should not hold the collector up behind every other workspace's feeds.
var client = &http.Client{Timeout: 20 * time.Second}

/*
Collect fetches one source and files what it finds.

It runs on the app (RLS) pool inside tenant.WithWorkspace for that source's
workspace, so the collector is confined exactly as a request is — a bug here
cannot file another workspace's items. The source row itself is read on the
owner pool by the caller, which is the same "resolve, then scope" shape the
public webhook and chat routes use.

A fetch that fails is recorded on the row (last_error / last_error_at) rather
than only logged: "why is nothing coming in" is a question the settings page
should be able to answer.
*/
func Collect(ctx context.Context, a *app.App, src Source) (int, error) {
	sctx := tenant.WithWorkspace(ctx, src.WorkspaceID)

	// fail records why this source came back empty and returns the reason. The
	// optional sentinel is what lets a caller tell "this URL is not a feed at
	// all" — permanent, worth refusing outright — apart from "the host was down
	// this minute", which is worth keeping the source for and retrying.
	fail := func(msg string, sentinel ...error) (int, error) {
		_, _ = a.SQLDB.ExecContext(sctx, `
			UPDATE feed_sources SET last_error = $3, last_error_at = now(), last_fetched_at = now(), updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, src.WorkspaceID, src.ID, trimTo(msg, 300))
		if len(sentinel) > 0 {
			// msg is already the sentinel's own text here; wrapping it twice would
			// say the same sentence to the user twice.
			return 0, fmt.Errorf("%w", sentinel[0])
		}
		return 0, fmt.Errorf("%s", msg)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return fail("that URL is not usable: " + err.Error())
	}
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml;q=0.9, */*;q=0.8")
	req.Header.Set("User-Agent", "Moharrik/1.0 (+https://moharrik.com)")

	res, err := client.Do(req)
	if err != nil {
		return fail("could not fetch the feed: " + err.Error())
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fail(fmt.Sprintf("the feed answered %d", res.StatusCode))
	}

	feed, err := Parse(res.Body)
	if err != nil {
		return fail(err.Error(), err)
	}

	topics, err := loadTopics(sctx, a.SQLDB, src.WorkspaceID)
	if err != nil {
		return fail("could not read the topics: " + err.Error())
	}

	added := 0
	for _, item := range feed.Items {
		ids := Match(topics, item)
		res, err := a.SQLDB.ExecContext(sctx, `
			INSERT INTO news_items (workspace_id, source_id, source_name, external_id, title, url, summary, author, topic_ids, published_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (workspace_id, source_id, external_id) WHERE source_id IS NOT NULL DO NOTHING`,
			src.WorkspaceID, src.ID, src.Name, item.ExternalID, trimTo(item.Title, 400), item.URL,
			item.Summary, trimTo(item.Author, 200), pgArray(ids), item.PublishedAt)
		if err != nil {
			return fail("could not store an item: " + err.Error())
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}

	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE feed_sources SET last_fetched_at = now(), last_error = '', last_error_at = NULL,
		  item_count = item_count + $3, kind = $4, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, src.WorkspaceID, src.ID, int64(added), feed.Kind)
	return added, nil
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
		var kw pgTextArray
		if err := rows.Scan(&t.ID, &t.Name, &kw); err != nil {
			return nil, err
		}
		t.Keywords = kw
		out = append(out, t)
	}
	return out, nil
}

/*
CollectDue polls the feeds that are due and returns how many items it filed.

It reads the queue on the OWNER pool on purpose: "which workspaces have a feed
due" is a cross-tenant question by definition, the same one tenant resolution
asks. Everything after that runs scoped, per source.

limit keeps one pass bounded, so a thousand workspaces cannot turn a tick into
an hour.
*/
func CollectDue(ctx context.Context, a *app.App, every time.Duration, limit int) (int, int) {
	rows, err := a.SysDB.QueryContext(ctx, `
		SELECT id, workspace_id, name, url FROM feed_sources
		WHERE enabled AND (last_fetched_at IS NULL OR last_fetched_at < now() - $1::interval)
		ORDER BY last_fetched_at NULLS FIRST LIMIT $2`,
		fmt.Sprintf("%d seconds", int(every.Seconds())), limit)
	if err != nil {
		a.Log.Warn("trends: could not read the feed queue", "err", err)
		return 0, 0
	}
	var due []Source
	for rows.Next() {
		var s Source
		if rows.Scan(&s.ID, &s.WorkspaceID, &s.Name, &s.URL) == nil {
			due = append(due, s)
		}
	}
	rows.Close()

	items := 0
	for _, s := range due {
		n, err := Collect(ctx, a, s)
		if err != nil {
			a.Log.Info("trends: feed failed", "workspace", s.WorkspaceID, "source", s.ID, "err", err)
			continue
		}
		items += n
	}
	return len(due), items
}

/*
StartCollector runs CollectDue on a ticker for the life of the process.

One in-process ticker, not a job queue: Moharrik runs as a single container
(deploy/lxc/docker-compose.yml), and a second scheduler would be infrastructure
this does not have yet. When it runs on more than one replica this needs a lock
— the ON CONFLICT above makes a double poll harmless, but it is still wasted
work, so the note is here rather than in a commit message nobody reads.
*/
func StartCollector(a *app.App, interval, staleAfter time.Duration, perTick int) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// A small delay so a restart does not hammer every feed at boot.
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			sources, items := CollectDue(ctx, a, staleAfter, perTick)
			if sources > 0 {
				a.Log.Info("trends: collected", "sources", sources, "new_items", items)
			}
			timer.Reset(interval)
		}
	}()
	return cancel
}

// ---- small pg helpers -----------------------------------------------------

// pgTextArray scans a Postgres text[] without pulling in a driver-specific type.
type pgTextArray []string

func (a *pgTextArray) Scan(src any) error {
	*a = nil
	var s string
	switch v := src.(type) {
	case nil:
		return nil
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("trends: cannot scan %T into text[]", src)
	}
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return nil
	}
	s = s[1 : len(s)-1]
	if s == "" {
		return nil
	}
	for _, part := range splitPGArray(s) {
		*a = append(*a, part)
	}
	return nil
}

// splitPGArray splits on commas that are not inside quotes, and unquotes.
func splitPGArray(s string) []string {
	var out []string
	var cur strings.Builder
	inQuotes, escaped := false, false
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inQuotes = !inQuotes
		case r == ',' && !inQuotes:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	out = append(out, cur.String())
	return out
}

// pgArray renders []string as a Postgres array literal.
func pgArray(vals []string) string {
	if len(vals) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		v = strings.ReplaceAll(v, `\`, `\\`)
		v = strings.ReplaceAll(v, `"`, `\"`)
		parts = append(parts, `"`+v+`"`)
	}
	return "{" + strings.Join(parts, ",") + "}"
}
