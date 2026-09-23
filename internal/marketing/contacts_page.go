package marketing

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

/*
The contacts list, every row of it (MH-408).

It used to be one query ending in LIMIT 1000. A workspace with 1,200 contacts
saw 1,000 and had no way to know the other 200 existed: no total, no "more",
no search that could reach them. The same query also fetched EVERY
subscription in the workspace to decorate those 1,000 rows.

Now it is a page:

  - KEYSET paging on (created_at, id), newest first. The cursor is the last row
    of the previous page, so a contact added while somebody is paging does not
    shift everything by one and show a row twice. The id tie-break is not
    decoration: an import inserts a batch in one transaction, every row of it
    gets the same created_at, and paging on the timestamp alone skips them.
  - An accurate TOTAL for the current filter, counted by the same WHERE clause
    that selects the rows — one builder, so the two cannot disagree.
  - Per-status COUNTS when the view is one list, so the status filter can say
    how many each choice holds before it is clicked.
  - Subscriptions fetched for the page's contacts only.

The page size is explicit and reported back, and next_cursor says there is
more. Nothing is cut off without the caller being told.
*/

// Page sizes. The default is what the workspace page asks for; the maximum
// bounds one request, not the list — the cursor reaches the rest.
const (
	DefaultContactPage = 50
	MaxContactPage     = 200
)

// ContactFilter is what the list is narrowed by. Status only means something
// with a list: consent is per list, so "unsubscribed" alone does not say from
// what.
type ContactFilter struct {
	ListID string
	Status string
	Q      string
}

// StatusCounts is one list's subscriptions by status, under the current search.
type StatusCounts struct {
	All          int64 `json:"all"`
	Subscribed   int64 `json:"subscribed"`
	Pending      int64 `json:"pending"`
	Unsubscribed int64 `json:"unsubscribed"`
	Bounced      int64 `json:"bounced"`
}

// ContactPage is one page of the filtered list.
type ContactPage struct {
	Contacts []ContactOut `json:"contacts"`
	// Total is how many contacts match the filter — all pages, not this one.
	Total int64 `json:"total"`
	// Limit is the page size that was applied.
	Limit int `json:"limit"`
	// NextCursor fetches the page after this one. Empty on the last page.
	NextCursor string `json:"next_cursor"`
	// Counts is set when the view is one list.
	Counts *StatusCounts `json:"counts"`
}

// ErrBadCursor is a cursor this server did not issue (or one mangled in transit).
var ErrBadCursor = errors.New("that page cursor is not valid")

// ErrStatusNeedsList is a status filter with no list to apply it to.
var ErrStatusNeedsList = errors.New("a status filter needs a list: consent is per list")

// ErrBadStatus is a status that is not one of the four.
var ErrBadStatus = errors.New("status must be subscribed, pending, unsubscribed or bounced")

func validStatus(s string) bool {
	switch s {
	case StatusSubscribed, StatusPending, StatusUnsubscribed, StatusBounced:
		return true
	}
	return false
}

// Validate normalises the filter and rejects what cannot be answered.
func (f *ContactFilter) Validate() error {
	f.ListID = strings.TrimSpace(f.ListID)
	f.Status = strings.ToLower(strings.TrimSpace(f.Status))
	f.Q = strings.TrimSpace(f.Q)
	if len([]rune(f.Q)) > 200 {
		f.Q = string([]rune(f.Q)[:200])
	}
	if f.Status == "" {
		return nil
	}
	if !validStatus(f.Status) {
		return ErrBadStatus
	}
	if f.ListID == "" {
		return ErrStatusNeedsList
	}
	return nil
}

// likeEscape makes user text literal inside an ILIKE pattern: without it a
// search for "50%" or "a_b" matches things that do not contain it.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// where builds the one WHERE clause the rows, the total and the export all use.
// $1 is always the workspace. The tables are aliased c (contacts) and co
// (companies, LEFT JOINed).
func (f ContactFilter) where(wsID string) (string, []any) {
	args := []any{wsID}
	w := "c.workspace_id = $1"
	if f.ListID != "" {
		args = append(args, f.ListID)
		cond := "s.list_id = $" + strconv.Itoa(len(args))
		if f.Status != "" {
			args = append(args, f.Status)
			cond += " AND s.status = $" + strconv.Itoa(len(args))
		}
		w += " AND EXISTS (SELECT 1 FROM list_subscriptions s WHERE s.workspace_id = $1 AND s.contact_id = c.id AND " + cond + ")"
	}
	if f.Q != "" {
		args = append(args, "%"+likeEscape(f.Q)+"%")
		n := "$" + strconv.Itoa(len(args))
		w += " AND (c.email ILIKE " + n + " OR c.name ILIKE " + n + " OR c.phone ILIKE " + n + " OR co.name ILIKE " + n + ")"
	}
	return w, args
}

const contactFrom = ` FROM contacts c LEFT JOIN companies co ON co.workspace_id = c.workspace_id AND co.id = c.company_id `

// cursor is the last row a page returned.
type cursor struct {
	at time.Time
	id string
}

// EncodeCursor is opaque to callers; it is the row's timestamp in microseconds
// (Postgres' precision, so it round-trips exactly) and its id.
func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UnixMicro(), 10) + "|" + id))
}

func decodeCursor(s string) (*cursor, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, ErrBadCursor
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok || id == "" || len(id) > 64 {
		return nil, ErrBadCursor
	}
	us, err := strconv.ParseInt(at, 10, 64)
	if err != nil {
		return nil, ErrBadCursor
	}
	return &cursor{at: time.UnixMicro(us).UTC(), id: id}, nil
}

func clampLimit(n int) int {
	if n <= 0 {
		return DefaultContactPage
	}
	if n > MaxContactPage {
		return MaxContactPage
	}
	return n
}

// ListContactsPage returns one page of the workspace's contacts. db must be
// scoped to the workspace (the app pool with a tenant context).
func ListContactsPage(ctx context.Context, db *sql.DB, wsID string, f ContactFilter, after string, limit int) (*ContactPage, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	cur, err := decodeCursor(after)
	if err != nil {
		return nil, err
	}
	page, err := contactRows(ctx, db, wsID, f, cur, clampLimit(limit))
	if err != nil {
		return nil, err
	}

	where, args := f.where(wsID)
	if err := db.QueryRowContext(ctx, `SELECT count(*)`+contactFrom+`WHERE `+where, args...).Scan(&page.Total); err != nil {
		return nil, err
	}
	if f.ListID != "" {
		if page.Counts, err = listCounts(ctx, db, wsID, f); err != nil {
			return nil, err
		}
	}
	return page, nil
}

// contactRows is the page itself, without the totals — the export walks the
// list with it, so the file holds exactly the rows the screen does.
func contactRows(ctx context.Context, db *sql.DB, wsID string, f ContactFilter, cur *cursor, limit int) (*ContactPage, error) {
	where, args := f.where(wsID)
	if cur != nil {
		args = append(args, cur.at, cur.id)
		n := len(args)
		where += fmt.Sprintf(" AND (c.created_at, c.id) < ($%d, $%d)", n-1, n)
	}
	// One more than the page, to know whether there is a next one without a
	// second query.
	args = append(args, limit+1)
	q := `SELECT c.id, c.email, c.name, c.locale, c.source, c.phone, c.title, c.company_id, coalesce(co.name, ''), c.created_at,
	             c.whatsapp_opt_in_at, c.whatsapp_opt_out_at` + contactFrom + `WHERE ` + where +
		` ORDER BY c.created_at DESC, c.id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	page := &ContactPage{Contacts: []ContactOut{}, Limit: limit}
	for rows.Next() {
		var c ContactOut
		if err := rows.Scan(&c.ID, &c.Email, &c.Name, &c.Locale, &c.Source, &c.Phone, &c.Title, &c.CompanyID, &c.CompanyName, &c.CreatedAt,
			&c.WhatsAppOptInAt, &c.WhatsAppOptOutAt); err != nil {
			return nil, err
		}
		c.Subscriptions = []SubscriptionOut{}
		page.Contacts = append(page.Contacts, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(page.Contacts) > limit {
		page.Contacts = page.Contacts[:limit]
		last := page.Contacts[limit-1]
		page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	if err := attachSubscriptions(ctx, db, wsID, page.Contacts); err != nil {
		return nil, err
	}
	return page, nil
}

// attachSubscriptions loads the list memberships of these contacts only.
func attachSubscriptions(ctx context.Context, db *sql.DB, wsID string, list []ContactOut) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]string, len(list))
	idx := make(map[string]int, len(list))
	for i, c := range list {
		ids[i] = c.ID
		idx[c.ID] = i
	}
	rows, err := db.QueryContext(ctx, `
		SELECT s.id, s.contact_id, s.list_id, l.name, s.status, s.confirmed_at, s.unsubscribed_at, s.unsubscribed_by IS NOT NULL
		FROM list_subscriptions s JOIN mail_lists l ON l.id = s.list_id AND l.workspace_id = s.workspace_id
		WHERE s.workspace_id = $1 AND s.contact_id = ANY($2) ORDER BY lower(l.name)`, wsID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var s SubscriptionOut
		var contactID string
		if err := rows.Scan(&s.ID, &contactID, &s.ListID, &s.ListName, &s.Status, &s.ConfirmedAt, &s.UnsubscribedAt, &s.ByWorkspace); err != nil {
			return err
		}
		if i, ok := idx[contactID]; ok {
			list[i].Subscriptions = append(list[i].Subscriptions, s)
		}
	}
	return rows.Err()
}

// listCounts is the list's subscriptions by status under the same search, so
// "Pending (12)" is exactly what choosing Pending then shows.
func listCounts(ctx context.Context, db *sql.DB, wsID string, f ContactFilter) (*StatusCounts, error) {
	// Search only: the list is the JOIN below, and the status is what is
	// being counted.
	g := ContactFilter{Q: f.Q}
	where, args := g.where(wsID)
	args = append(args, f.ListID)
	rows, err := db.QueryContext(ctx, `SELECT s.status, count(*)`+contactFrom+
		`JOIN list_subscriptions s ON s.workspace_id = c.workspace_id AND s.contact_id = c.id AND s.list_id = $`+strconv.Itoa(len(args))+
		` WHERE `+where+` GROUP BY s.status`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &StatusCounts{}
	for rows.Next() {
		var st string
		var n int64
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out.All += n
		switch st {
		case StatusSubscribed:
			out.Subscribed = n
		case StatusPending:
			out.Pending = n
		case StatusUnsubscribed:
			out.Unsubscribed = n
		case StatusBounced:
			out.Bounced = n
		}
	}
	return out, rows.Err()
}

// getContactOut reads one contact the way the list shows it. The add-contact
// route used to find the contact it had just written by scanning the capped
// list — which missed any existing contact older than the newest thousand.
func getContactOut(ctx context.Context, db *sql.DB, wsID, id string) (*ContactOut, error) {
	var c ContactOut
	err := db.QueryRowContext(ctx, `
		SELECT c.id, c.email, c.name, c.locale, c.source, c.phone, c.title, c.company_id, coalesce(co.name, ''), c.created_at,
		       c.whatsapp_opt_in_at, c.whatsapp_opt_out_at`+contactFrom+`WHERE c.workspace_id = $1 AND c.id = $2`, wsID, id,
	).Scan(&c.ID, &c.Email, &c.Name, &c.Locale, &c.Source, &c.Phone, &c.Title, &c.CompanyID, &c.CompanyName, &c.CreatedAt,
		&c.WhatsAppOptInAt, &c.WhatsAppOptOutAt)
	if err != nil {
		return nil, err
	}
	c.Subscriptions = []SubscriptionOut{}
	list := []ContactOut{c}
	if err := attachSubscriptions(ctx, db, wsID, list); err != nil {
		return nil, err
	}
	return &list[0], nil
}
