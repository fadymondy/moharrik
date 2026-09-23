package trends

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pgtext"
)

/*
Keyword topics: the workspace's watch lists. Until MH-385 they were the whole
feature — the timeline filtered by keyword. Now they are one input to a
trending topic's rank (relevance, rank.go), so every change here rescores the
recent stories.
*/

// rescore recomputes the recent stories after the keyword topics changed.
func rescore(ctx context.Context, a *app.App, wsID string) {
	if err := RescoreAll(ctx, a, wsID); err != nil {
		a.Log.Warn("trends: rescoring after a topic change failed", "workspace", wsID, "err", err)
		return
	}
	publish(a, wsID)
}

func registerTopics(api huma.API, a *app.App) {
	db := func() *sql.DB { return a.SQLDB }

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
			var kw pgtext.Array
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
		rescore(ctx, a, s.WorkspaceID)
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
		// Keywords, the name (relevance chips show it) or on/off: rescore.
		if len(sets) > 0 {
			rescore(ctx, a, s.WorkspaceID)
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
		rescore(ctx, a, s.WorkspaceID)
		return &struct{}{}, nil
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

func getTopic(ctx context.Context, db *sql.DB, wsID, id string) (TopicOut, error) {
	var t TopicOut
	var kw pgtext.Array
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
