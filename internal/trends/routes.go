package trends

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

var tags = []string{"Trends"}

// ---- wire types -----------------------------------------------------------

type SourceOut struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	URL           string     `json:"url"`
	Kind          string     `json:"kind"`
	Enabled       bool       `json:"enabled"`
	LastFetchedAt *time.Time `json:"last_fetched_at"`
	LastError     string     `json:"last_error"`
	LastErrorAt   *time.Time `json:"last_error_at"`
	ItemCount     int64      `json:"item_count"`
	CreatedAt     time.Time  `json:"created_at"`
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
	Title       string     `json:"title"`
	URL         string     `json:"url"`
	Summary     string     `json:"summary"`
	Author      string     `json:"author"`
	TopicIDs    []string   `json:"topic_ids"`
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

// Register mounts the trends endpoints. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	db := func() *sql.DB { return a.SQLDB }

	// ---- sources -----------------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-feed-sources", Method: http.MethodGet, Path: "/api/w/feed_sources",
		Summary: "The feeds this workspace watches", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[struct {
		Sources []SourceOut `json:"sources"`
	}], error) {
		s, err := scope(ctx, authz.TrendsRead)
		if err != nil {
			return nil, err
		}
		rows, err := db().QueryContext(ctx, `
			SELECT id, name, url, kind, enabled, last_fetched_at, last_error, last_error_at, item_count, created_at
			FROM feed_sources WHERE workspace_id = $1 ORDER BY lower(name)`, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list feeds")
		}
		defer rows.Close()
		list := []SourceOut{}
		for rows.Next() {
			var so SourceOut
			var fetched, errAt sql.NullTime
			if err := rows.Scan(&so.ID, &so.Name, &so.URL, &so.Kind, &so.Enabled, &fetched, &so.LastError, &errAt, &so.ItemCount, &so.CreatedAt); err != nil {
				return nil, huma.Error500InternalServerError("could not list feeds")
			}
			so.LastFetchedAt, so.LastErrorAt = timep(fetched), timep(errAt)
			list = append(list, so)
		}
		return out(struct {
			Sources []SourceOut `json:"sources"`
		}{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-feed-source", Method: http.MethodPost, Path: "/api/w/feed_sources",
		DefaultStatus: http.StatusCreated, Summary: "Watch a feed (it is fetched once immediately, so a bad URL is reported now)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Name string `json:"name,omitempty" maxLength:"120" doc:"Defaults to the feed's own title."`
			URL  string `json:"url" minLength:"8" maxLength:"2000"`
		}
	}) (*body[struct {
		Source SourceOut `json:"source"`
		Added  int       `json:"added" doc:"Items collected by the first fetch."`
	}], error) {
		s, err := scope(ctx, authz.TrendsManage)
		if err != nil {
			return nil, err
		}
		url := strings.TrimSpace(in.Body.URL)
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			return nil, huma.Error422UnprocessableEntity("the URL must start with http:// or https://")
		}
		name := strings.TrimSpace(in.Body.Name)
		if name == "" {
			name = url
		}
		var id string
		err = db().QueryRowContext(ctx, `
			INSERT INTO feed_sources (workspace_id, name, url, created_by) VALUES ($1, $2, $3, $4) RETURNING id`,
			s.WorkspaceID, name, url, s.UserID).Scan(&id)
		if pgCode(err) == "23505" {
			return nil, huma.Error409Conflict("this workspace already watches that URL")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not add the feed")
		}

		// Fetch once now: a typo'd URL should fail while the person who typed it
		// is still looking, not silently on a ticker twenty minutes later.
		added, ferr := Collect(ctx, a, Source{ID: id, WorkspaceID: s.WorkspaceID, Name: name, URL: url})
		if errors.Is(ferr, ErrNotAFeed) {
			// A URL that is not a feed at all will never become one, so the source
			// is not kept: otherwise a typo leaves a dead row the collector retries
			// forever and the person has to notice and delete. A host that was
			// merely down is a different error and IS kept, with last_error set.
			_, _ = db().ExecContext(ctx, `DELETE FROM feed_sources WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, id)
			return nil, huma.Error422UnprocessableEntity(ferr.Error())
		}
		so, err := getSource(ctx, db(), s.WorkspaceID, id)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the feed")
		}
		if ferr == nil && strings.TrimSpace(in.Body.Name) == "" {
			// Name it after the feed once we know what it calls itself.
			if title := feedTitle(ctx, a, url); title != "" {
				_, _ = db().ExecContext(ctx, `UPDATE feed_sources SET name = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, id, title)
				so.Name = title
			}
		}
		return out(struct {
			Source SourceOut `json:"source"`
			Added  int       `json:"added" doc:"Items collected by the first fetch."`
		}{so, added}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-feed-source", Method: http.MethodPatch, Path: "/api/w/feed_sources/{id}",
		Summary: "Rename a feed, or enable/disable it", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Name    *string `json:"name,omitempty" maxLength:"120"`
			Enabled *bool   `json:"enabled,omitempty"`
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
		if len(sets) > 0 {
			res, err := db().ExecContext(ctx, `UPDATE feed_sources SET `+strings.Join(sets, ", ")+`, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
				append([]any{s.WorkspaceID, in.ID}, args...)...)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not update the feed")
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, huma.Error404NotFound("feed not found")
			}
		}
		so, err := getSource(ctx, db(), s.WorkspaceID, in.ID)
		if notFound(err) {
			return nil, huma.Error404NotFound("feed not found")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not read the feed")
		}
		return out(struct {
			Source SourceOut `json:"source"`
		}{so}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-fetch-feed-source", Method: http.MethodPost, Path: "/api/w/feed_sources/{id}/fetch",
		Summary: "Fetch this feed now", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[struct {
		Source SourceOut `json:"source"`
		Added  int       `json:"added"`
	}], error) {
		s, err := scope(ctx, authz.TrendsManage)
		if err != nil {
			return nil, err
		}
		var name, url string
		if err := db().QueryRowContext(ctx, `SELECT name, url FROM feed_sources WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID).Scan(&name, &url); err != nil {
			if notFound(err) {
				return nil, huma.Error404NotFound("feed not found")
			}
			return nil, huma.Error500InternalServerError("could not read the feed")
		}
		added, ferr := Collect(ctx, a, Source{ID: in.ID, WorkspaceID: s.WorkspaceID, Name: name, URL: url})
		so, err := getSource(ctx, db(), s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the feed")
		}
		if ferr != nil {
			// The row carries the reason; answer 200 with it rather than a 500,
			// because "the feed is broken" is an answer, not a server fault.
			return out(struct {
				Source SourceOut `json:"source"`
				Added  int       `json:"added"`
			}{so, 0}), nil
		}
		return out(struct {
			Source SourceOut `json:"source"`
			Added  int       `json:"added"`
		}{so, added}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-feed-source", Method: http.MethodDelete, Path: "/api/w/feed_sources/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Stop watching a feed (its items stay)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		s, err := scope(ctx, authz.TrendsManage)
		if err != nil {
			return nil, err
		}
		res, err := db().ExecContext(ctx, `DELETE FROM feed_sources WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not delete the feed")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("feed not found")
		}
		return &struct{}{}, nil
	})

	// ---- topics ------------------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-topics", Method: http.MethodGet, Path: "/api/w/topics",
		Summary: "Topics this workspace watches, with how many items matched", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[struct {
		Topics []TopicOut `json:"topics"`
	}], error) {
		s, err := scope(ctx, authz.TrendsRead)
		if err != nil {
			return nil, err
		}
		rows, err := db().QueryContext(ctx, `
			SELECT t.id, t.name, t.keywords, t.enabled, t.created_at,
			       (SELECT count(*) FROM news_items n WHERE n.workspace_id = t.workspace_id AND t.id = ANY (n.topic_ids))
			FROM topics t WHERE t.workspace_id = $1 ORDER BY lower(t.name)`, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list topics")
		}
		defer rows.Close()
		list := []TopicOut{}
		for rows.Next() {
			var t TopicOut
			var kw pgTextArray
			if err := rows.Scan(&t.ID, &t.Name, &kw, &t.Enabled, &t.CreatedAt, &t.ItemCount); err != nil {
				return nil, huma.Error500InternalServerError("could not list topics")
			}
			t.Keywords = kw
			if t.Keywords == nil {
				t.Keywords = []string{}
			}
			list = append(list, t)
		}
		return out(struct {
			Topics []TopicOut `json:"topics"`
		}{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-topic", Method: http.MethodPost, Path: "/api/w/topics",
		DefaultStatus: http.StatusCreated, Summary: "Watch a topic. With no keywords it matches its own name.", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Name     string   `json:"name" minLength:"1" maxLength:"80"`
			Keywords []string `json:"keywords,omitempty" maxItems:"40"`
		}
	}) (*body[struct {
		Topic TopicOut `json:"topic"`
	}], error) {
		s, err := scope(ctx, authz.TrendsManage)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSpace(in.Body.Name)
		if name == "" {
			return nil, huma.Error422UnprocessableEntity("a name is required")
		}
		var id string
		err = db().QueryRowContext(ctx, `
			INSERT INTO topics (workspace_id, name, keywords, created_by)
			VALUES ($1, $2, ARRAY(SELECT jsonb_array_elements_text($3::jsonb)), $4) RETURNING id`,
			s.WorkspaceID, name, jsonArray(cleanKeywords(in.Body.Keywords)), s.UserID).Scan(&id)
		if pgCode(err) == "23505" {
			return nil, huma.Error409Conflict("a topic with that name already exists")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not create the topic")
		}
		// Match what is already collected, so a new topic is not empty until the
		// next fetch: the items are already here, they just were not labelled.
		_, _ = db().ExecContext(ctx, backfillSQL, s.WorkspaceID, id)
		t, err := getTopic(ctx, db(), s.WorkspaceID, id)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the topic")
		}
		return out(struct {
			Topic TopicOut `json:"topic"`
		}{t}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-topic", Method: http.MethodPatch, Path: "/api/w/topics/{id}",
		Summary: "Rename a topic, change its keywords, or enable/disable it", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Name     *string   `json:"name,omitempty" maxLength:"80"`
			Keywords *[]string `json:"keywords,omitempty" maxItems:"40"`
			Enabled  *bool     `json:"enabled,omitempty"`
		}
	}) (*body[struct {
		Topic TopicOut `json:"topic"`
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
		if in.Body.Keywords != nil {
			args = append(args, jsonArray(cleanKeywords(*in.Body.Keywords)))
			sets = append(sets, "keywords = ARRAY(SELECT jsonb_array_elements_text($"+strconv.Itoa(len(args)+2)+"::jsonb))")
		}
		if in.Body.Enabled != nil {
			args = append(args, *in.Body.Enabled)
			sets = append(sets, "enabled = $"+strconv.Itoa(len(args)+2))
		}
		if len(sets) > 0 {
			res, err := db().ExecContext(ctx, `UPDATE topics SET `+strings.Join(sets, ", ")+`, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
				append([]any{s.WorkspaceID, in.ID}, args...)...)
			if pgCode(err) == "23505" {
				return nil, huma.Error409Conflict("a topic with that name already exists")
			} else if err != nil {
				return nil, huma.Error500InternalServerError("could not update the topic")
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, huma.Error404NotFound("topic not found")
			}
		}
		// Keywords changed means the labels are stale: relabel what is collected.
		if in.Body.Keywords != nil {
			_, _ = db().ExecContext(ctx, `UPDATE news_items SET topic_ids = array_remove(topic_ids, $2) WHERE workspace_id = $1 AND $2 = ANY (topic_ids)`, s.WorkspaceID, in.ID)
			_, _ = db().ExecContext(ctx, backfillSQL, s.WorkspaceID, in.ID)
		}
		t, err := getTopic(ctx, db(), s.WorkspaceID, in.ID)
		if notFound(err) {
			return nil, huma.Error404NotFound("topic not found")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not read the topic")
		}
		return out(struct {
			Topic TopicOut `json:"topic"`
		}{t}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-topic", Method: http.MethodDelete, Path: "/api/w/topics/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Stop watching a topic (items stay, unlabelled)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		s, err := scope(ctx, authz.TrendsManage)
		if err != nil {
			return nil, err
		}
		res, err := db().ExecContext(ctx, `DELETE FROM topics WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not delete the topic")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("topic not found")
		}
		_, _ = db().ExecContext(ctx, `UPDATE news_items SET topic_ids = array_remove(topic_ids, $2) WHERE workspace_id = $1 AND $2 = ANY (topic_ids)`, s.WorkspaceID, in.ID)
		return &struct{}{}, nil
	})

	// ---- the timeline ------------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-news", Method: http.MethodGet, Path: "/api/w/news",
		Summary: "Collected items, newest first", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Topic  string `query:"topic" doc:"A topic id."`
		Source string `query:"source" doc:"A feed id."`
		Saved  bool   `query:"saved"`
		Q      string `query:"q" doc:"Title or summary contains."`
		Limit  int    `query:"limit" minimum:"1" maximum:"200"`
	}) (*body[struct {
		Items []NewsOut `json:"items"`
		Total int64     `json:"total"`
	}], error) {
		s, err := scope(ctx, authz.TrendsRead)
		if err != nil {
			return nil, err
		}
		q := `SELECT id, source_id, source_name, title, url, summary, author, topic_ids, published_at, saved_at
		      FROM news_items WHERE workspace_id = $1`
		args := []any{s.WorkspaceID}
		if in.Topic != "" {
			args = append(args, in.Topic)
			q += ` AND $` + strconv.Itoa(len(args)) + ` = ANY (topic_ids)`
		}
		if in.Source != "" {
			args = append(args, in.Source)
			q += ` AND source_id = $` + strconv.Itoa(len(args))
		}
		if in.Saved {
			q += ` AND saved_at IS NOT NULL`
		}
		if strings.TrimSpace(in.Q) != "" {
			args = append(args, "%"+strings.ToLower(strings.TrimSpace(in.Q))+"%")
			q += ` AND (lower(title) LIKE $` + strconv.Itoa(len(args)) + ` OR lower(summary) LIKE $` + strconv.Itoa(len(args)) + `)`
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 100
		}
		q += ` ORDER BY published_at DESC LIMIT ` + strconv.Itoa(limit)
		rows, err := db().QueryContext(ctx, q, args...)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list the timeline")
		}
		defer rows.Close()
		list := []NewsOut{}
		for rows.Next() {
			var n NewsOut
			var src sql.NullString
			var saved sql.NullTime
			var topics pgTextArray
			if err := rows.Scan(&n.ID, &src, &n.SourceName, &n.Title, &n.URL, &n.Summary, &n.Author, &topics, &n.PublishedAt, &saved); err != nil {
				return nil, huma.Error500InternalServerError("could not list the timeline")
			}
			n.SourceID, n.SavedAt = strp(src), timep(saved)
			n.TopicIDs = topics
			if n.TopicIDs == nil {
				n.TopicIDs = []string{}
			}
			list = append(list, n)
		}
		var total int64
		_ = db().QueryRowContext(ctx, `SELECT count(*) FROM news_items WHERE workspace_id = $1`, s.WorkspaceID).Scan(&total)
		return out(struct {
			Items []NewsOut `json:"items"`
			Total int64     `json:"total"`
		}{list, total}), nil
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

// backfillSQL labels already-collected items with one topic: any keyword (or the
// topic's own name, when it has none) appearing in the title or summary.
const backfillSQL = `
UPDATE news_items n SET topic_ids = array_append(n.topic_ids, t.id)
FROM topics t
WHERE t.workspace_id = $1 AND t.id = $2 AND n.workspace_id = t.workspace_id
  AND NOT (t.id = ANY (n.topic_ids))
  AND EXISTS (
    SELECT 1 FROM unnest(CASE WHEN cardinality(t.keywords) = 0 THEN ARRAY[t.name] ELSE t.keywords END) AS kw
    WHERE lower(n.title || ' ' || n.summary) LIKE '%' || lower(kw) || '%'
  )`

func getSource(ctx context.Context, db *sql.DB, wsID, id string) (SourceOut, error) {
	var so SourceOut
	var fetched, errAt sql.NullTime
	err := db.QueryRowContext(ctx, `
		SELECT id, name, url, kind, enabled, last_fetched_at, last_error, last_error_at, item_count, created_at
		FROM feed_sources WHERE workspace_id = $1 AND id = $2`, wsID, id,
	).Scan(&so.ID, &so.Name, &so.URL, &so.Kind, &so.Enabled, &fetched, &so.LastError, &errAt, &so.ItemCount, &so.CreatedAt)
	so.LastFetchedAt, so.LastErrorAt = timep(fetched), timep(errAt)
	return so, err
}

func getTopic(ctx context.Context, db *sql.DB, wsID, id string) (TopicOut, error) {
	var t TopicOut
	var kw pgTextArray
	err := db.QueryRowContext(ctx, `
		SELECT t.id, t.name, t.keywords, t.enabled, t.created_at,
		       (SELECT count(*) FROM news_items n WHERE n.workspace_id = t.workspace_id AND t.id = ANY (n.topic_ids))
		FROM topics t WHERE t.workspace_id = $1 AND t.id = $2`, wsID, id,
	).Scan(&t.ID, &t.Name, &kw, &t.Enabled, &t.CreatedAt, &t.ItemCount)
	t.Keywords = kw
	if t.Keywords == nil {
		t.Keywords = []string{}
	}
	return t, err
}

// feedTitle re-fetches just to read the feed's own title, for naming a source
// the person did not name. Best effort: a failure here is not an error.
func feedTitle(ctx context.Context, a *app.App, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Moharrik/1.0 (+https://moharrik.com)")
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

func cleanKeywords(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, k := range in {
		k = strings.TrimSpace(k)
		if k == "" || seen[strings.ToLower(k)] {
			continue
		}
		seen[strings.ToLower(k)] = true
		out = append(out, k)
	}
	return out
}

// jsonArray renders []string as a JSON array, for the jsonb_array_elements_text
// trick that keeps text[] parameters out of the driver's hands.
func jsonArray(vals []string) string {
	if len(vals) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		v = strings.ReplaceAll(v, `\`, `\\`)
		v = strings.ReplaceAll(v, `"`, `\"`)
		parts = append(parts, `"`+v+`"`)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
