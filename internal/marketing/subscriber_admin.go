package marketing

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
)

/*
Subscriber admin (MH-408): the three things a workspace could not do to its
own audience.

  - Take ONE address off ONE list. The only option used to be deleting the
    whole contact, which also destroys their deals and their timeline — the
    wrong tool for "they asked us by phone to stop sending the newsletter".
  - Export a list as CSV, filtered the way the screen is.
  - Rename a list and change its double opt-in after creating it.
*/

// ErrNotOnList is an unsubscribe for a contact that is not on that list.
var ErrNotOnList = errors.New("that contact is not on that list")

// UnsubscribeOne takes one contact off one list, on a member's say-so.
//
// It writes exactly what the link in an email writes — status unsubscribed,
// unsubscribed_at kept if already set — so everything downstream treats the
// two the same: campaigns skip the address, and adding it back goes through
// Subscribe, which puts it back to pending with a fresh token on a double
// opt-in list (MH-318). The only difference is unsubscribed_by, the member who
// did it, and a note on the contact's timeline saying so.
//
// The contact, its other subscriptions, its deals and its timeline are not
// touched. Already unsubscribed is not an error and changes nothing: the first
// record of who did it stands. changed reports whether anything happened.
func UnsubscribeOne(ctx context.Context, db *sql.DB, wsID, contactID, listID, userID, userEmail string) (changed bool, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck

	var subID, status, listName string
	err = tx.QueryRowContext(ctx, `
		SELECT s.id, s.status, l.name FROM list_subscriptions s
		JOIN mail_lists l ON l.id = s.list_id AND l.workspace_id = s.workspace_id
		WHERE s.workspace_id = $1 AND s.contact_id = $2 AND s.list_id = $3
		FOR UPDATE OF s`, wsID, contactID, listID).Scan(&subID, &status, &listName)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotOnList
	}
	if err != nil {
		return false, err
	}
	if status == StatusUnsubscribed {
		return false, tx.Commit()
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE list_subscriptions SET status = 'unsubscribed', unsubscribed_at = coalesce(unsubscribed_at, now()),
		  unsubscribed_by = $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, wsID, subID, userID); err != nil {
		return false, err
	}

	// Who did it, where the team looks: the contact's timeline, in the
	// workspace's language.
	var locale string
	_ = tx.QueryRowContext(ctx, `SELECT locale FROM workspaces WHERE id = $1`, wsID).Scan(&locale)
	who := strings.TrimSpace(userEmail)
	if who == "" {
		who = userID
	}
	note := `Unsubscribed from the list "` + listName + `" by ` + who + `. Their other lists, deals and history are unchanged.`
	if locale == "ar" {
		note = `أُلغي اشتراكه في القائمة "` + listName + `" بواسطة ` + who + `. قوائمه الأخرى وصفقاته وسجله كما هي.`
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO activities (workspace_id, kind, body, contact_id, created_by) VALUES ($1, 'note', $2, $3, $4)`,
		wsID, note, contactID, userID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// getList reads one list with its counts.
func getList(ctx context.Context, db *sql.DB, wsID, id string) (*ListOut, error) {
	var l ListOut
	err := db.QueryRowContext(ctx, `
		SELECT l.id, l.name, l.description, l.double_opt_in, l.created_at,
		       count(s.id) FILTER (WHERE s.status = 'subscribed'),
		       count(s.id) FILTER (WHERE s.status = 'pending')
		FROM mail_lists l LEFT JOIN list_subscriptions s ON s.list_id = l.id AND s.workspace_id = l.workspace_id
		WHERE l.workspace_id = $1 AND l.id = $2
		GROUP BY l.id`, wsID, id,
	).Scan(&l.ID, &l.Name, &l.Description, &l.DoubleOptIn, &l.CreatedAt, &l.Subscribed, &l.Pending)
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// ExportColumns is the CSV header, in order.
var ExportColumns = []string{"name", "email", "phone", "company", "status", "subscribed_at", "unsubscribed_at", "source"}

// utf8BOM makes Excel read the file as UTF-8. Without it Excel on Windows
// guesses the ANSI code page and every Arabic name comes out as mojibake.
const utf8BOM = "\xEF\xBB\xBF"

// phoneLike is a value that is only a phone number. It starts with "+", which
// the formula guard below would otherwise prefix — and it is harmless: a
// spreadsheet reads it as a number, never as a function call.
var phoneLike = regexp.MustCompile(`^\+[0-9 ()\-.]*$`)

// cell neutralises spreadsheet formula injection. Names and companies arrive
// from public forms, so "=HYPERLINK(...)" as somebody's name is a real input;
// a leading apostrophe makes the spreadsheet show it as text instead of
// running it (OWASP "CSV injection").
func cell(s string) string {
	if s == "" || phoneLike.MatchString(s) {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

func stampCSV(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// WriteListCSV streams one list's contacts, filtered like the screen, as CSV.
//
// It walks the list with the same page query the screen uses (contactRows),
// so the file holds exactly the rows the filtered view does — no second query
// to drift from the first, and no cap: it follows the cursor to the end, a
// page at a time, flushing as it goes so memory stays one page deep however
// long the list is.
func WriteListCSV(ctx context.Context, db *sql.DB, wsID string, f ContactFilter, w io.Writer) (int, error) {
	if _, err := w.Write([]byte(utf8BOM)); err != nil {
		return 0, err
	}
	cw := csv.NewWriter(w)
	cw.UseCRLF = true // what Excel writes, and what RFC 4180 specifies
	if err := cw.Write(ExportColumns); err != nil {
		return 0, err
	}
	n := 0
	var cur *cursor
	for {
		page, err := contactRows(ctx, db, wsID, f, cur, MaxContactPage)
		if err != nil {
			return n, err
		}
		for _, c := range page.Contacts {
			var sub *SubscriptionOut
			for i := range c.Subscriptions {
				if c.Subscriptions[i].ListID == f.ListID {
					sub = &c.Subscriptions[i]
				}
			}
			if sub == nil {
				continue // left the list between the page query and this read
			}
			if err := cw.Write([]string{
				cell(c.Name), cell(c.Email), cell(c.Phone), cell(c.CompanyName), sub.Status,
				stampCSV(sub.ConfirmedAt), stampCSV(sub.UnsubscribedAt), cell(c.Source),
			}); err != nil {
				return n, err
			}
			n++
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return n, err
		}
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		if page.NextCursor == "" {
			return n, nil
		}
		if cur, err = decodeCursor(page.NextCursor); err != nil {
			return n, err
		}
	}
}

// exportDisposition names the file "<list>[-<status>]-<date>.csv". The list name can be
// Arabic, so it goes in filename* (RFC 6266/5987) with an ASCII fallback.
func exportDisposition(listName, status string, now time.Time) string {
	base := listName
	if status != "" {
		base += "-" + status
	}
	base += "-" + now.Format("2006-01-02") + ".csv"
	ascii := "contacts"
	if status != "" {
		ascii += "-" + status
	}
	ascii += "-" + now.Format("2006-01-02") + ".csv"
	return `attachment; filename="` + ascii + `"; filename*=UTF-8''` + url.PathEscape(base)
}

func registerSubscriberAdmin(api huma.API, a *app.App) {

	huma.Register(api, huma.Operation{
		OperationID: "w-unsubscribe-contact-from-list", Method: http.MethodPost,
		Path:    "/api/w/contacts/{id}/lists/{listId}/unsubscribe",
		Summary: "Unsubscribe a contact from ONE list (the contact, its other lists and its deals stay)",
		Description: "Writes what the unsubscribe link in an email writes, plus who did it. Adding the address back later " +
			"asks for consent again on a double opt-in list. Already unsubscribed is not an error.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID     string `path:"id"`
		ListID string `path:"listId"`
	}) (*body[struct {
		Changed bool       `json:"changed" doc:"False when the contact was already unsubscribed from that list"`
		Contact ContactOut `json:"contact"`
	}], error) {
		s, err := scope(ctx, authz.ContactsWrite)
		if err != nil {
			return nil, err
		}
		changed, err := UnsubscribeOne(ctx, a.SQLDB, s.WorkspaceID, in.ID, in.ListID, s.UserID, s.Email)
		if errors.Is(err, ErrNotOnList) {
			return nil, huma.Error404NotFound(err.Error())
		}
		if err != nil {
			slog.ErrorContext(ctx, "admin unsubscribe", "err", err)
			return nil, huma.Error500InternalServerError("could not unsubscribe the contact")
		}
		if changed {
			PublishContacts(a, s.WorkspaceID)
		}
		c, err := getContactOut(ctx, a.SQLDB, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the contact")
		}
		return out(struct {
			Changed bool       `json:"changed" doc:"False when the contact was already unsubscribed from that list"`
			Contact ContactOut `json:"contact"`
		}{changed, *c}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-mail-list", Method: http.MethodPatch, Path: "/api/w/mail/lists/{id}",
		Summary: "Rename a mailing list, change its description or its double opt-in",
		Description: "Double opt-in applies to addresses added from now on. Turning it off does not confirm anyone " +
			"already pending, and turning it on does not ask existing subscribers again.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Name        *string `json:"name,omitempty" minLength:"1" maxLength:"80"`
			Description *string `json:"description,omitempty" maxLength:"500"`
			DoubleOptIn *bool   `json:"double_opt_in,omitempty"`
		}
	}) (*body[struct {
		List ListOut `json:"list"`
	}], error) {
		// The same right as creating a list: anyone who can create one with
		// double opt-in off can already have a list without it.
		s, err := scope(ctx, authz.ContactsWrite)
		if err != nil {
			return nil, err
		}
		sets, args := []string{}, []any{s.WorkspaceID, in.ID}
		if in.Body.Name != nil {
			name := strings.TrimSpace(*in.Body.Name)
			if name == "" {
				return nil, huma.Error422UnprocessableEntity("a name is required")
			}
			args = append(args, name)
			sets = append(sets, "name = $3")
		}
		if in.Body.Description != nil {
			args = append(args, strings.TrimSpace(*in.Body.Description))
			sets = append(sets, "description = $"+strconv.Itoa(len(args)))
		}
		if in.Body.DoubleOptIn != nil {
			args = append(args, *in.Body.DoubleOptIn)
			sets = append(sets, "double_opt_in = $"+strconv.Itoa(len(args)))
		}
		if len(sets) > 0 {
			res, err := a.SQLDB.ExecContext(ctx, `UPDATE mail_lists SET `+strings.Join(sets, ", ")+`, updated_at = now()
				WHERE workspace_id = $1 AND id = $2`, args...)
			if isUnique(err) {
				return nil, huma.Error409Conflict("a list with that name already exists")
			}
			if err != nil {
				slog.ErrorContext(ctx, "update list", "err", err)
				return nil, huma.Error500InternalServerError("could not update the list")
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, huma.Error404NotFound("list not found")
			}
		}
		l, err := getList(ctx, a.SQLDB, s.WorkspaceID, in.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("list not found")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the list")
		}
		return out(struct {
			List ListOut `json:"list"`
		}{*l}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-export-mail-list", Method: http.MethodGet, Path: "/api/w/mail/lists/{id}/export.csv",
		Summary: "Download a list's contacts as CSV (UTF-8 with BOM), filtered like the contacts page",
		Description: "Columns: " + strings.Join(ExportColumns, ", ") + ". Needs contacts:export (manager and up): " +
			"a whole audience in a file is a bulk disclosure of personal data, not just reading it.",
		Tags: tags,
		Responses: map[string]*huma.Response{
			"200": {Description: "CSV", Content: map[string]*huma.MediaType{"text/csv": {Schema: &huma.Schema{Type: "string"}}}},
		},
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID     string `path:"id"`
		Status string `query:"status" doc:"subscribed, pending, unsubscribed or bounced; empty is every status"`
		Q      string `query:"q" maxLength:"200" doc:"The same search as the contacts page"`
	}) (*huma.StreamResponse, error) {
		s, err := scope(ctx, authz.ContactsExport)
		if err != nil {
			return nil, err
		}
		f := ContactFilter{ListID: in.ID, Status: in.Status, Q: in.Q}
		if err := f.Validate(); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		// Everything that can fail with a status code fails HERE, before the
		// 200 and the first byte go out.
		l, err := getList(ctx, a.SQLDB, s.WorkspaceID, f.ListID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("list not found")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the list")
		}
		wsID := s.WorkspaceID
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			hctx.SetHeader("Content-Type", "text/csv; charset=utf-8")
			hctx.SetHeader("Content-Disposition", exportDisposition(l.Name, f.Status, time.Now()))
			hctx.SetHeader("Cache-Control", "no-store")
			hctx.SetHeader("X-Content-Type-Options", "nosniff")
			// ctx, not hctx.Context(): ctx carries the workspace scope the RLS
			// pool needs, and it is the same request's context.
			n, err := WriteListCSV(ctx, a.SQLDB, wsID, f, hctx.BodyWriter())
			if err != nil {
				a.Log.Warn("list export stopped", "workspace", wsID, "list", f.ListID, "rows", n, "err", err)
			}
		}}, nil
	})
}
