package trends

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
)

/*
The trends center's operations, once (MH-386, on top of MH-385/387).

routes.go and stories.go's REST handlers and internal/mcp's trends tools all
call these, so an agent adding a source gets exactly what a member does — the
catalog entry's provider, params and interval, the first fetch that spends one
of the day's budget, the refusal (and removal) of a URL that is not a feed, a
recluster when anything new arrived, and the realtime "trends" event — and the
two can never drift apart.

Each reads the caller from ctx and checks the role itself. Errors are huma
status errors: REST returns them as they are, MCP hands back their message.
*/

// ---- sources ----------------------------------------------------------------

// AddSourceInput is a new source: a catalog entry by key, or any feed URL.
type AddSourceInput struct {
	CatalogKey      string `json:"catalog_key,omitempty" maxLength:"80" doc:"A key from GET /api/w/feed_catalog. Takes the entry's name, URL, provider and interval."`
	Name            string `json:"name,omitempty" maxLength:"120" doc:"Defaults to the catalog name, or the feed's own title."`
	URL             string `json:"url,omitempty" maxLength:"2000" doc:"An RSS or Atom feed; required without catalog_key."`
	IntervalMinutes int    `json:"interval_minutes,omitempty" doc:"360, 720 or 1440. Defaults to the catalog's suggestion, else 720."`
}

// FeedSourcePatch changes a source. nil = unchanged.
type FeedSourcePatch struct {
	Name            *string `json:"name,omitempty" maxLength:"120"`
	Enabled         *bool   `json:"enabled,omitempty"`
	IntervalMinutes *int    `json:"interval_minutes,omitempty" doc:"360, 720 or 1440."`
}

// ListSources lists the sources this workspace watches, with each one's
// schedule and status.
func ListSources(ctx context.Context, a *app.App) ([]SourceOut, error) {
	s, err := scope(ctx, authz.TrendsRead)
	if err != nil {
		return nil, err
	}
	rows, err := a.SQLDB.QueryContext(ctx, `SELECT `+sourceColumns+` FROM feed_sources WHERE workspace_id = $1 ORDER BY lower(name)`, s.WorkspaceID)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not list sources")
	}
	defer rows.Close()
	list := []SourceOut{}
	for rows.Next() {
		so, err := scanSource(rows)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list sources")
		}
		list = append(list, so)
	}
	return list, nil
}

// CreateSource watches a source and fetches it once at once, so a bad URL is
// reported while the person (or agent) who gave it is still there. Returns the
// source and how many items the first fetch filed.
func CreateSource(ctx context.Context, a *app.App, in AddSourceInput) (*SourceOut, int, error) {
	s, err := scope(ctx, authz.TrendsManage)
	if err != nil {
		return nil, 0, err
	}
	src := Source{WorkspaceID: s.WorkspaceID, Provider: ProviderFeed, Params: map[string]string{}}
	interval := 720
	catalogKey := ""
	if key := strings.TrimSpace(in.CatalogKey); key != "" {
		entry, ok := CatalogEntryFor(key)
		if !ok {
			return nil, 0, huma.Error422UnprocessableEntity("there is no catalog source called " + key)
		}
		catalogKey = entry.Key
		src.Name, src.URL, src.Provider, interval = entry.Name, entry.URL, entry.Provider, entry.Interval
		for k, v := range entry.Params {
			src.Params[k] = v
		}
	} else {
		src.URL = strings.TrimSpace(in.URL)
		if src.URL == "" {
			return nil, 0, huma.Error422UnprocessableEntity("pick a source from the catalog, or give a feed URL")
		}
		if !strings.HasPrefix(src.URL, "http://") && !strings.HasPrefix(src.URL, "https://") {
			return nil, 0, huma.Error422UnprocessableEntity("the URL must start with http:// or https://")
		}
		if len(src.URL) > 2000 {
			return nil, 0, huma.Error422UnprocessableEntity("the URL is too long")
		}
		src.Name = src.URL
	}
	if n := strings.TrimSpace(in.Name); n != "" {
		src.Name = n
	}
	if in.IntervalMinutes != 0 {
		if !validInterval(in.IntervalMinutes) {
			return nil, 0, huma.Error422UnprocessableEntity("the interval must be 360, 720 or 1440 minutes")
		}
		interval = in.IntervalMinutes
	}
	kind := "rss"
	if src.Provider != ProviderFeed {
		kind = "api"
	}
	params, _ := json.Marshal(src.Params)
	err = a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO feed_sources (workspace_id, name, url, kind, provider, params, catalog_key, interval_minutes, created_by)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9) RETURNING id`,
		s.WorkspaceID, src.Name, src.URL, kind, src.Provider, string(params), catalogKey, interval, s.UserID).Scan(&src.ID)
	if pgCode(err) == "23505" {
		return nil, 0, huma.Error409Conflict("this workspace already watches that source")
	} else if err != nil {
		return nil, 0, huma.Error500InternalServerError("could not add the source")
	}

	// Fetch once now: a typo'd URL should fail while the person who typed it
	// is still looking, not silently on a schedule hours later.
	added, ferr := Collect(ctx, a, src)
	if errors.Is(ferr, ErrNotAFeed) && catalogKey == "" {
		// A URL that is not a feed at all will never become one, so the source
		// is not kept: otherwise a typo leaves a dead row the collector retries
		// forever. A host that was merely down is a different error and IS
		// kept, with last_error set.
		_, _ = a.SQLDB.ExecContext(ctx, `DELETE FROM feed_sources WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, src.ID)
		return nil, 0, huma.Error422UnprocessableEntity(ferr.Error())
	}
	if ferr == nil && catalogKey == "" && strings.TrimSpace(in.Name) == "" {
		// Name it after the feed once we know what it calls itself.
		if title := feedTitle(ctx, src.URL); title != "" {
			_, _ = a.SQLDB.ExecContext(ctx, `UPDATE feed_sources SET name = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, src.ID, title)
		}
	}
	if added > 0 {
		afterCollect(ctx, a, s.WorkspaceID)
	}
	so, err := getSource(ctx, a.SQLDB, s.WorkspaceID, src.ID)
	if err != nil {
		return nil, 0, huma.Error500InternalServerError("could not read the source")
	}
	return &so, added, nil
}

// UpdateSource renames a source, changes how often it is fetched, or turns
// it on or off.
func UpdateSource(ctx context.Context, a *app.App, id string, p FeedSourcePatch) (*SourceOut, error) {
	s, err := scope(ctx, authz.TrendsManage)
	if err != nil {
		return nil, err
	}
	sets, args := []string{}, []any{}
	if p.Name != nil {
		n := strings.TrimSpace(*p.Name)
		if n == "" {
			return nil, huma.Error422UnprocessableEntity("a name is required")
		}
		args = append(args, n)
		sets = append(sets, "name = $"+strconv.Itoa(len(args)+2))
	}
	if p.Enabled != nil {
		args = append(args, *p.Enabled)
		sets = append(sets, "enabled = $"+strconv.Itoa(len(args)+2))
	}
	if p.IntervalMinutes != nil {
		if !validInterval(*p.IntervalMinutes) {
			return nil, huma.Error422UnprocessableEntity("the interval must be 360, 720 or 1440 minutes")
		}
		args = append(args, *p.IntervalMinutes)
		sets = append(sets, "interval_minutes = $"+strconv.Itoa(len(args)+2))
	}
	if len(sets) > 0 {
		res, err := a.SQLDB.ExecContext(ctx, `UPDATE feed_sources SET `+strings.Join(sets, ", ")+`, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
			append([]any{s.WorkspaceID, id}, args...)...)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not update the source")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("source not found")
		}
	}
	so, err := getSource(ctx, a.SQLDB, s.WorkspaceID, id)
	if notFound(err) {
		return nil, huma.Error404NotFound("source not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the source")
	}
	return &so, nil
}

// FetchSource fetches one source now, spending one fetch of today's budget. A
// failed or refused fetch is not an error: the reason is on the source
// (last_status, last_error) — "the feed is broken" or "the budget is spent"
// is an answer, not a fault.
func FetchSource(ctx context.Context, a *app.App, id string) (*SourceOut, int, error) {
	s, err := scope(ctx, authz.TrendsManage)
	if err != nil {
		return nil, 0, err
	}
	src := Source{ID: id, WorkspaceID: s.WorkspaceID}
	var params string
	if err := a.SQLDB.QueryRowContext(ctx, `SELECT name, url, provider, params::text FROM feed_sources WHERE workspace_id = $1 AND id = $2`,
		s.WorkspaceID, id).Scan(&src.Name, &src.URL, &src.Provider, &params); err != nil {
		if notFound(err) {
			return nil, 0, huma.Error404NotFound("source not found")
		}
		return nil, 0, huma.Error500InternalServerError("could not read the source")
	}
	src.Params = paramsOf(params)
	added, _ := Collect(ctx, a, src)
	if added > 0 {
		afterCollect(ctx, a, s.WorkspaceID)
	}
	so, err := getSource(ctx, a.SQLDB, s.WorkspaceID, id)
	if err != nil {
		return nil, 0, huma.Error500InternalServerError("could not read the source")
	}
	return &so, added, nil
}

// CollectResult is what one workspace-wide collection did.
type CollectResult struct {
	Sources []CollectedSource `json:"sources"`
	// Added is the new items across every source.
	Added int `json:"added"`
	// Failed is how many sources could not be fetched; each says why.
	Failed int `json:"failed"`
	// OverBudget is how many were not fetched because today's budget is spent.
	OverBudget int `json:"over_budget"`
	// Clustered is true when new items arrived and were placed into trending
	// topics (Recluster ran). NewTopics is how many stories that started; new
	// items that joined an existing story start none.
	Clustered bool `json:"clustered"`
	NewTopics int  `json:"new_topics"`
	// Budget is the budget after the collection.
	Budget TrendSettings `json:"budget"`
}

// CollectedSource is one source's part of a collection.
type CollectedSource struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Added  int    `json:"added"`
	Status string `json:"status" enum:"ok,failed,over_budget"`
	Error  string `json:"error,omitempty"`
}

// collectParallel bounds how many of one workspace's sources are fetched at once.
const collectParallel = 4

/*
CollectWorkspace fetches every enabled source of the caller's workspace now,
instead of waiting for each one's interval.

It spends the same daily budget the collector does — each fetch takes one
(Collect's takeBudget, one atomic statement) — so it cannot be used to go past
what the workspace allows. It stops starting fetches once the budget is spent;
the sources it did not reach are reported over_budget, and stay due, exactly
as the collector leaves them. When anything new arrived it reclusters the
workspace once and tells the open tabs, the same as a scheduled pass.
*/
func CollectWorkspace(ctx context.Context, a *app.App) (*CollectResult, error) {
	s, err := scope(ctx, authz.TrendsManage)
	if err != nil {
		return nil, err
	}
	rows, err := a.SQLDB.QueryContext(ctx, `
		SELECT id, name, url, provider, params::text FROM feed_sources
		WHERE workspace_id = $1 AND enabled ORDER BY last_fetched_at NULLS FIRST, id LIMIT 200`, s.WorkspaceID)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the sources")
	}
	var due []Source
	for rows.Next() {
		src := Source{WorkspaceID: s.WorkspaceID}
		var params string
		if err := rows.Scan(&src.ID, &src.Name, &src.URL, &src.Provider, &params); err != nil {
			rows.Close()
			return nil, huma.Error500InternalServerError("could not read the sources")
		}
		src.Params = paramsOf(params)
		due = append(due, src)
	}
	rows.Close()

	res := &CollectResult{Sources: make([]CollectedSource, len(due))}
	var mu sync.Mutex
	spent := false
	sem := make(chan struct{}, collectParallel)
	var wg sync.WaitGroup
	for i, src := range due {
		wg.Add(1)
		go func(i int, src Source) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out := CollectedSource{ID: src.ID, Name: src.Name, Status: "ok"}
			mu.Lock()
			stop := spent
			mu.Unlock()
			if stop {
				out.Status, out.Error = "over_budget", ErrOverBudget.Error()
				res.Sources[i] = out
				return
			}
			n, err := Collect(ctx, a, src)
			out.Added = n
			switch {
			case errors.Is(err, ErrOverBudget):
				out.Status, out.Error = "over_budget", err.Error()
				mu.Lock()
				spent = true
				mu.Unlock()
			case err != nil:
				out.Status, out.Error = "failed", err.Error()
			}
			res.Sources[i] = out
		}(i, src)
	}
	wg.Wait()
	for _, c := range res.Sources {
		res.Added += c.Added
		switch c.Status {
		case "failed":
			res.Failed++
		case "over_budget":
			res.OverBudget++
		}
	}
	if res.Added > 0 {
		res.NewTopics = afterCollect(ctx, a, s.WorkspaceID)
		res.Clustered = true
	}
	if res.Budget, err = getSettings(ctx, a.SQLDB, s.WorkspaceID); err != nil {
		return nil, huma.Error500InternalServerError("could not read the budget")
	}
	return res, nil
}

// afterCollect is what follows new items, whoever collected them: group them
// into trending topics, and tell the workspace's open tabs.
func afterCollect(ctx context.Context, a *app.App, wsID string) int {
	n, err := Recluster(ctx, a, wsID)
	if err != nil {
		a.Log.Warn("trends: clustering failed", "workspace", wsID, "err", err)
	}
	publish(a, wsID)
	return n
}

// ---- the catalog and the budget -------------------------------------------------

// CatalogOut is a catalog entry and this workspace's source for it, if any.
type CatalogOut struct {
	FeedCatalogEntry
	SourceID *string `json:"source_id" doc:"This workspace's source for the entry, when it has added it."`
}

// ListCatalog lists the sources a workspace can add in one click, and which it
// already has.
func ListCatalog(ctx context.Context, a *app.App) ([]CatalogOut, error) {
	s, err := scope(ctx, authz.TrendsRead)
	if err != nil {
		return nil, err
	}
	byKey, byURL := map[string]string{}, map[string]string{}
	rows, err := a.SQLDB.QueryContext(ctx, `SELECT id, catalog_key, lower(url) FROM feed_sources WHERE workspace_id = $1`, s.WorkspaceID)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the catalog")
	}
	for rows.Next() {
		var id, key, u string
		if rows.Scan(&id, &key, &u) == nil {
			if key != "" {
				byKey[key] = id
			}
			byURL[u] = id
		}
	}
	rows.Close()
	list := make([]CatalogOut, 0, len(Catalog))
	for _, e := range Catalog {
		c := CatalogOut{FeedCatalogEntry: e}
		if c.Params == nil {
			c.Params = map[string]string{}
		}
		if id, ok := byKey[e.Key]; ok {
			c.SourceID = &id
		} else if id, ok := byURL[strings.ToLower(e.URL)]; ok {
			c.SourceID = &id
		}
		list = append(list, c)
	}
	return list, nil
}

// GetBudget reads the workspace's daily collection budget and what it has
// spent today.
func GetBudget(ctx context.Context, a *app.App) (*TrendSettings, error) {
	s, err := scope(ctx, authz.TrendsRead)
	if err != nil {
		return nil, err
	}
	st, err := getSettings(ctx, a.SQLDB, s.WorkspaceID)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the budget")
	}
	return &st, nil
}

// SetBudget changes the daily collection budget.
func SetBudget(ctx context.Context, a *app.App, perDay int) (*TrendSettings, error) {
	s, err := scope(ctx, authz.TrendsManage)
	if err != nil {
		return nil, err
	}
	if perDay < 1 || perDay > MaxBudget {
		return nil, huma.Error422UnprocessableEntity("the budget must be between 1 and 500 fetches a day")
	}
	if _, err := a.SQLDB.ExecContext(ctx, `
		INSERT INTO trend_settings (workspace_id, fetch_budget_per_day, budget_day) VALUES ($1, $2, (now() AT TIME ZONE 'UTC')::date)
		ON CONFLICT (workspace_id) DO UPDATE SET fetch_budget_per_day = EXCLUDED.fetch_budget_per_day, updated_at = now()`,
		s.WorkspaceID, perDay); err != nil {
		return nil, huma.Error500InternalServerError("could not save the budget")
	}
	st, err := getSettings(ctx, a.SQLDB, s.WorkspaceID)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the budget")
	}
	return &st, nil
}

// ---- items ------------------------------------------------------------------------

// NewsFilter narrows the items. Every field is optional.
type NewsFilter struct {
	TopicID   string // a keyword topic
	SourceID  string
	ClusterID string // a trending topic (story)
	Saved     bool
	Q         string
	Day       string // YYYY-MM-DD (UTC)
	Cursor    string
	Limit     int
}

// NewsPage is one page of items.
type NewsPage struct {
	Items      []NewsOut `json:"items"`
	Total      int64     `json:"total" doc:"Every item in the workspace, whatever the filters."`
	HasMore    bool      `json:"has_more"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// ListNews is the collected items, newest first, a page at a time.
func ListNews(ctx context.Context, a *app.App, f NewsFilter) (*NewsPage, error) {
	s, err := scope(ctx, authz.TrendsRead)
	if err != nil {
		return nil, err
	}
	q := `SELECT id, source_id, source_name, outlet, provider, title, url, summary, author, topic_ids, cluster_id,
	             rank, points, comments, published_at, saved_at
	      FROM news_items WHERE workspace_id = $1`
	args := []any{s.WorkspaceID}
	add := func(clause string, v any) {
		args = append(args, v)
		q += " AND " + strings.ReplaceAll(clause, "?", "$"+strconv.Itoa(len(args)))
	}
	if f.TopicID != "" {
		add(`? = ANY (topic_ids)`, f.TopicID)
	}
	if f.SourceID != "" {
		add(`source_id = ?`, f.SourceID)
	}
	if f.ClusterID != "" {
		add(`cluster_id = ?`, f.ClusterID)
	}
	if f.Saved {
		q += ` AND saved_at IS NOT NULL`
	}
	if t := strings.TrimSpace(f.Q); t != "" {
		add(`(lower(title) LIKE ? ESCAPE '\' OR lower(summary) LIKE ? ESCAPE '\')`, likePattern(t))
	}
	if f.Day != "" {
		d, err := time.Parse("2006-01-02", f.Day)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("day must be YYYY-MM-DD")
		}
		add(`published_at >= ?`, d)
		add(`published_at < ?`, d.Add(24*time.Hour))
	}
	if f.Cursor != "" {
		at, id, err := decodeNewsCursor(f.Cursor)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("cursor is not valid")
		}
		args = append(args, at, id)
		q += ` AND (published_at, id) < ($` + strconv.Itoa(len(args)-1) + `, $` + strconv.Itoa(len(args)) + `)`
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 30
	}
	if limit > 200 {
		limit = 200
	}
	q += ` ORDER BY published_at DESC, id DESC LIMIT ` + strconv.Itoa(limit+1)
	list, err := queryNews(ctx, a.SQLDB, q, args...)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not list the items")
	}
	page := &NewsPage{Items: list}
	if len(list) > limit {
		page.Items = list[:limit]
		last := page.Items[limit-1]
		page.HasMore = true
		page.NextCursor = encodeNewsCursor(last.PublishedAt, last.ID)
	}
	_ = a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM news_items WHERE workspace_id = $1`, s.WorkspaceID).Scan(&page.Total)
	return page, nil
}

// SetSaved saves an item for later, or unsaves it.
func SetSaved(ctx context.Context, a *app.App, id string, saved bool) error {
	s, err := scope(ctx, authz.TrendsRead)
	if err != nil {
		return err
	}
	var val any
	if saved {
		val = time.Now()
	}
	res, err := a.SQLDB.ExecContext(ctx, `UPDATE news_items SET saved_at = $3 WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, id, val)
	if err != nil {
		return huma.Error500InternalServerError("could not update the item")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return huma.Error404NotFound("item not found")
	}
	return nil
}

// ---- trending topics (stories, MH-385) --------------------------------------------

// TopicFilter narrows the trending topics. Every field is optional.
type TopicFilter struct {
	State    string // open (default), saved, reviewed, dismissed, all
	Day      string // YYYY-MM-DD (UTC)
	SourceID string
	Q        string
	Cursor   string
	Limit    int
}

// ListTrendTopics is the trending topics — stories clustered across sources —
// newest day first and best first, a page at a time, with the counts per
// state and the days that have stories.
func ListTrendTopics(ctx context.Context, a *app.App, f TopicFilter) (*TrendTopicPage, error) {
	s, err := scope(ctx, authz.TrendsRead)
	if err != nil {
		return nil, err
	}
	db := a.SQLDB
	stateSQL, ok := stateClause(f.State)
	if !ok {
		return nil, huma.Error422UnprocessableEntity("state must be open, saved, reviewed, dismissed or all")
	}
	// The filters every count shares: day, source, search.
	base := `workspace_id = $1`
	args := []any{s.WorkspaceID}
	add := func(clause string, v any) {
		args = append(args, v)
		base += " AND " + strings.ReplaceAll(clause, "?", "$"+strconv.Itoa(len(args)))
	}
	if f.SourceID != "" {
		add(`? = ANY (source_ids)`, f.SourceID)
	}
	if t := strings.TrimSpace(f.Q); t != "" {
		add(`(lower(title) LIKE ? ESCAPE '\' OR lower(summary) LIKE ? ESCAPE '\')`, likePattern(t))
	}
	baseNoDay, argsNoDay := base, append([]any{}, args...)
	if f.Day != "" {
		if _, err := time.Parse("2006-01-02", f.Day); err != nil {
			return nil, huma.Error422UnprocessableEntity("day must be YYYY-MM-DD")
		}
		add(`day = ?::date`, f.Day)
	}

	where, listArgs := base, append([]any{}, args...)
	if stateSQL != "" {
		where += " AND " + stateSQL
	}
	if f.Cursor != "" {
		c, ok := decodeStoryCursor(f.Cursor)
		if !ok {
			return nil, huma.Error422UnprocessableEntity("cursor is not valid")
		}
		n := len(listArgs)
		listArgs = append(listArgs, c.Day, c.Score, c.ID)
		where += ` AND (day, score, id) < ($` + strconv.Itoa(n+1) + `::date, $` + strconv.Itoa(n+2) + `::double precision, $` + strconv.Itoa(n+3) + `)`
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx, `SELECT `+storyColumns+` FROM trend_clusters WHERE `+where+`
		ORDER BY day DESC, score DESC, id DESC LIMIT `+strconv.Itoa(limit+1), listArgs...)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not list the topics")
	}
	page := &TrendTopicPage{Topics: []TrendTopicOut{}, Days: []TrendDay{}}
	for rows.Next() {
		st, err := scanStory(rows)
		if err != nil {
			rows.Close()
			return nil, huma.Error500InternalServerError("could not list the topics")
		}
		page.Topics = append(page.Topics, st)
	}
	rows.Close()
	if len(page.Topics) > limit {
		page.Topics = page.Topics[:limit]
		last := page.Topics[limit-1]
		page.HasMore = true
		page.NextCursor = encodeStoryCursor(storyCursor{last.Day, last.Score, last.ID})
	}
	if err := attachItems(ctx, db, s.WorkspaceID, page.Topics, 3); err != nil {
		return nil, huma.Error500InternalServerError("could not list the topics")
	}

	if err := db.QueryRowContext(ctx, `SELECT
		count(*) FILTER (WHERE reviewed_at IS NULL AND dismissed_at IS NULL AND saved_at IS NULL),
		count(*) FILTER (WHERE saved_at IS NOT NULL),
		count(*) FILTER (WHERE reviewed_at IS NOT NULL AND dismissed_at IS NULL),
		count(*) FILTER (WHERE dismissed_at IS NOT NULL),
		count(*)
		FROM trend_clusters WHERE `+base, args...).Scan(&page.Counts.Open, &page.Counts.Saved, &page.Counts.Reviewed,
		&page.Counts.Dismissed, &page.Counts.All); err != nil {
		return nil, huma.Error500InternalServerError("could not count the topics")
	}
	dayWhere := baseNoDay
	if stateSQL != "" {
		dayWhere += " AND " + stateSQL
	}
	drows, err := db.QueryContext(ctx, `SELECT day, count(*) FROM trend_clusters WHERE `+dayWhere+`
		GROUP BY day ORDER BY day DESC LIMIT 30`, argsNoDay...)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not list the days")
	}
	defer drows.Close()
	for drows.Next() {
		var d time.Time
		var fd TrendDay
		if drows.Scan(&d, &fd.Count) == nil {
			fd.Day = d.UTC().Format("2006-01-02")
			page.Days = append(page.Days, fd)
		}
	}
	return page, nil
}

// TrendTopicDetail is one trending topic with every linked item and outlet.
type TrendTopicDetail struct {
	Topic   TrendTopicOut `json:"topic"`
	Outlets []TrendOutlet `json:"outlets" doc:"Every outlet that carried it, most items first."`
}

// GetTrendTopic reads one trending topic with every linked item and outlet.
func GetTrendTopic(ctx context.Context, a *app.App, id string) (*TrendTopicDetail, error) {
	s, err := scope(ctx, authz.TrendsRead)
	if err != nil {
		return nil, err
	}
	st, err := getStory(ctx, a.SQLDB, s.WorkspaceID, id)
	if notFound(err) {
		return nil, huma.Error404NotFound("topic not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the topic")
	}
	list := []TrendTopicOut{st}
	if err := attachItems(ctx, a.SQLDB, s.WorkspaceID, list, 0); err != nil {
		return nil, huma.Error500InternalServerError("could not read the topic")
	}
	counts := map[string]int{}
	for _, n := range list[0].Items {
		counts[n.Outlet]++
	}
	outlets := []TrendOutlet{}
	for o, c := range counts {
		outlets = append(outlets, TrendOutlet{o, c})
	}
	sort.Slice(outlets, func(i, j int) bool {
		if outlets[i].Count != outlets[j].Count {
			return outlets[i].Count > outlets[j].Count
		}
		return outlets[i].Outlet < outlets[j].Outlet
	})
	return &TrendTopicDetail{list[0], outlets}, nil
}

// TopicReview marks a trending topic. nil = unchanged.
type TopicReview struct {
	Reviewed  *bool `json:"reviewed,omitempty"`
	Dismissed *bool `json:"dismissed,omitempty"`
	Saved     *bool `json:"saved,omitempty"`
}

// ReviewTrendTopic reviews, dismisses or saves a trending topic, or undoes any
// of them. Saving takes it out of dismissed and dismissing out of saved;
// reviewed is independent.
func ReviewTrendTopic(ctx context.Context, a *app.App, id string, r TopicReview) (*TrendTopicOut, error) {
	s, err := scope(ctx, authz.TrendsRead)
	if err != nil {
		return nil, err
	}
	sets := []string{}
	on := func(col string, v bool) {
		if v {
			sets = append(sets, col+" = now()")
		} else {
			sets = append(sets, col+" = NULL")
		}
	}
	if r.Reviewed != nil {
		on("reviewed_at", *r.Reviewed)
		if *r.Reviewed {
			sets = append(sets, "reviewed_by = $3")
		}
	}
	if r.Dismissed != nil {
		on("dismissed_at", *r.Dismissed)
		if *r.Dismissed && r.Saved == nil {
			sets = append(sets, "saved_at = NULL")
		}
	}
	if r.Saved != nil {
		on("saved_at", *r.Saved)
		if *r.Saved && r.Dismissed == nil {
			sets = append(sets, "dismissed_at = NULL")
		}
	}
	if len(sets) == 0 {
		return nil, huma.Error422UnprocessableEntity("nothing to change")
	}
	args := []any{s.WorkspaceID, id}
	if strings.Contains(strings.Join(sets, ","), "$3") {
		args = append(args, s.UserID)
	}
	res, err := a.SQLDB.ExecContext(ctx, `UPDATE trend_clusters SET `+strings.Join(sets, ", ")+`, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, args...)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not update the topic")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, huma.Error404NotFound("topic not found")
	}
	st, err := getStory(ctx, a.SQLDB, s.WorkspaceID, id)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the topic")
	}
	list := []TrendTopicOut{st}
	_ = attachItems(ctx, a.SQLDB, s.WorkspaceID, list, 3)
	publish(a, s.WorkspaceID)
	return &list[0], nil
}
