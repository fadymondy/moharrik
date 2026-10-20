package inbox

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

/*
MH-664: emoji reactions on messages, and reply-to-message quotes.

Reactions live in message_reactions, one row per reactor per message (see
internal/db/schema/message_reactions.sql for who a reactor is). Reacting with
the emoji you already have takes it back; a different one replaces it.

On WhatsApp a reaction is real: it goes out through Evolution's sendReaction
and the customer's own reactions come back through the webhook
(FileWhatsAppReaction). Every other channel has no way to carry one, so it is
stored and shown in Moharrik only — the client says so on the chip.

A quote is three columns on messages (197_message_reply_to.sql). An outgoing
reply names the message it quotes; WhatsApp gets the `quoted` field on the
send, email gets In-Reply-To set to the quoted mail's Message-ID, and every
channel shows the quote in the thread.
*/

// ReplyToOut is the message a message quotes.
type ReplyToOut struct {
	MessageID    *string `json:"message_id" doc:"The quoted message, when it is in this thread (null if it never reached Moharrik)."`
	Body         string  `json:"body" doc:"A snippet of the quoted text; empty for a quoted attachment with no caption."`
	Direction    string  `json:"direction,omitempty" doc:"in or out, when the quoted message is known."`
	AuthorUserID *string `json:"author_user_id,omitempty"`
	Attachment   bool    `json:"attachment,omitempty" doc:"The quoted message carried an attachment."`
}

// ReactionOut is one emoji's reactions on a message.
type ReactionOut struct {
	Emoji    string       `json:"emoji"`
	Count    int          `json:"count"`
	Mine     bool         `json:"mine" doc:"Clicking this emoji would take it back: it is the signed-in member's reaction (on WhatsApp, the number's)."`
	Reactors []ReactorOut `json:"reactors"`
}

// ReactorOut is who reacted.
type ReactorOut struct {
	Kind   string `json:"kind" enum:"customer,member,phone" doc:"customer = the other side; member = a teammate (user_id); phone = the linked WhatsApp phone itself."`
	UserID string `json:"user_id,omitempty"`
}

// ReactionResult is what reacting answers.
type ReactionResult struct {
	Emoji     string        `json:"emoji" doc:"The signed-in member's reaction now; empty when it was taken back."`
	Delivered bool          `json:"delivered" doc:"true when it went out on the channel (WhatsApp); false when it is shown in Moharrik only."`
	Reactions []ReactionOut `json:"reactions"`
}

const replySnippet = 300

type replyToKey struct{}

// WithReplyTo makes the next Reply/ReplyWithAttachments in ctx quote the
// message msgID (MH-664). Carried in the context so the reply's existing
// callers (REST, MCP, automation rules) keep their signatures.
func WithReplyTo(ctx context.Context, msgID string) context.Context {
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		return ctx
	}
	return context.WithValue(ctx, replyToKey{}, msgID)
}

func replyToFrom(ctx context.Context) string {
	s, _ := ctx.Value(replyToKey{}).(string)
	return s
}

// quoted is the message a reply is about to quote, read before the send.
type quoted struct {
	ID         string
	ExternalID string
	Body       string
	Direction  string
}

// quotedMessage reads the message a reply quotes. It must be in the same
// workspace AND the same conversation: anything else answers 404, the same
// as a message id that does not exist.
func quotedMessage(ctx context.Context, db *sql.DB, wsID, convID, msgID string) (*quoted, error) {
	q := &quoted{ID: msgID}
	err := db.QueryRowContext(ctx, `
		SELECT external_id, body, direction FROM messages
		WHERE workspace_id = $1 AND conversation_id = $2 AND id = $3`, wsID, convID, msgID).
		Scan(&q.ExternalID, &q.Body, &q.Direction)
	if notFound(err) {
		return nil, huma.Error404NotFound("the message being replied to was not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the message being replied to")
	}
	return q, nil
}

type emailQuoteKey struct{}

// withEmailQuote makes SendEmailReply answer this Message-ID specifically.
func withEmailQuote(ctx context.Context, messageID string) context.Context {
	if messageID == "" {
		return ctx
	}
	return context.WithValue(ctx, emailQuoteKey{}, messageID)
}

func emailQuoteFrom(ctx context.Context) string {
	s, _ := ctx.Value(emailQuoteKey{}).(string)
	return s
}

// setReplyTo records what a message quotes. externalID/body describe it as
// the channel did; the local row is looked up by that external id when the
// caller does not already know it.
func setReplyTo(ctx context.Context, db *sql.DB, wsID, msgID string, localID, externalID, body string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE messages SET
		  reply_to_message_id = COALESCE($3::text,
		    (SELECT q.id FROM messages q WHERE q.workspace_id = $1 AND $4 <> '' AND q.external_id = $4 LIMIT 1)),
		  reply_to_external_id = $4,
		  reply_to_body = $5
		WHERE workspace_id = $1 AND id = $2`,
		wsID, msgID, nullable(localID), externalID, trimTo(strings.TrimSpace(body), replySnippet))
	return err
}

// fileInboundQuote stores the quote an inbound WhatsApp message carried
// (whatsapp.WithQuote, set by the webhook). Best effort: the message itself
// is already filed, and a quote that could not be recorded costs a snippet,
// not the customer's words.
func fileInboundQuote(ctx context.Context, a *app.App, db *sql.DB, wsID, msgID string) {
	q := whatsapp.QuoteFrom(ctx)
	if q == nil || q.ExternalID == "" {
		return
	}
	if err := setReplyTo(ctx, db, wsID, msgID, "", q.ExternalID, q.Body); err != nil {
		a.Log.Warn("could not record a WhatsApp reply's quote", "workspace", wsID, "err", err)
	}
}

// attachThreadExtras fills in each message's quote and reactions, one query
// each for the whole page. userID decides "mine"; "" leaves every one false.
func attachThreadExtras(ctx context.Context, db *sql.DB, wsID, convID, userID string, msgs []MessageOut) error {
	if len(msgs) == 0 {
		return nil
	}
	ids := make([]string, len(msgs))
	at := make(map[string]int, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
		at[m.ID] = i
	}

	rows, err := db.QueryContext(ctx, `
		SELECT m.id, m.reply_to_body, q.id, q.body, q.direction, q.author_user_id,
		       COALESCE(q.id IS NOT NULL AND EXISTS (SELECT 1 FROM message_attachments ma
		         WHERE ma.workspace_id = q.workspace_id AND ma.message_id = q.id), false)
		FROM messages m
		LEFT JOIN LATERAL (
		  SELECT id, body, direction, author_user_id, workspace_id FROM messages q
		  WHERE q.workspace_id = m.workspace_id
		    AND (q.id = m.reply_to_message_id
		         OR (m.reply_to_message_id IS NULL AND m.reply_to_external_id <> '' AND q.external_id = m.reply_to_external_id))
		  LIMIT 1
		) q ON true
		WHERE m.workspace_id = $1 AND m.id = ANY($2)
		  AND (m.reply_to_message_id IS NOT NULL OR m.reply_to_external_id <> '')`, wsID, ids)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, cached string
		var qid, qbody, qdir, qauthor sql.NullString
		var hasAtt bool
		if err := rows.Scan(&id, &cached, &qid, &qbody, &qdir, &qauthor, &hasAtt); err != nil {
			rows.Close()
			return err
		}
		r := &ReplyToOut{MessageID: strp(qid), Body: cached, Direction: qdir.String, AuthorUserID: strp(qauthor), Attachment: hasAtt}
		if qid.Valid {
			r.Body = trimTo(strings.TrimSpace(qbody.String), replySnippet)
		}
		msgs[at[id]].ReplyTo = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var channel string
	if err := db.QueryRowContext(ctx, `SELECT channel FROM conversations WHERE workspace_id = $1 AND id = $2`,
		wsID, convID).Scan(&channel); err != nil && !notFound(err) {
		return err
	}
	byMsg, err := reactionsFor(ctx, db, wsID, ids, myReactorKey(channel, userID))
	if err != nil {
		return err
	}
	for id, rs := range byMsg {
		msgs[at[id]].Reactions = rs
	}
	return nil
}

// myReactorKey is the slot the signed-in member's reaction occupies: the
// number's shared one on WhatsApp, their own everywhere else.
func myReactorKey(channel, userID string) string {
	if channel == "whatsapp" {
		return "team"
	}
	if userID == "" {
		return ""
	}
	return "user:" + userID
}

// reactionsFor aggregates the reactions on ids, by emoji in the order each
// emoji first appeared.
func reactionsFor(ctx context.Context, db *sql.DB, wsID string, ids []string, mine string) (map[string][]ReactionOut, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT message_id, reactor_key, COALESCE(user_id, ''), emoji FROM message_reactions
		WHERE workspace_id = $1 AND message_id = ANY($2)
		ORDER BY created_at, reactor_key`, wsID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]ReactionOut{}
	for rows.Next() {
		var msgID, key, userID, emoji string
		if err := rows.Scan(&msgID, &key, &userID, &emoji); err != nil {
			return nil, err
		}
		who := ReactorOut{Kind: "member", UserID: userID}
		switch {
		case key == "customer":
			who = ReactorOut{Kind: "customer"}
		case key == "team" && userID == "":
			who = ReactorOut{Kind: "phone"}
		}
		list := out[msgID]
		i := -1
		for j := range list {
			if list[j].Emoji == emoji {
				i = j
				break
			}
		}
		if i < 0 {
			list = append(list, ReactionOut{Emoji: emoji, Reactors: []ReactorOut{}})
			i = len(list) - 1
		}
		list[i].Count++
		list[i].Reactors = append(list[i].Reactors, who)
		if mine != "" && key == mine {
			list[i].Mine = true
		}
		out[msgID] = list
	}
	return out, rows.Err()
}

// validEmoji accepts one emoji (a ZWJ sequence, a flag, a skin-tone
// variant…) and nothing that is plainly text.
func validEmoji(s string) bool {
	if s == "" || len(s) > 32 || !utf8.ValidString(s) || utf8.RuneCountInString(s) > 12 {
		return false
	}
	for _, r := range s {
		if r < 0x80 && r != '#' && r != '*' && !(r >= '0' && r <= '9') {
			return false
		}
		if unicode.IsSpace(r) || unicode.IsControl(r) || unicode.IsLetter(r) {
			return false
		}
	}
	// A bare digit/#/* is not an emoji without its keycap mark.
	return strings.ContainsFunc(s, func(r rune) bool { return r >= 0x80 })
}

// react sets or toggles the signed-in member's reaction on msgID. toggle:
// the same emoji again takes it back. emoji "" with toggle=false removes it.
func react(ctx context.Context, a *app.App, msgID, emoji string, toggle bool) (*ReactionResult, error) {
	s, err := scope(ctx, authz.InboxWrite)
	if err != nil {
		return nil, err
	}
	emoji = strings.TrimSpace(emoji)
	if emoji != "" && !validEmoji(emoji) {
		return nil, huma.Error422UnprocessableEntity("reaction must be a single emoji")
	}
	convID, err := messageWorkspace(ctx, a.SQLDB, s.WorkspaceID, msgID)
	if err != nil {
		return nil, err
	}
	var channel, phone, direction, externalID string
	if err := a.SQLDB.QueryRowContext(ctx, `
		SELECT c.channel, c.customer_phone, m.direction, m.external_id
		FROM messages m JOIN conversations c ON c.workspace_id = m.workspace_id AND c.id = m.conversation_id
		WHERE m.workspace_id = $1 AND m.id = $2`, s.WorkspaceID, msgID).
		Scan(&channel, &phone, &direction, &externalID); err != nil {
		if notFound(err) {
			return nil, huma.Error404NotFound("message not found")
		}
		return nil, huma.Error500InternalServerError("could not read the message")
	}
	key := myReactorKey(channel, s.UserID)

	var current string
	if err := a.SQLDB.QueryRowContext(ctx, `
		SELECT emoji FROM message_reactions WHERE workspace_id = $1 AND message_id = $2 AND reactor_key = $3`,
		s.WorkspaceID, msgID, key).Scan(&current); err != nil && !notFound(err) {
		return nil, huma.Error500InternalServerError("could not read the reaction")
	}
	next := emoji
	if toggle && emoji == current {
		next = ""
	}

	delivered := false
	if channel == "whatsapp" && next != current {
		if externalID == "" {
			return nil, huma.Error422UnprocessableEntity("this message has no WhatsApp id to react to")
		}
		// Sent first, stored after — the same rule as a reply: a reaction the
		// customer never saw must not sit on the bubble looking sent.
		if err := whatsapp.SendReactionAs(threadNumber(ctx, a.SQLDB, s.WorkspaceID, convID), a, s.WorkspaceID, phone, externalID, direction == "out", next); err != nil {
			return nil, whatsappError(err)
		}
		delivered = true
	} else if channel == "whatsapp" {
		delivered = true
	}

	if err := storeReaction(ctx, a.SQLDB, s.WorkspaceID, msgID, key, nullable(s.UserID), next); err != nil {
		return nil, huma.Error500InternalServerError("could not save the reaction")
	}
	changed(ctx, a, s.WorkspaceID, realtime.Event{Kind: "reaction", ConversationID: convID, AuthorUserID: s.UserID})

	byMsg, err := reactionsFor(ctx, a.SQLDB, s.WorkspaceID, []string{msgID}, key)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the reactions")
	}
	rs := byMsg[msgID]
	if rs == nil {
		rs = []ReactionOut{}
	}
	return &ReactionResult{Emoji: next, Delivered: delivered, Reactions: rs}, nil
}

// storeReaction sets one reactor's slot, or clears it for emoji "". Setting
// the emoji a slot already has changes nothing — not even who it is credited
// to — which is what makes the webhook echo of this app's own sendReaction
// (fromMe, no member attached) harmless.
func storeReaction(ctx context.Context, db *sql.DB, wsID, msgID, key string, userID any, emoji string) error {
	if emoji == "" {
		_, err := db.ExecContext(ctx, `
			DELETE FROM message_reactions WHERE workspace_id = $1 AND message_id = $2 AND reactor_key = $3`,
			wsID, msgID, key)
		return err
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO message_reactions (workspace_id, message_id, reactor_key, user_id, emoji)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (workspace_id, message_id, reactor_key) DO UPDATE SET
		  user_id = CASE WHEN message_reactions.emoji = EXCLUDED.emoji THEN message_reactions.user_id ELSE EXCLUDED.user_id END,
		  updated_at = CASE WHEN message_reactions.emoji = EXCLUDED.emoji THEN message_reactions.updated_at ELSE now() END,
		  emoji = EXCLUDED.emoji`,
		wsID, msgID, key, userID, emoji)
	return err
}

// FileWhatsAppReaction files a reaction from the webhook (MH-664) against
// the message it reacts to, found by WhatsApp's id. fromMe is the linked
// number (the echo of this app's own send, or a reaction made on the phone);
// otherwise it is the customer. A reaction to a message Moharrik never
// stored is dropped: there is nothing to put it on, and it is never a
// message of its own.
func FileWhatsAppReaction(ctx context.Context, a *app.App, workspaceID, phone, targetExternalID, emoji string, fromMe bool) error {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	db := a.SQLDB
	var msgID, convID string
	err := db.QueryRowContext(sctx, `
		SELECT m.id, m.conversation_id FROM messages m
		JOIN conversations c ON c.workspace_id = m.workspace_id AND c.id = m.conversation_id
		WHERE m.workspace_id = $1 AND m.external_id = $2 AND c.channel = 'whatsapp'
		LIMIT 1`, workspaceID, targetExternalID).Scan(&msgID, &convID)
	if notFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if emoji != "" && !validEmoji(emoji) {
		return nil
	}
	key := "customer"
	if fromMe {
		key = "team"
	}
	if err := storeReaction(sctx, db, workspaceID, msgID, key, nil, emoji); err != nil {
		return err
	}
	changed(ctx, a, workspaceID, realtime.Event{Kind: "reaction", ConversationID: convID})
	return nil
}

func registerReactions(api huma.API, a *app.App) {
	// The path id is declared directly: huma does not look inside an embedded
	// unexported struct next to a Body, so an embedded idPath came through empty.
	huma.Register(api, huma.Operation{
		OperationID: "w-react-message", Method: http.MethodPut, Path: "/api/w/messages/{id}/reaction",
		Summary:     "React to a message with an emoji, or take the reaction back (MH-664)",
		Description: "One reaction per member per message; on WhatsApp one per number, sent to the customer through the linked phone. On other channels the reaction is shown in Moharrik only (delivered=false).",
		Tags:        tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Emoji string `json:"emoji" maxLength:"32" doc:"One emoji. Reacting with the emoji you already have takes it back; a different one replaces it."`
		}
	}) (*body[ReactionResult], error) {
		if strings.TrimSpace(in.Body.Emoji) == "" {
			return nil, huma.Error422UnprocessableEntity("emoji is required; DELETE removes a reaction")
		}
		r, err := react(ctx, a, in.ID, in.Body.Emoji, true)
		if err != nil {
			return nil, err
		}
		return out(*r), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-unreact-message", Method: http.MethodDelete, Path: "/api/w/messages/{id}/reaction",
		Summary: "Remove the signed-in member's reaction from a message (MH-664)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[ReactionResult], error) {
		r, err := react(ctx, a, in.ID, "", false)
		if err != nil {
			return nil, err
		}
		return out(*r), nil
	})
}

// React toggles the signed-in member's reaction (MH-664). Exported for
// callers outside the REST surface (MCP).
func React(ctx context.Context, a *app.App, msgID, emoji string) (*ReactionResult, error) {
	return react(ctx, a, msgID, emoji, true)
}
