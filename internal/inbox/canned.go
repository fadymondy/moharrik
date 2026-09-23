package inbox

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
)

/*
Canned replies (MH-383): the answers a team types again and again — "thanks,
we'll be back shortly", "here is how to book a call" — kept once per workspace,
in English and Arabic (internal/db/schema/canned_reply.sql).

Every member reads them (they insert them into replies); a manager or above
writes them, the same split as the chat widgets. Inserting is the UI's job and
does not send anything: most canned text wants a sentence of context added.
*/

type CannedReplyOut struct {
	ID        string    `json:"id"`
	Title     string    `json:"title" doc:"What the picker shows; never sent."`
	BodyEn    string    `json:"body_en"`
	BodyAr    string    `json:"body_ar"`
	Position  int64     `json:"position"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const cannedSelect = `SELECT id, title, body_en, body_ar, position, created_at, updated_at FROM canned_replies WHERE workspace_id = $1`

func scanCanned(r interface{ Scan(...any) error }) (CannedReplyOut, error) {
	var c CannedReplyOut
	err := r.Scan(&c.ID, &c.Title, &c.BodyEn, &c.BodyAr, &c.Position, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func readCanned(ctx context.Context, db *sql.DB, wsID, id string) (CannedReplyOut, error) {
	return scanCanned(db.QueryRowContext(ctx, cannedSelect+` AND id = $2`, wsID, id))
}

type cannedBody struct {
	Title    *string `json:"title,omitempty" maxLength:"80"`
	BodyEn   *string `json:"body_en,omitempty" maxLength:"4000" doc:"The English text. One of body_en and body_ar is required."`
	BodyAr   *string `json:"body_ar,omitempty" maxLength:"4000" doc:"The Arabic text."`
	Position *int64  `json:"position,omitempty" minimum:"0" maximum:"10000" doc:"Lower comes first in the picker."`
}

func registerCanned(api huma.API, a *app.App) {
	db := func() *sql.DB { return a.SQLDB }

	huma.Register(api, huma.Operation{
		OperationID: "w-canned-replies", Method: http.MethodGet, Path: "/api/w/canned_replies",
		Summary: "The workspace's canned replies, in picker order", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[struct {
		Replies []CannedReplyOut `json:"replies"`
	}], error) {
		s, err := scope(ctx, authz.InboxRead)
		if err != nil {
			return nil, err
		}
		rows, err := db().QueryContext(ctx, cannedSelect+` ORDER BY position, lower(title), created_at LIMIT 500`, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list canned replies")
		}
		defer rows.Close()
		list := []CannedReplyOut{}
		for rows.Next() {
			c, err := scanCanned(rows)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not list canned replies")
			}
			list = append(list, c)
		}
		return out(struct {
			Replies []CannedReplyOut `json:"replies"`
		}{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-canned-reply", Method: http.MethodPost, Path: "/api/w/canned_replies",
		DefaultStatus: http.StatusCreated, Summary: "Add a canned reply (English, Arabic or both)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body cannedBody
	}) (*body[struct {
		Reply CannedReplyOut `json:"reply"`
	}], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		title, en, ar := deref(in.Body.Title), deref(in.Body.BodyEn), deref(in.Body.BodyAr)
		if title == "" {
			return nil, huma.Error422UnprocessableEntity("a title is required")
		}
		if en == "" && ar == "" {
			return nil, huma.Error422UnprocessableEntity("write the reply in English, Arabic or both")
		}
		var pos int64
		if in.Body.Position != nil {
			pos = *in.Body.Position
		} else if err := db().QueryRowContext(ctx, `SELECT coalesce(max(position), -1) + 1 FROM canned_replies WHERE workspace_id = $1`, s.WorkspaceID).Scan(&pos); err != nil {
			return nil, huma.Error500InternalServerError("could not add the canned reply")
		}
		var id string
		if err := db().QueryRowContext(ctx, `
			INSERT INTO canned_replies (workspace_id, title, body_en, body_ar, position, created_by)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, s.WorkspaceID, title, en, ar, pos, s.UserID).Scan(&id); err != nil {
			return nil, huma.Error500InternalServerError("could not add the canned reply")
		}
		c, err := readCanned(ctx, db(), s.WorkspaceID, id)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the canned reply")
		}
		return out(struct {
			Reply CannedReplyOut `json:"reply"`
		}{c}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-canned-reply", Method: http.MethodPatch, Path: "/api/w/canned_replies/{id}",
		Summary: "Change a canned reply", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body cannedBody
	}) (*body[struct {
		Reply CannedReplyOut `json:"reply"`
	}], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		u := &update{}
		if in.Body.Title != nil {
			t := deref(in.Body.Title)
			if t == "" {
				return nil, huma.Error422UnprocessableEntity("a title is required")
			}
			u.set("title", t)
		}
		if in.Body.BodyEn != nil {
			u.set("body_en", deref(in.Body.BodyEn))
		}
		if in.Body.BodyAr != nil {
			u.set("body_ar", deref(in.Body.BodyAr))
		}
		if in.Body.Position != nil {
			u.set("position", *in.Body.Position)
		}
		if len(u.sets) > 0 {
			res, err := db().ExecContext(ctx, `UPDATE canned_replies SET `+strings.Join(u.sets, ", ")+`, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
				append([]any{s.WorkspaceID, in.ID}, u.args...)...)
			if pgCode(err) == "23514" {
				return nil, huma.Error422UnprocessableEntity("write the reply in English, Arabic or both")
			} else if err != nil {
				return nil, huma.Error500InternalServerError("could not change the canned reply")
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, huma.Error404NotFound("canned reply not found")
			}
		}
		c, err := readCanned(ctx, db(), s.WorkspaceID, in.ID)
		if notFound(err) {
			return nil, huma.Error404NotFound("canned reply not found")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not read the canned reply")
		}
		return out(struct {
			Reply CannedReplyOut `json:"reply"`
		}{c}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-canned-reply", Method: http.MethodDelete, Path: "/api/w/canned_replies/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Delete a canned reply", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		res, err := db().ExecContext(ctx, `DELETE FROM canned_replies WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not delete the canned reply")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("canned reply not found")
		}
		return &struct{}{}, nil
	})
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}
