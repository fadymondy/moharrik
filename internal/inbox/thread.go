package inbox

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/paging"
)

/*
A thread is read newest-first, a page at a time (MH-471).

It used to be one query with LIMIT 500 in ascending order: a thread past 500
messages showed its FIRST 500 and silently dropped everything newer, including
the message that had just arrived, with nothing on the page to say so. Now
opening a conversation answers its newest page with the thread's total, and
"Load earlier" walks back with a keyset cursor (created_at, id) until the first
message. Each page still reads oldest to newest, the way a thread is read.
*/

const (
	threadPageDefault = 100
	threadPageMax     = 500
)

// ThreadPage is one page of a thread, oldest first, and how to get the one before it.
type ThreadPage struct {
	Messages []MessageOut `json:"messages" doc:"Oldest first."`
	Total    int64        `json:"total" doc:"Messages in the whole thread."`
	// Empty when this page reaches the thread's first message.
	NextCursor string `json:"next_cursor" doc:"Fetches the page of EARLIER messages. Empty when this page holds the first message."`
}

// threadPage reads up to limit messages older than cursor (the newest when the
// cursor is empty), returned oldest first.
func threadPage(ctx context.Context, db *sql.DB, wsID, convID, cursor string, limit int) (*ThreadPage, error) {
	cur, err := paging.Decode(cursor, 2)
	if err != nil {
		return nil, err
	}
	args := []any{wsID, convID, limit + 1}
	where := ""
	if cur != nil {
		at, err := time.Parse(time.RFC3339Nano, cur[0])
		if err != nil {
			return nil, paging.ErrBadCursor
		}
		args = append(args, at, cur[1])
		where = ` AND (created_at, id) < ($4, $5)`
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, direction, author_user_id, body, body_html, mailbox_id, created_at
		FROM messages WHERE workspace_id = $1 AND conversation_id = $2`+where+`
		ORDER BY created_at DESC, id DESC LIMIT $3`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	newestFirst := []MessageOut{}
	for rows.Next() {
		var m MessageOut
		var author, mailboxID sql.NullString
		if err := rows.Scan(&m.ID, &m.Direction, &author, &m.Body, &m.BodyHTML, &mailboxID, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.AuthorUserID = strp(author)
		m.MailboxID = strp(mailboxID)
		newestFirst = append(newestFirst, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	page := &ThreadPage{Messages: make([]MessageOut, 0, min(len(newestFirst), limit))}
	if len(newestFirst) > limit {
		newestFirst = newestFirst[:limit]
		last := newestFirst[limit-1]
		page.NextCursor = paging.Encode(last.CreatedAt.UTC().Format(time.RFC3339Nano), last.ID)
	}
	for i := len(newestFirst) - 1; i >= 0; i-- {
		page.Messages = append(page.Messages, newestFirst[i])
	}
	if err := attachMessages(ctx, db, wsID, page.Messages); err != nil {
		return nil, err
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE workspace_id = $1 AND conversation_id = $2`,
		wsID, convID).Scan(&page.Total); err != nil {
		return nil, err
	}
	return page, nil
}

// attachMessages fills in each message's Attachments, one query for the
// whole page.
func attachMessages(ctx context.Context, db *sql.DB, wsID string, msgs []MessageOut) error {
	ids := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	byMsg, err := attachmentsFor(ctx, db, wsID, ids)
	if err != nil {
		return err
	}
	for i := range msgs {
		msgs[i].Attachments = byMsg[msgs[i].ID]
	}
	return nil
}

// ThreadMessages is one page of a conversation's thread, for "Load earlier".
// Unlike opening the conversation it does not mark it read: it is the same
// thread the member already has open.
func ThreadMessages(ctx context.Context, a *app.App, id, cursor string, limit int) (*ThreadPage, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	n, err := paging.Limit(limit, threadPageDefault, threadPageMax)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	if _, err := readConversation(ctx, a.SQLDB, s.WorkspaceID, id); notFound(err) {
		return nil, huma.Error404NotFound("conversation not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the conversation")
	}
	page, err := threadPage(ctx, a.SQLDB, s.WorkspaceID, id, cursor, n)
	if errors.Is(err, paging.ErrBadCursor) {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the thread")
	}
	return page, nil
}

func registerThread(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-conversation-messages", Method: http.MethodGet, Path: "/api/w/conversations/{id}/messages",
		Summary: "A page of a conversation's messages, walking back from the newest",
		Description: "Opening the conversation answers its newest page; this answers the ones before it. Follow next_cursor " +
			"until it is empty to reach the first message. Each page is oldest first. Does not mark the conversation read.",
		Tags: []string{"Inbox"},
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID     string `path:"id"`
		Cursor string `query:"cursor" doc:"next_cursor from the page after this one; empty for the newest page."`
		Limit  int    `query:"limit" minimum:"0" maximum:"500" doc:"Messages per page, default 100."`
	}) (*body[ThreadPage], error) {
		page, err := ThreadMessages(ctx, a, in.ID, in.Cursor, in.Limit)
		if err != nil {
			return nil, err
		}
		return out(*page), nil
	})
}
