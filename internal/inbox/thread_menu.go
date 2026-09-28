package inbox

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
)

/*
MH-662: the thread ⋮ menu's own surface — star/unstar, "delete for us"
(message_hides — never touches WhatsApp, only Moharrik's own copy), in-chat
search and exporting a chat.

Star and hide share message_stars/message_hides (internal/db/schema/
thread_menu.sql), one row per member per message, the same per-member shape
as conversation_prefs (MH-657): a star or a hide is that member's own view of
the thread, not the team's.

Search and export both read with threadPage's hide-filter but without paging
a human scrolls through, so they use the plain SQL below rather than
threadPage itself — a search match needs to rank/filter by body text, and an
export needs the WHOLE thread, neither of which threadPage's keyset shape is
for.
*/

// StarMessage stars a message for the signed-in member (MH-662). Exported for
// the star_message MCP tool (internal/mcp/user_tools_inbox.go).
func StarMessage(ctx context.Context, a *app.App, msgID string) error { return starMessage(ctx, a, msgID) }

// UnstarMessage unstars a message for the signed-in member (MH-662). Exported
// for the star_message MCP tool.
func UnstarMessage(ctx context.Context, a *app.App, msgID string) error { return unstarMessage(ctx, a, msgID) }

// ExportChat exports a conversation as a WhatsApp-format .txt transcript
// (MH-662). Exported for the export_conversation MCP tool.
func ExportChat(ctx context.Context, a *app.App, convID string) (string, []byte, error) {
	return exportChat(ctx, a, convID)
}

// starMessage records the signed-in member's star (idempotent).
func starMessage(ctx context.Context, a *app.App, msgID string) error {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return err
	}
	if _, err := messageWorkspace(ctx, a.SQLDB, s.WorkspaceID, msgID); err != nil {
		return err
	}
	_, err = a.SQLDB.ExecContext(ctx, `
		INSERT INTO message_stars (workspace_id, message_id, user_id) VALUES ($1, $2, $3)
		ON CONFLICT (workspace_id, message_id, user_id) DO NOTHING`, s.WorkspaceID, msgID, s.UserID)
	return err
}

// unstarMessage removes the signed-in member's star (idempotent).
func unstarMessage(ctx context.Context, a *app.App, msgID string) error {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return err
	}
	if _, err := messageWorkspace(ctx, a.SQLDB, s.WorkspaceID, msgID); err != nil {
		return err
	}
	_, err = a.SQLDB.ExecContext(ctx, `DELETE FROM message_stars WHERE workspace_id = $1 AND message_id = $2 AND user_id = $3`,
		s.WorkspaceID, msgID, s.UserID)
	return err
}

// hideMessage records the signed-in member's "delete for us" — the message
// stays on WhatsApp and for every other member; it only disappears from this
// member's own view of the thread (threadPage's hideWhere).
func hideMessage(ctx context.Context, a *app.App, msgID string) error {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return err
	}
	if _, err := messageWorkspace(ctx, a.SQLDB, s.WorkspaceID, msgID); err != nil {
		return err
	}
	_, err = a.SQLDB.ExecContext(ctx, `
		INSERT INTO message_hides (workspace_id, message_id, user_id) VALUES ($1, $2, $3)
		ON CONFLICT (workspace_id, message_id, user_id) DO NOTHING`, s.WorkspaceID, msgID, s.UserID)
	return err
}

// messageWorkspace confirms a message exists in this workspace, the same
// "does this id belong here" check every other write in this package does
// before touching it.
func messageWorkspace(ctx context.Context, db *sql.DB, wsID, msgID string) (string, error) {
	var convID string
	err := db.QueryRowContext(ctx, `SELECT conversation_id FROM messages WHERE workspace_id = $1 AND id = $2`, wsID, msgID).Scan(&convID)
	if notFound(err) {
		return "", huma.Error404NotFound("message not found")
	}
	if err != nil {
		return "", huma.Error500InternalServerError("could not read the message")
	}
	return convID, nil
}

// starredMessages lists every message the signed-in member starred in one
// conversation, newest first — the panel's "Starred messages" tab.
func starredMessages(ctx context.Context, a *app.App, convID string) ([]MessageOut, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	if _, err := readConversation(ctx, a.SQLDB, s.WorkspaceID, convID); notFound(err) {
		return nil, huma.Error404NotFound("conversation not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the conversation")
	}
	rows, err := a.SQLDB.QueryContext(ctx, `
		SELECT m.id, m.direction, m.author_user_id, m.body, m.body_html, m.mailbox_id, m.created_at,
		       m.sender_contact_id, m.sender_name, m.sender_external_id, m.subject, m.status
		FROM messages m JOIN message_stars st ON st.workspace_id = m.workspace_id AND st.message_id = m.id
		WHERE m.workspace_id = $1 AND m.conversation_id = $2 AND st.user_id = $3
		ORDER BY m.created_at DESC`, s.WorkspaceID, convID, s.UserID)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read starred messages")
	}
	defer rows.Close()
	msgs := []MessageOut{}
	for rows.Next() {
		var m MessageOut
		var author, mailboxID, senderContactID sql.NullString
		if err := rows.Scan(&m.ID, &m.Direction, &author, &m.Body, &m.BodyHTML, &mailboxID, &m.CreatedAt,
			&senderContactID, &m.SenderName, &m.SenderExternalID, &m.Subject, &m.Status); err != nil {
			return nil, huma.Error500InternalServerError("could not read starred messages")
		}
		m.AuthorUserID = strp(author)
		m.MailboxID = strp(mailboxID)
		m.SenderContactID = strp(senderContactID)
		m.Starred = true
		msgs = append(msgs, m)
	}
	if err := attachMessages(ctx, a.SQLDB, s.WorkspaceID, msgs); err != nil {
		return nil, huma.Error500InternalServerError("could not read starred messages")
	}
	return msgs, nil
}

// SearchResult is one page of a conversation's search-in-chat matches, oldest
// first like a ThreadPage, with the same "load older" cursor shape.
type SearchResult struct {
	Messages   []MessageOut `json:"messages" doc:"Oldest first, only messages whose body matched the query."`
	NextCursor string       `json:"next_cursor" doc:"Fetches the page of EARLIER matches. Empty when this page holds the first match."`
}

// searchThread finds messages in one conversation whose body contains query
// (case-insensitive), walking back from the newest match — "find in chat,
// jump between matches, load older pages as needed".
func searchThread(ctx context.Context, a *app.App, convID, query, cursor string, limit int) (*SearchResult, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	if _, err := readConversation(ctx, a.SQLDB, s.WorkspaceID, convID); notFound(err) {
		return nil, huma.Error404NotFound("conversation not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the conversation")
	}
	if strings.TrimSpace(query) == "" {
		return &SearchResult{Messages: []MessageOut{}}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	args := []any{s.WorkspaceID, convID, "%" + strings.ToLower(query) + "%", limit + 1}
	where := ""
	if cursor != "" {
		at, id, err := decodeSearchCursor(cursor)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("bad cursor")
		}
		args = append(args, at, id)
		where = ` AND (created_at, id) < ($5, $6)`
	}
	args = append(args, s.UserID)
	rows, err := a.SQLDB.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, direction, author_user_id, body, body_html, mailbox_id, created_at,
		       sender_contact_id, sender_name, sender_external_id, subject, status
		FROM messages
		WHERE workspace_id = $1 AND conversation_id = $2 AND lower(body) LIKE $3
		  AND NOT EXISTS (SELECT 1 FROM message_hides h WHERE h.workspace_id = messages.workspace_id AND h.message_id = messages.id AND h.user_id = $%d)%s
		ORDER BY created_at DESC, id DESC LIMIT $4`, len(args), where), args...)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not search this chat")
	}
	defer rows.Close()
	newestFirst := []MessageOut{}
	for rows.Next() {
		var m MessageOut
		var author, mailboxID, senderContactID sql.NullString
		if err := rows.Scan(&m.ID, &m.Direction, &author, &m.Body, &m.BodyHTML, &mailboxID, &m.CreatedAt,
			&senderContactID, &m.SenderName, &m.SenderExternalID, &m.Subject, &m.Status); err != nil {
			return nil, huma.Error500InternalServerError("could not search this chat")
		}
		m.AuthorUserID = strp(author)
		m.MailboxID = strp(mailboxID)
		m.SenderContactID = strp(senderContactID)
		newestFirst = append(newestFirst, m)
	}
	res := &SearchResult{Messages: make([]MessageOut, 0, min(len(newestFirst), limit))}
	if len(newestFirst) > limit {
		newestFirst = newestFirst[:limit]
		last := newestFirst[limit-1]
		res.NextCursor = encodeSearchCursor(last.CreatedAt, last.ID)
	}
	for i := len(newestFirst) - 1; i >= 0; i-- {
		res.Messages = append(res.Messages, newestFirst[i])
	}
	if err := attachMessages(ctx, a.SQLDB, s.WorkspaceID, res.Messages); err != nil {
		return nil, huma.Error500InternalServerError("could not search this chat")
	}
	if err := attachStars(ctx, a.SQLDB, s.WorkspaceID, s.UserID, res.Messages); err != nil {
		return nil, huma.Error500InternalServerError("could not search this chat")
	}
	return res, nil
}

func encodeSearchCursor(at time.Time, id string) string {
	return fmt.Sprintf("%s|%s", at.UTC().Format(time.RFC3339Nano), id)
}

func decodeSearchCursor(cursor string) (time.Time, string, error) {
	parts := strings.SplitN(cursor, "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("bad cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", err
	}
	return at, parts[1], nil
}

// exportChat renders a whole conversation as WhatsApp's own export .txt
// format: "M/D/YY, H:MM AM/PM - Sender: body", one line per message, media
// messages noted the way WhatsApp notes them.
func exportChat(ctx context.Context, a *app.App, convID string) (string, []byte, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return "", nil, err
	}
	c, err := readConversation(ctx, a.SQLDB, s.WorkspaceID, convID)
	if notFound(err) {
		return "", nil, huma.Error404NotFound("conversation not found")
	} else if err != nil {
		return "", nil, huma.Error500InternalServerError("could not read the conversation")
	}
	msgs, err := thread(ctx, a.SQLDB, s.WorkspaceID, convID)
	if err != nil {
		return "", nil, huma.Error500InternalServerError("could not read the thread")
	}
	var buf bytes.Buffer
	for _, m := range msgs {
		who := c.CustomerName
		if m.Direction == "out" {
			who = "You"
		}
		if m.SenderName != "" {
			who = m.SenderName
		}
		body := m.Body
		if body == "" && len(m.Attachments) > 0 {
			body = "<Media omitted>"
		}
		fmt.Fprintf(&buf, "%s - %s: %s\n", m.CreatedAt.Local().Format("1/2/06, 3:04 PM"), who, strings.ReplaceAll(body, "\n", " "))
	}
	name := strings.TrimSpace(c.CustomerName)
	if name == "" {
		name = convID
	}
	filename := fmt.Sprintf("WhatsApp Chat with %s.txt", sanitizeFilename(name))
	return filename, buf.Bytes(), nil
}

// exportAttachments zips every attachment in the conversation — only called
// when there is at least one (ExportAttachmentsAvailable checks first).
func exportAttachments(ctx context.Context, a *app.App, convID string) (string, []byte, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return "", nil, err
	}
	c, err := readConversation(ctx, a.SQLDB, s.WorkspaceID, convID)
	if notFound(err) {
		return "", nil, huma.Error404NotFound("conversation not found")
	} else if err != nil {
		return "", nil, huma.Error500InternalServerError("could not read the conversation")
	}
	rows, err := a.SQLDB.QueryContext(ctx, `
		SELECT ma.filename, ma.data FROM message_attachments ma
		JOIN messages m ON m.workspace_id = ma.workspace_id AND m.id = ma.message_id
		WHERE ma.workspace_id = $1 AND m.conversation_id = $2 AND ma.status = 'ready'
		ORDER BY m.created_at`, s.WorkspaceID, convID)
	if err != nil {
		return "", nil, huma.Error500InternalServerError("could not read attachments")
	}
	defer rows.Close()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	seen := map[string]int{}
	n := 0
	for rows.Next() {
		var filename string
		var data []byte
		if err := rows.Scan(&filename, &data); err != nil {
			return "", nil, huma.Error500InternalServerError("could not read attachments")
		}
		if filename == "" {
			filename = "attachment"
		}
		if c := seen[filename]; c > 0 {
			ext := ""
			if i := strings.LastIndex(filename, "."); i > 0 {
				ext = filename[i:]
				filename = filename[:i]
			}
			filename = fmt.Sprintf("%s (%d)%s", filename, c, ext)
		}
		seen[filename]++
		w, err := zw.Create(filename)
		if err != nil {
			return "", nil, huma.Error500InternalServerError("could not build the zip")
		}
		if _, err := w.Write(data); err != nil {
			return "", nil, huma.Error500InternalServerError("could not build the zip")
		}
		n++
	}
	if err := zw.Close(); err != nil {
		return "", nil, huma.Error500InternalServerError("could not build the zip")
	}
	if n == 0 {
		return "", nil, huma.Error404NotFound("this chat has no attachments")
	}
	name := strings.TrimSpace(c.CustomerName)
	if name == "" {
		name = convID
	}
	return fmt.Sprintf("WhatsApp Chat with %s - attachments.zip", sanitizeFilename(name)), buf.Bytes(), nil
}

func sanitizeFilename(s string) string {
	repl := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "\"", "", "<", "", ">", "", "|", "-", "?", "", "*", "")
	return repl.Replace(s)
}

func registerThreadMenu(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-star-message", Method: http.MethodPut, Path: "/api/w/messages/{id}/star",
		DefaultStatus: http.StatusNoContent, Summary: "Star a message, for the signed-in member only (MH-662)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		return nil, starMessage(ctx, a, in.ID)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-unstar-message", Method: http.MethodDelete, Path: "/api/w/messages/{id}/star",
		DefaultStatus: http.StatusNoContent, Summary: "Unstar a message, for the signed-in member only (MH-662)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		return nil, unstarMessage(ctx, a, in.ID)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-hide-message", Method: http.MethodPost, Path: "/api/w/messages/{id}/hide",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Delete a message for the signed-in member only (MH-662)",
		Description:   "Never sent to WhatsApp and never removes it for a teammate: this hides it from the signed-in member's own view of the thread.",
		Tags:          tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		return nil, hideMessage(ctx, a, in.ID)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-starred-messages", Method: http.MethodGet, Path: "/api/w/conversations/{id}/starred",
		Summary: "Messages the signed-in member starred in this chat (MH-662)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[struct {
		Messages []MessageOut `json:"messages"`
	}], error) {
		msgs, err := starredMessages(ctx, a, in.ID)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Messages []MessageOut `json:"messages"`
		}{msgs}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-search-conversation", Method: http.MethodGet, Path: "/api/w/conversations/{id}/search",
		Summary: "Find messages in this chat's thread (MH-662)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID     string `path:"id"`
		Q      string `query:"q" doc:"Text to find, case-insensitive."`
		Cursor string `query:"cursor" doc:"next_cursor from the page after this one; empty for the newest matches."`
		Limit  int    `query:"limit" minimum:"0" maximum:"100"`
	}) (*body[SearchResult], error) {
		res, err := searchThread(ctx, a, in.ID, in.Q, in.Cursor, in.Limit)
		if err != nil {
			return nil, err
		}
		return out(*res), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-export-conversation-txt", Method: http.MethodGet, Path: "/api/w/conversations/{id}/export.txt",
		Summary: "Export a chat as a WhatsApp-format .txt transcript (MH-662)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*huma.StreamResponse, error) {
		filename, data, err := exportChat(ctx, a, in.ID)
		if err != nil {
			return nil, err
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			hctx.SetHeader("Content-Type", "text/plain; charset=utf-8")
			hctx.SetHeader("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(filename, `"`, "")+`"`)
			hctx.BodyWriter().Write(data) //nolint:errcheck
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-export-conversation-zip", Method: http.MethodGet, Path: "/api/w/conversations/{id}/export.zip",
		Summary: "Export a chat's attachments as a .zip (MH-662)",
		Description: "404 when the chat has no attachments — the client only offers this download when export.txt's " +
			"conversation shows at least one.",
		Tags: tags,
	}, func(ctx context.Context, in *idPath) (*huma.StreamResponse, error) {
		filename, data, err := exportAttachments(ctx, a, in.ID)
		if err != nil {
			return nil, err
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			hctx.SetHeader("Content-Type", "application/zip")
			hctx.SetHeader("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(filename, `"`, "")+`"`)
			hctx.BodyWriter().Write(data) //nolint:errcheck
		}}, nil
	})
}
