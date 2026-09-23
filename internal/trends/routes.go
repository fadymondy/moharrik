package trends

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

var tags = []string{"Trends"}

// ---- wire types -----------------------------------------------------------

type SourceOut struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	URL             string            `json:"url"`
	Kind            string            `json:"kind"`
	Provider        string            `json:"provider" enum:"feed,hn,devto" doc:"feed = RSS/Atom; hn and devto are read through their public APIs."`
	Params          map[string]string `json:"params"`
	CatalogKey      string            `json:"catalog_key" doc:"The catalog entry this source was added from, or empty."`
	Enabled         bool              `json:"enabled"`
	IntervalMinutes int               `json:"interval_minutes" enum:"360,720,1440"`
	LastStatus      string            `json:"last_status" enum:",ok,failed,over_budget" doc:"Empty until the first fetch."`
	LastAdded       int               `json:"last_added" doc:"New items the last fetch filed."`
	LastFetchedAt   *time.Time        `json:"last_fetched_at"`
	NextRunAt       *time.Time        `json:"next_run_at" doc:"When the collector will next fetch it; null when it is off. In the past means on the next pass."`
	LastError       string            `json:"last_error"`
	LastErrorAt     *time.Time        `json:"last_error_at"`
	ItemCount       int64             `json:"item_count"`
	CreatedAt       time.Time         `json:"created_at"`
}

type TopicOut struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Keywords  []string  `json:"keywords"`
	Enabled   bool      `json:"enabled"`
	ItemCount int64     `json:"item_count"`
	CreatedAt time.Time `json:"created_at"`
}

type NewsOut struct {
	ID          string     `json:"id"`
	SourceID    *string    `json:"source_id"`
	SourceName  string     `json:"source_name"`
	Outlet      string     `json:"outlet" doc:"The publisher; an aggregator's rows name each one."`
	Provider    string     `json:"provider"`
	Title       string     `json:"title"`
	URL         string     `json:"url"`
	Summary     string     `json:"summary"`
	Author      string     `json:"author"`
	TopicIDs    []string   `json:"topic_ids"`
	ClusterID   *string    `json:"cluster_id" doc:"The trending topic (story) it belongs to."`
	Rank        int        `json:"rank" doc:"Position on an API source's list (Hacker News), 0 otherwise."`
	Points      int        `json:"points" doc:"Hacker News points or DEV reactions."`
	Comments    int        `json:"comments"`
	PublishedAt time.Time  `json:"published_at"`
	SavedAt     *time.Time `json:"saved_at"`
}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type idPath struct {
	wsHeader
	ID string `path:"id"`
}

type body[T any] struct{ Body T }

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

func scope(ctx context.Context, action string) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, action) {
		return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
	}
	return s, nil
}

func notFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func pgCode(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}

func timep(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	v := n.Time
	return &v
}

func strp(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

const sourceColumns = `id, name, url, kind, provider, params::text, catalog_key, enabled, interval_minutes, last_status, last_added,
	last_fetched_at, last_error, last_error_at, item_count, created_at`

func scanSource(row interface{ Scan(...any) error }) (SourceOut, error) {
	var so SourceOut
	var params string
	var fetched, errAt sql.NullTime
	err := row.Scan(&so.ID, &so.Name, &so.URL, &so.Kind, &so.Provider, &params, &so.CatalogKey, &so.Enabled, &so.IntervalMinutes,
		&so.LastStatus, &so.LastAdded, &fetched, &so.LastError, &errAt, &so.ItemCount, &so.CreatedAt)
	so.Params = paramsOf(params)
	so.LastFetchedAt, so.LastErrorAt = timep(fetched), timep(errAt)
	if so.Enabled {
		next := time.Now()
		if so.LastFetchedAt != nil {
			next = so.LastFetchedAt.Add(time.Duration(so.IntervalMinutes) * time.Minute)
		}
		so.NextRunAt = &next
	}
	return so, err
}

// Register mounts the trends endpoints. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	db := func() *sql.DB { return a.SQLDB }

	// ---- sources -----------------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-feed-sources", Method: http.MethodGet, Path: "/api/w/feed_sources",
		Summary: "The sources this workspace watches, with each one's schedule and status", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[struct {
		Sources []SourceOut `json:"sources"`
	}], error) {
		s, err := scope(ctx, authz.TrendsRead)
		if err != nil {
			return nil, err
		}
		rows, err := db().QueryContext(ctx, `SELECT `+sourceColumns+` FROM feed_sources WHERE workspace_id = $1 ORDER BY lower(name)`, s.WorkspaceID)
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
		return out(struct {
			Sources []SourceOut `json:"sources"`
		}{list}), nil
	})

	type createdFeedSource struct {
		Source SourceOut `json:"source"`
		Added  int       `json:"added" doc:"Items collected by the first fetch."`
	}

	huma.Register(api, huma.Operation{
		OperationID: "w-create-feed-source", Method: http.MethodPost, Path: "/api/w/feed_sources",
		DefaultStatus: http.StatusCreated,
		Summary:       "Watch a source: a catalog entry in one click, or any RSS/Atom URL. It is fetched once immediately, so a bad URL is reported now.",
		Tags:          tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			CatalogKey      string `json:"catalog_key,omitempty" maxLength:"80" doc:"A key from GET /api/w/feed_catalog. Takes the entry's name, URL, provider and interval."`
			Name            string `json:"name,omitempty" maxLength:"120" doc:"Defaults to the catalog name, or the feed's own title."`
			URL             string `json:"url,omitempty" maxLength:"2000" doc:"An RSS or Atom feed; required without catalog_key."`
			IntervalMinutes int    `json:"interval_minutes,omitempty" doc:"360, 720 or 1440. Defaults to the catalog's suggestion, else 720."`
		}
	}) (*body[createdFeedSource], error) {
		s, err := scope(ctx, authz.TrendsManage)
		if err != nil {
			return nil, err
		}
		src := Source{WorkspaceID: s.WorkspaceID, Provider: ProviderFeed, Params: map[string]string{}}
		interval := 720
		catalogKey := ""
		if key := strings.TrimSpace(in.Body.CatalogKey); key != "" {
			entry, ok := CatalogEntryFor(key)
			if !ok {
				return nil, huma.Error422UnprocessableEntity("there is no catalog source called " + key)
			}
			catalogKey = entry.Key
			src.Name, src.URL, src.Provider, interval = entry.Name, entry.URL, entry.Provider, entry.Interval
			for k, v := range entry.Params {
				src.Params[k] = v
			}
		} else {
			src.URL = strings.TrimSpace(in.Body.URL)
			if src.URL == "" {
				return nil, huma.Error422UnprocessableEntity("pick a source from the catalog, or give a feed URL")
			}
			if !strings.HasPrefix(src.URL, "http://") && !strings.HasPrefix(src.URL, "https://") {
				return nil, huma.Error422UnprocessableEntity("the URL must start with http:// or https://")
			}
			src.Name = src.URL
		}
		if n := strings.TrimSpace(in.Body.Name); n != "" {
			src.Name = n
		}
		if in.Body.IntervalMinutes != 0 {
			if !validInterval(in.Body.IntervalMinutes) {
				return nil, huma.Error422UnprocessableEntity("the interval must be 360, 720 or 1440 minutes")
			}
			interval = in.Body.IntervalMinutes
		}
		kind := "rss"
		if src.Provider != ProviderFeed {
			kind = "api"
		}
		params, _ := json.Marshal(src.Params)
		err = db().QueryRowContext(ctx, `
			INSERT INTO feed_sources (workspace_id, name, url, kind, provider, params, catalog_key, interval_minutes, created_by)
			VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9) RETURNING id`,
			s.WorkspaceID, src.Name, src.URL, kind, src.Provider, string(params), catalogKey, interval, s.UserID).Scan(&src.ID)
		if pgCode(err) == "23505" {
			return nil, huma.Error409Conflict("this workspace already watches that source")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not add the source")
		}

		// Fetch once now: a typo'd URL should fail while the person who typed it
		// is still looking, not silently on a schedule hours later.
		added, ferr := Collect(ctx, a, src)
		if errors.Is(ferr, ErrNotAFeed) && catalogKey == "" {
			// A URL that is not a feed at all will never become one, so the source
			// is not kept: otherwise a typo leaves a dead row the collector retries
			// forever. A host that was merely down is a different error and IS
			// kept, with last_error set.
			_, _ = db().ExecContext(ctx, `DELETE FROM feed_sources WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, src.ID)
			return nil, huma.Error422UnprocessableEntity(ferr.Error())
		}
		if ferr == nil && catalogKey == "" && strings.TrimSpace(in.Body.Name) == "" {
			// Name it after the feed once we know what it calls itself.
			if title := feedTitle(ctx, src.URL); title != "" {
				_, _ = db().ExecContext(ctx, `UPDATE feed_sources SET name = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, src.ID, title)
			}
		}
		if added > 0 {
			if _, err := Recluster(ctx, a, s.WorkspaceID); err != nil {
				a.Log.Warn("trends: clustering failed", "workspace", s.WorkspaceID, "err", err)
			}
			publish(a, s.WorkspaceID)
		}
		so, err := getSource(ctx, db(), s.WorkspaceID, src.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the source")
		}
		return out(createdFeedSource{so, added}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-feed-source", Method: http.MethodPatch, Path: "/api/w/feed_sources/{id}",
		Summary: "Rename a source, change how often it is fetched, or turn it on/off", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Name            *string `json:"name,omitempty" maxLength:"120"`
			Enabled         *bool   `json:"enabled,omitempty"`
			IntervalMinutes *int    `json:"interval_minutes,omitempty" doc:"360, 720 or 1440."`
		}
	}) (*body[struct {
		Source SourceOut `json:"source"`
	}], error) {
		s, err := scope(ctx, authz.TrendsManage)
		if err != nil {
			return nil, err
		}
		sets, args := []string{}, []any{}
		if in.Body.Name != nil {
			n := strings.TrimSpace(*in.Body.Name)
			if n == "" {
				return nil, huma.Error422UnprocessableEntity("a name is required")
			}
			args = append(args, n)
			sets = append(sets, "name = $"+strconv.Itoa(len(args)+2))
		}
		if in.Body.Enabled != nil {
			args = append(args, *in.Body.Enabled)
			sets = append(sets, "enabled = $"+strconv.Itoa(len(args)+2))
		}
		if in.Body.IntervalMinutes != nil {
			if !validInterval(*in.Body.IntervalMinutes) {
				return nil, huma.Error422UnprocessableEntity("the interval must be 360, 720 or 1440 minutes")
			}
			args = append(args, *in.Body.IntervalMinutes)
			sets = append(sets, "interval_minutes = $"+strconv.Itoa(len(args)+2))
		}
		if len(sets) > 0 {
			res, err := db().ExecContext(ctx, `UPDATE feed_sources SET `+strings.Join(sets, ", ")+`, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
				append([]any{s.WorkspaceID, in.ID}, args...)...)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not update the source")
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, huma.Error404NotFound("source not found")
			}
		}
		so, err := getSource(ctx, db(), s.WorkspaceID, in.ID)
		if notFound(err) {
			return nil, huma.Error404NotFound("source not found")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not read the source")
		}
		return out(struct {
			Source SourceOut `json:"source"`
		}{so}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-fetch-feed-source", Method: http.MethodPost, Path: "/api/w/feed_sources/{id}/fetch",
		Summary: "Fetch this source now (spends one fetch of today's budget)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[struct {
		Source SourceOut `json:"source"`
		Added  int       `json:"added"`
	}], error) {
		s, err := scope(ctx, authz.TrendsManage)
		if err != nil {
			return nil, err
		}
		src := Source{ID: in.ID, WorkspaceID: s.WorkspaceID}
		var params string
		if err := db().QueryRowContext(ctx, `SELECT name, url, provider, params::text FROM feed_sources WHERE workspace_id = $1 AND id = $2`,
			s.WorkspaceID, in.ID).Scan(&src.Name, &src.URL, &src.Provider, &params); err != nil {
			if notFound(err) {
				return nil, huma.Error404NotFound("source not found")
			}
			return nil, huma.Error500InternalServerError("could not read the source")
		}
		src.Params = paramsOf(params)
		added, _ := Collect(ctx, a, src)
		if added > 0 {
			if _, err := Recluster(ctx, a, s.WorkspaceID); err != nil {
				a.Log.Warn("trends: clustering failed", "workspace", s.WorkspaceID, "err", err)
			}
			publish(a, s.WorkspaceID)
		}
		// A failed or refused fetch answers 200 with the reason on the source:
		// "the feed is broken" or "the budget is spent" is an answer, not a fault.
		so, err := getSource(ctx, db(), s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the source")
		}
		return out(struct {
			Source SourceOut `json:"source"`
			Added  int       `json:"added"`
		}{so, added}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-feed-source", Method: http.MethodDelete, Path: "/api/w/feed_sources/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Stop watching a source (its items stay)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		s, err := scope(ctx, authz.TrendsManage)
		if err != nil {
			return nil, err
		}
		res, err := db().ExecContext(ctx, `DELETE FROM feed_sources WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not delete the source")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("source not found")
		}
		return &struct{}{}, nil
	})

	registerTopics(api, a)
	registerNews(api, a)
	registerStories(api, a)
}

func registerNews(api huma.API, a *app.App) {
	db := func() *sql.DB { return a.SQLDB }

	type newsPageOut struct {
		Items      []NewsOut `json:"items"`
		Total      int64     `json:"total" doc:"Every item in the workspace, whatever the filters."`
		HasMore    bool      `json:"has_more"`
		NextCursor string    `json:"next_cursor,omitempty"`
	}

	huma.Register(api, huma.Operation{
		OperationID: "w-news", Method: http.MethodGet, Path: "/api/w/news",
		Summary: "Collected items, newest first, a page at a time", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Topic   string `query:"topic" doc:"A keyword topic id."`
		Source  string `query:"source" doc:"A source id."`
		Cluster string `query:"cluster" doc:"A trending topic (story) id."`
		Saved   bool   `query:"saved"`
		Q       string `query:"q" doc:"Title or summary contains."`
		Day     string `query:"day" doc:"YYYY-MM-DD (UTC): only items published that day."`
		Cursor  string `query:"cursor" doc:"next_cursor from the previous page."`
		Limit   int    `query:"limit" minimum:"1" maximum:"200" doc:"Default 30."`
	}) (*body[newsPageOut], error) {
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
		if in.Topic != "" {
			add(`? = ANY (topic_ids)`, in.Topic)
		}
		if in.Source != "" {
			add(`source_id = ?`, in.Source)
		}
		if in.Cluster != "" {
			add(`cluster_id = ?`, in.Cluster)
		}
		if in.Saved {
			q += ` AND saved_at IS NOT NULL`
		}
		if t := strings.TrimSpace(in.Q); t != "" {
			add(`(lower(title) LIKE ? ESCAPE '\' OR lower(summary) LIKE ? ESCAPE '\')`, likePattern(t))
		}
		if in.Day != "" {
			d, err := time.Parse("2006-01-02", in.Day)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("day must be YYYY-MM-DD")
			}
			add(`published_at >= ?`, d)
			add(`published_at < ?`, d.Add(24*time.Hour))
		}
		if in.Cursor != "" {
			at, id, err := decodeNewsCursor(in.Cursor)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("cursor is not valid")
			}
			args = append(args, at, id)
			q += ` AND (published_at, id) < ($` + strconv.Itoa(len(args)-1) + `, $` + strconv.Itoa(len(args)) + `)`
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 30
		}
		q += ` ORDER BY published_at DESC, id DESC LIMIT ` + strconv.Itoa(limit+1)
		list, err := queryNews(ctx, db(), q, args...)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list the items")
		}
		page := newsPageOut{Items: list}
		if len(list) > limit {
			page.Items = list[:limit]
			last := page.Items[limit-1]
			page.HasMore = true
			page.NextCursor = encodeNewsCursor(last.PublishedAt, last.ID)
		}
		_ = db().QueryRowContext(ctx, `SELECT count(*) FROM news_items WHERE workspace_id = $1`, s.WorkspaceID).Scan(&page.Total)
		return out(page), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-news", Method: http.MethodPatch, Path: "/api/w/news/{id}",
		Summary: "Save an item for later, or unsave it", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Saved *bool `json:"saved,omitempty"`
		}
	}) (*body[struct {
		Saved bool `json:"saved"`
	}], error) {
		s, err := scope(ctx, authz.TrendsRead)
		if err != nil {
			return nil, err
		}
		if in.Body.Saved == nil {
			return nil, huma.Error422UnprocessableEntity("nothing to change")
		}
		var val any
		if *in.Body.Saved {
			val = time.Now()
		}
		res, err := db().ExecContext(ctx, `UPDATE news_items SET saved_at = $3 WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID, val)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not update the item")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("item not found")
		}
		return out(struct {
			Saved bool `json:"saved"`
		}{*in.Body.Saved}), nil
	})
}

func queryNews(ctx context.Context, db *sql.DB, q string, args ...any) ([]NewsOut, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []NewsOut{}
	for rows.Next() {
		var n NewsOut
		var src, cluster sql.NullString
		var saved sql.NullTime
		var topics pgtext.Array
		if err := rows.Scan(&n.ID, &src, &n.SourceName, &n.Outlet, &n.Provider, &n.Title, &n.URL, &n.Summary, &n.Author, &topics, &cluster,
			&n.Rank, &n.Points, &n.Comments, &n.PublishedAt, &saved); err != nil {
			return nil, err
		}
		n.SourceID, n.ClusterID, n.SavedAt = strp(src), strp(cluster), timep(saved)
		if n.Outlet == "" {
			n.Outlet = n.SourceName
		}
		n.TopicIDs = topics
		if n.TopicIDs == nil {
			n.TopicIDs = []string{}
		}
		list = append(list, n)
	}
	return list, rows.Err()
}

func encodeNewsCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeNewsCursor(c string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 || parts[1] == "" {
		return time.Time{}, "", errors.New("cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	return at, parts[1], err
}

func likePattern(q string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(q)) + "%"
}

func getSource(ctx context.Context, db *sql.DB, wsID, id string) (SourceOut, error) {
	return scanSource(db.QueryRowContext(ctx, `SELECT `+sourceColumns+` FROM feed_sources WHERE workspace_id = $1 AND id = $2`, wsID, id))
}

// feedTitle re-fetches just to read the feed's own title, for naming a source
// the person did not name. Best effort: a failure here is not an error.
func feedTitle(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", userAgent)
	res, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	f, err := Parse(res.Body)
	if err != nil {
		return ""
	}
	return trimTo(f.Title, 120)
}
