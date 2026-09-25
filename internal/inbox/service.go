package inbox

import (
	"context"
	"database/sql"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/channels"
	"github.com/fadymondy/moharrik/internal/contacts"
	"github.com/fadymondy/moharrik/internal/crm"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/paging"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

/*
The inbox's operations, once (MH-380).

The REST handlers in routes.go and crm_link.go and the MCP tools in
internal/mcp call these same functions, so a reply an agent sends goes out
through exactly the path a member's does — the conversation's real channel,
the same unread, reopen and closed_at rules, the same realtime event — and a
snooze, a contact link or a deal opened from a thread follows MH-379/383's
rules whoever asks. The two sides can never drift apart.

Each one reads the caller from ctx and checks the role itself, so neither
caller can forget to. Errors are huma status errors: REST returns them as they
are, and MCP hands back their message.
*/

// ConversationFilter narrows the inbox list. Every field is optional.
type ConversationFilter struct {
	Status    string
	Channel   string
	Unread    bool
	ContactID string
	MailboxID string
	// CustomerEmail (MH-600) narrows the list to one sender, case-insensitive —
	// what clicking a row in the "grouped by sender" view (see
	// ListConversationsBySender) does: the group's own email, lower-cased, so
	// "Jane@x.com" and "jane@x.com" narrow to the same conversations they were
	// grouped with.
	CustomerEmail string
	// Q is a full-text search (MH-556) over the customer's name, email and
	// phone, the subject, AND every message's body (not just the preview) —
	// conversations.search and messages.search, both generated tsvector
	// columns (internal/db/post/150_mailboxes.sql).
	Q string
	// Cursor walks back from the newest page (MH-556, the same keyset shape
	// thread.go's threadPage already uses for a thread's own messages).
	Cursor string
	Limit  int
	// Order "unread_first" (MH-592) puts every unread conversation before the
	// read ones, each group newest first; the default is newest first only.
	// The cursor carries the unread flag too, so a page boundary that falls
	// between the two groups still walks on correctly.
	Order string
	// Keep (MH-599) is the conversations this tab opened while they were
	// unread. Opening one reads it, and under unread_first a read row drops
	// below every unread one — so the row a person just left moved, and the
	// slot they came back to held another conversation. Kept ids sort as if
	// still unread until the page is reloaded; they still SHOW as read.
	Keep []string
}

// ConversationList is a page of the inbox, most recent first.
type ConversationList struct {
	Conversations []ConversationOut `json:"conversations"`
	UnreadCount   int64             `json:"unread_count"`
	NextCursor    string            `json:"next_cursor,omitempty" doc:"Fetches the next OLDER page. Empty when this page reaches the last conversation."`
	// ChannelCounts is one row per channel the workspace has a conversation on
	// OR a connection for (an enabled chat widget, an enabled mailbox, a linked
	// WhatsApp number) — MH-568's channel tabs. total and unread are narrowed
	// by the SAME status and q this call used, but never by channel, unread or
	// mailbox_id themselves: the whole point is comparing channels against
	// each other under the rest of the filters. Data-driven on purpose — a
	// channel this build has never heard of (telegram, slack, a workflow feed)
	// still gets counted and a tab, the moment a row names it.
	ChannelCounts []ChannelCount `json:"channel_counts"`
}

// ChannelCount is one channel's conversation counts (MH-568). See
// ConversationList.ChannelCounts for what total and unread are narrowed by.
type ChannelCount struct {
	Channel string `json:"channel"`
	Total   int64  `json:"total"`
	Unread  int64  `json:"unread"`
}

// ConversationPatch is a partial update. nil = unchanged; "" unassigns/unlinks.
type ConversationPatch struct {
	Status         *string `json:"status,omitempty" enum:"open,snoozed,closed"`
	SnoozedUntil   *string `json:"snoozed_until,omitempty" doc:"RFC 3339. Snoozes the conversation until then (it must be in the future). Empty string wakes it."`
	AssigneeUserID *string `json:"assignee_user_id,omitempty" doc:"A member's user id; empty string unassigns."`
	ContactID      *string `json:"contact_id,omitempty" doc:"A CRM contact id; empty string unlinks (and stops the automatic email/phone match relinking it)."`
	Subject        *string `json:"subject,omitempty" maxLength:"200"`
	Unread         *bool   `json:"unread,omitempty"`
}

// maxKeep bounds ConversationFilter.Keep: a long session reads many threads,
// but the query string and the ANY() list stay small.
const maxKeep = 100

// ListConversations is the inbox, most recent first. A snooze that has run out
// is over whether or not the waker has come round yet: the list never shows a
// thread as snoozed past its time.
func ListConversations(ctx context.Context, a *app.App, f ConversationFilter) (*ConversationList, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	if f.Status != "" && !statuses[f.Status] {
		return nil, huma.Error422UnprocessableEntity("status must be open, snoozed or closed")
	}
	// Channel is deliberately NOT checked against a fixed set: telegram,
	// slack, discord and workflow-fed channels (MH-563/564) already exist on
	// main, and a filter on one of them must work the moment a conversation
	// exists on it, not the next time this package is redeployed. An unknown
	// channel just matches nothing, the same as any other filter with no hits.
	f.Channel = strings.TrimSpace(f.Channel)
	wakeDue(ctx, a, s.WorkspaceID)
	q := conversationSelect
	args := []any{s.WorkspaceID}
	add := func(clause string, v any) {
		args = append(args, v)
		q += strings.ReplaceAll(clause, "$?", "$"+strconv.Itoa(len(args)))
	}
	if f.Status != "" {
		add(` AND c.status = $?`, f.Status)
	}
	if f.Channel != "" {
		add(` AND c.channel = $?`, f.Channel)
	}
	if f.Unread {
		q += ` AND c.unread`
	}
	if id := strings.TrimSpace(f.ContactID); id != "" {
		add(` AND c.contact_id = $?`, id)
	}
	if id := strings.TrimSpace(f.MailboxID); id != "" {
		add(` AND c.mailbox_id = $?`, id)
	}
	if email := strings.ToLower(strings.TrimSpace(f.CustomerEmail)); email != "" {
		add(` AND lower(c.customer_email) = $?`, email)
	}
	if term := strings.TrimSpace(f.Q); term != "" {
		// Matches the conversation's own fields (customer, subject) OR any
		// message in its thread — a hit inside a long thread still surfaces
		// the conversation, not just a hit in its preview.
		args = append(args, term, term)
		n1, n2 := strconv.Itoa(len(args)-1), strconv.Itoa(len(args))
		q += ` AND (c.search @@ websearch_to_tsquery('simple', $` + n1 + `)
		      OR EXISTS (SELECT 1 FROM messages sm WHERE sm.workspace_id = c.workspace_id AND sm.conversation_id = c.id
		                 AND sm.search @@ websearch_to_tsquery('simple', $` + n2 + `)))`
	}
	// A conversation with no messages has nothing to open (MH-599): the
	// WhatsApp import used to file one for every chat the phone listed, even
	// one with no history, and it read as a thread whose messages had gone.
	q += ` AND EXISTS (SELECT 1 FROM messages em WHERE em.workspace_id = c.workspace_id AND em.conversation_id = c.id)`
	unreadFirst := f.Order == "unread_first"
	// unreadKey is what unread_first sorts and pages on: unread, or kept.
	unreadKey := `c.unread`
	kept := map[string]bool{}
	if unreadFirst {
		var ids []string
		for _, id := range f.Keep {
			if id = strings.TrimSpace(id); id != "" && !kept[id] && len(ids) < maxKeep {
				kept[id] = true
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			args = append(args, strings.Join(ids, ","))
			unreadKey = `(c.unread OR c.id::text = ANY(string_to_array($` + strconv.Itoa(len(args)) + `, ',')))`
		}
	}
	parts := 2
	if unreadFirst {
		parts = 3
	}
	cur, err := paging.Decode(f.Cursor, parts)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	if cur != nil {
		at, perr := time.Parse(time.RFC3339Nano, cur[parts-2])
		if perr != nil {
			return nil, huma.Error422UnprocessableEntity(paging.ErrBadCursor.Error())
		}
		if unreadFirst {
			// Every column descends, so "after the cursor" is one row compare.
			args = append(args, cur[0] == "1", at, cur[2])
			n := len(args)
			q += ` AND (` + unreadKey + `::int, c.last_message_at, c.id) < ($` + strconv.Itoa(n-2) + `::boolean::int, $` + strconv.Itoa(n-1) + `, $` + strconv.Itoa(n) + `)`
		} else {
			args = append(args, at, cur[1])
			n1, n2 := strconv.Itoa(len(args)-1), strconv.Itoa(len(args))
			q += ` AND (c.last_message_at, c.id) < ($` + n1 + `, $` + n2 + `)`
		}
	}
	limit, lerr := paging.Limit(f.Limit, 50, 200)
	if lerr != nil {
		return nil, huma.Error422UnprocessableEntity(lerr.Error())
	}
	if unreadFirst {
		q += ` ORDER BY ` + unreadKey + ` DESC, c.last_message_at DESC, c.id DESC LIMIT ` + strconv.Itoa(limit+1)
	} else {
		q += ` ORDER BY c.last_message_at DESC, c.id DESC LIMIT ` + strconv.Itoa(limit+1)
	}
	rows, err := a.SQLDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not list conversations")
	}
	defer rows.Close()
	list := []ConversationOut{}
	for rows.Next() {
		c, err := scanConversation(s.WorkspaceID, rows)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list conversations")
		}
		list = append(list, c)
	}
	// MH-602 defect 1: fetch stale/never-fetched WhatsApp photos in the
	// background the moment a conversation is first listed, not only on its
	// next inbound message.
	triggerPhotoRefreshes(a, s.WorkspaceID, list)
	res := &ConversationList{Conversations: list}
	if len(list) > limit {
		last := list[limit-1]
		at := last.LastMessageAt.UTC().Format(time.RFC3339Nano)
		if unreadFirst {
			flag := "0"
			if last.Unread || kept[last.ID] {
				flag = "1"
			}
			res.NextCursor = paging.Encode(flag, at, last.ID)
		} else {
			res.NextCursor = paging.Encode(at, last.ID)
		}
		res.Conversations = list[:limit]
	}
	if err := a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM conversations WHERE workspace_id = $1 AND unread`, s.WorkspaceID).Scan(&res.UnreadCount); err != nil {
		return nil, huma.Error500InternalServerError("could not count unread conversations")
	}
	cc, err := channelCounts(ctx, a.SQLDB, s.WorkspaceID, f.Status, f.Q)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not count conversations per channel")
	}
	res.ChannelCounts = cc
	return res, nil
}

// channelCounts is every channel the workspace has a conversation on OR a
// connection for, each with its total and unread count (MH-568). status and q
// narrow the counts the same way they narrow ListConversations' own query;
// channel, unread and mailbox_id do not — see ConversationList.ChannelCounts.
//
// "a connection" is deliberately loose: an enabled chat widget, an enabled
// mailbox, or a WhatsApp number linked at any point (whatsapp.Status uses the
// same test — a link that is currently reconnecting is still a channel worth
// a tab). A channel with neither a connection nor a single conversation gets
// no row at all, so the frontend never renders an empty tab for a channel
// this workspace has nothing to do with.
func channelCounts(ctx context.Context, db *sql.DB, wsID, status, q string) ([]ChannelCount, error) {
	args := []any{wsID}
	// Empty conversations are not listed (MH-599), so they are not counted.
	where := "workspace_id = $1 AND EXISTS (SELECT 1 FROM messages em WHERE em.workspace_id = conversations.workspace_id AND em.conversation_id = conversations.id)"
	if status != "" {
		args = append(args, status)
		where += " AND status = $" + strconv.Itoa(len(args))
	}
	if term := strings.TrimSpace(q); term != "" {
		args = append(args, term)
		n := strconv.Itoa(len(args))
		where += ` AND (search @@ websearch_to_tsquery('simple', $` + n + `)
			OR EXISTS (SELECT 1 FROM messages sm WHERE sm.workspace_id = conversations.workspace_id AND sm.conversation_id = conversations.id
			           AND sm.search @@ websearch_to_tsquery('simple', $` + n + `)))`
	}
	rows, err := db.QueryContext(ctx, `
		WITH counts AS (
			SELECT channel, count(*) AS total, count(*) FILTER (WHERE unread) AS unread
			FROM conversations WHERE `+where+` GROUP BY channel
		), connected AS (
			SELECT 'chat' AS channel WHERE EXISTS (SELECT 1 FROM chat_widgets WHERE workspace_id = $1 AND enabled)
			UNION ALL
			SELECT 'email' WHERE EXISTS (SELECT 1 FROM mailboxes WHERE workspace_id = $1 AND enabled)
			UNION ALL
			SELECT 'whatsapp' WHERE EXISTS (SELECT 1 FROM whatsapp_links WHERE workspace_id = $1)
		)
		SELECT coalesce(c.channel, n.channel), coalesce(c.total, 0), coalesce(c.unread, 0)
		FROM counts c FULL OUTER JOIN connected n ON n.channel = c.channel
		ORDER BY 1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []ChannelCount{}
	for rows.Next() {
		var c ChannelCount
		if err := rows.Scan(&c.Channel, &c.Total, &c.Unread); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// SenderGroupFilter narrows the sender-grouped email view (MH-600). It is
// deliberately smaller than ConversationFilter: grouping only makes sense for
// one channel (email — the only channel where "the same person, several
// threads" is common enough to want this), so there is no Status/Unread/
// ContactID/Order/Keep here.
type SenderGroupFilter struct {
	MailboxID string
	Q         string
	Cursor    string
	Limit     int
}

// SenderGroup is one sender's emails, rolled up (MH-600): "group by sender"
// on the inbox's email tab. sender_email is always lower-cased — the whole
// point is that "Jane@x.com" and "jane@x.com" are the same group.
type SenderGroup struct {
	SenderEmail          string    `json:"sender_email"`
	SenderName           string    `json:"sender_name" doc:"The customer name on the most recently active of this sender's conversations."`
	ConversationCount    int64     `json:"conversation_count"`
	UnreadCount          int64     `json:"unread_count"`
	LatestSubject        string    `json:"latest_subject"`
	LatestPreview        string    `json:"latest_preview" doc:"The most recent message across every conversation from this sender, trimmed."`
	LatestMessageAt      time.Time `json:"latest_message_at"`
	LatestConversationID string    `json:"latest_conversation_id" doc:"The conversation the preview/subject above came from."`
}

// SenderGroupList is a page of sender groups, unread-first then newest.
type SenderGroupList struct {
	Groups     []SenderGroup `json:"groups"`
	NextCursor string        `json:"next_cursor,omitempty" doc:"Fetches the next page of groups. Empty when this page reaches the last sender."`
}

// ListConversationsBySender rolls the email channel's conversations up by
// customer_email (MH-600): one row per sender, so a member can see at a
// glance who has written several times before opening any of them. Every
// group sorts unread-first (any conversation in the group unread) then by
// its most recent activity, matching ListConversations' own unread_first
// order so switching between the flat list and the grouped view feels like
// the same list, reshaped.
//
// A conversation with no messages is excluded, the same as ListConversations
// (MH-599) — a sender who has only ever had an empty thread filed for them
// (the WhatsApp-import bug MH-599 fixed, in principle, for any channel)
// should not appear as a group with nothing to open.
func ListConversationsBySender(ctx context.Context, a *app.App, f SenderGroupFilter) (*SenderGroupList, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	wakeDue(ctx, a, s.WorkspaceID)
	args := []any{s.WorkspaceID}
	filtered := `
		SELECT c.id, c.customer_email, c.customer_name, c.subject, c.unread, c.last_message_at,
		       coalesce((SELECT m.body FROM messages m WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id
		                 ORDER BY m.created_at DESC LIMIT 1), '') AS preview
		FROM conversations c
		WHERE c.workspace_id = $1 AND c.channel = 'email' AND c.customer_email <> ''
		  AND EXISTS (SELECT 1 FROM messages em WHERE em.workspace_id = c.workspace_id AND em.conversation_id = c.id)`
	if id := strings.TrimSpace(f.MailboxID); id != "" {
		args = append(args, id)
		filtered += ` AND c.mailbox_id = $` + strconv.Itoa(len(args))
	}
	if term := strings.TrimSpace(f.Q); term != "" {
		args = append(args, term, term)
		n1, n2 := strconv.Itoa(len(args)-1), strconv.Itoa(len(args))
		filtered += ` AND (c.search @@ websearch_to_tsquery('simple', $` + n1 + `)
		      OR EXISTS (SELECT 1 FROM messages sm WHERE sm.workspace_id = c.workspace_id AND sm.conversation_id = c.id
		                 AND sm.search @@ websearch_to_tsquery('simple', $` + n2 + `)))`
	}
	q := `
		WITH filtered AS (` + filtered + `),
		ranked AS (
			SELECT *, row_number() OVER (PARTITION BY lower(customer_email) ORDER BY last_message_at DESC, id DESC) AS rn
			FROM filtered
		)
		SELECT lower(customer_email) AS sender_email,
		       (array_agg(customer_name ORDER BY last_message_at DESC))[1] AS sender_name,
		       count(*) AS conversation_count,
		       count(*) FILTER (WHERE unread) AS unread_count,
		       max(last_message_at) AS latest_message_at,
		       max(subject) FILTER (WHERE rn = 1) AS latest_subject,
		       max(preview) FILTER (WHERE rn = 1) AS latest_preview,
		       max(id) FILTER (WHERE rn = 1) AS latest_conversation_id
		FROM ranked
		GROUP BY lower(customer_email)`
	cur, err := paging.Decode(f.Cursor, 3)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	if cur != nil {
		at, perr := time.Parse(time.RFC3339Nano, cur[1])
		if perr != nil {
			return nil, huma.Error422UnprocessableEntity(paging.ErrBadCursor.Error())
		}
		args = append(args, cur[0] == "1", at, cur[2])
		n := len(args)
		q += ` HAVING (bool_or(unread)::int, max(last_message_at), lower(customer_email)) < ($` +
			strconv.Itoa(n-2) + `::boolean::int, $` + strconv.Itoa(n-1) + `, $` + strconv.Itoa(n) + `)`
	}
	limit, lerr := paging.Limit(f.Limit, 50, 200)
	if lerr != nil {
		return nil, huma.Error422UnprocessableEntity(lerr.Error())
	}
	q += ` ORDER BY bool_or(unread) DESC, max(last_message_at) DESC, lower(customer_email) DESC LIMIT ` + strconv.Itoa(limit+1)
	rows, err := a.SQLDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not list conversations by sender")
	}
	defer rows.Close()
	list := []SenderGroup{}
	for rows.Next() {
		var g SenderGroup
		if err := rows.Scan(&g.SenderEmail, &g.SenderName, &g.ConversationCount, &g.UnreadCount,
			&g.LatestMessageAt, &g.LatestSubject, &g.LatestPreview, &g.LatestConversationID); err != nil {
			return nil, huma.Error500InternalServerError("could not list conversations by sender")
		}
		g.LatestPreview = trim(g.LatestPreview, 140)
		list = append(list, g)
	}
	if err := rows.Err(); err != nil {
		return nil, huma.Error500InternalServerError("could not list conversations by sender")
	}
	res := &SenderGroupList{Groups: list}
	if len(list) > limit {
		last := list[limit-1]
		flag := "0"
		if last.UnreadCount > 0 {
			flag = "1"
		}
		res.NextCursor = paging.Encode(flag, last.LatestMessageAt.UTC().Format(time.RFC3339Nano), last.SenderEmail)
		res.Groups = list[:limit]
	}
	return res, nil
}

// GetConversation reads one conversation and the newest page of its thread
// (with the thread's total and the cursor to the page before), with what the
// visitor gave (to prefill a contact) and the language to answer in. Opening a
// thread is what "read" means here, so it clears the unread flag — a caller
// with inbox:read only is still allowed to: seeing it IS reading it.
func GetConversation(ctx context.Context, a *app.App, id string) (*conversationDetail, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	wakeDue(ctx, a, s.WorkspaceID)
	c, err := readConversation(ctx, a.SQLDB, s.WorkspaceID, id)
	if notFound(err) {
		return nil, huma.Error404NotFound("conversation not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the conversation")
	}
	// MH-602 defect 1: the same "first shown" trigger as the list, for a
	// conversation opened directly (a permalink, a notification) without
	// ever appearing in a list response first.
	triggerPhotoRefreshes(a, s.WorkspaceID, []ConversationOut{c})
	page, err := threadPage(ctx, a.SQLDB, s.WorkspaceID, id, "", threadPageDefault)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the thread")
	}
	msgs := page.Messages
	if c.Unread {
		_, _ = a.SQLDB.ExecContext(ctx, `UPDATE conversations SET unread = false, updated_at = now() WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, id)
		c.Unread = false
		// Every other tab's bell drops its count.
		changed(ctx, a, s.WorkspaceID, realtime.Event{Kind: "conversation", ConversationID: id})
	}
	lang, err := replyLanguageFor(ctx, a.SQLDB, s.WorkspaceID, c, msgs)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the conversation")
	}
	return &conversationDetail{Conversation: c, Messages: msgs, MessagesTotal: page.Total, MessagesNextCursor: page.NextCursor,
		Visitor: guessVisitor(c, msgs, lang), ReplyLanguage: lang}, nil
}

// Reply answers a conversation as the caller, through the conversation's own
// channel: chat is picked up by the widget, email goes out over the
// workspace's SMTP, WhatsApp through its linked number.
func Reply(ctx context.Context, a *app.App, id, text string) (*MessageOut, error) {
	s, err := scope(ctx, authz.InboxWrite)
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, huma.Error422UnprocessableEntity("a message is required")
	}
	if len(text) > maxMessage {
		return nil, huma.Error422UnprocessableEntity("that message is too long (at most 8000 characters)")
	}
	var channel, customerEmail, customerPhone, subject, sourceChannelID, externalThreadID string
	var mailboxID sql.NullString
	if err := a.SQLDB.QueryRowContext(ctx, `
		SELECT channel, customer_email, customer_phone, subject, mailbox_id, coalesce(source_channel_id, ''), external_thread_id
		FROM conversations WHERE workspace_id = $1 AND id = $2`,
		s.WorkspaceID, id).Scan(&channel, &customerEmail, &customerPhone, &subject, &mailboxID, &sourceChannelID, &externalThreadID); err != nil {
		if notFound(err) {
			return nil, huma.Error404NotFound("conversation not found")
		}
		return nil, huma.Error500InternalServerError("could not read the conversation")
	}

	// An email thread is answered by EMAIL, and the send happens before the
	// row is written: a reply the customer never received must not sit in
	// the thread looking sent. Chat is the other way round (the widget polls
	// for it), which is why this is here rather than after the insert.
	var sentID string
	switch channel {
	case "email":
		sentID, err = SendEmailReply(ctx, a, s.WorkspaceID, id, mailboxID.String, customerEmail, subject, text)
		if err != nil {
			return nil, mailError(err)
		}
	case "whatsapp":
		// Same rule as email: send first, write the row after.
		sentID, err = whatsapp.SendAs(ctx, a, s.WorkspaceID, customerPhone, text, whatsapp.KindReply)
		if err != nil {
			return nil, whatsappError(err)
		}
	case "telegram":
		sentID, err = channels.SendTelegramReply(ctx, a, s.WorkspaceID, sourceChannelID, externalThreadID, text)
		if err != nil {
			return nil, channelError(err)
		}
	case "slack":
		sentID, err = channels.SendSlackReply(ctx, a, s.WorkspaceID, sourceChannelID, externalThreadID, text)
		if err != nil {
			return nil, channelError(err)
		}
	case "discord":
		sentID, err = channels.SendDiscordReply(ctx, a, s.WorkspaceID, sourceChannelID, externalThreadID, text)
		if err != nil {
			return nil, channelError(err)
		}
	}
	return recordOutbound(ctx, a, s.WorkspaceID, s.UserID, s.Email, id, mailboxID.String, text, sentID)
}

// recordOutbound writes the team's message to the thread after it has gone
// out, and moves the conversation on: read, and reopened (with no close time)
// if it was closed.
func recordOutbound(ctx context.Context, a *app.App, wsID, userID, email, convID, mailboxID, text, sentID string) (*MessageOut, error) {
	var msgID string
	var createdAt time.Time
	// sentID is the Message-ID this reply went out with: storing it is what
	// lets the customer's answer thread back onto this conversation. The reply
	// is Markdown (MH-556): body_html is its rendering, so the thread shows the
	// same formatting on every channel, not just the one that was actually sent
	// multipart (email).
	if err := a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO messages (workspace_id, conversation_id, direction, author_user_id, body, body_html, mailbox_id, external_id)
		VALUES ($1, $2, 'out', $3, $4, $5, $6, $7) RETURNING id, created_at`,
		wsID, convID, userID, text, RenderMarkdown(text), nullable(mailboxID), sentID,
	).Scan(&msgID, &createdAt); err != nil {
		return nil, huma.Error500InternalServerError("could not send the reply")
	}
	// A reply is the team's answer: the thread stops being unread, and it
	// reopens a closed conversation rather than replying into a closed one.
	_, _ = a.SQLDB.ExecContext(ctx, `
		UPDATE conversations SET last_message_at = now(), unread = false, updated_at = now(),
		  status = CASE WHEN status = 'closed' THEN 'open' ELSE status END,
		  closed_at = NULL,
		  -- The visitor has a reply they have not seen (MH-382): the widget's
		  -- launcher badge. Chat only; email and WhatsApp have their own apps.
		  unread_visitor = unread_visitor + CASE WHEN channel = 'chat' THEN 1 ELSE 0 END
		WHERE workspace_id = $1 AND id = $2`, wsID, convID)
	// The visitor's open widget hears it at once rather than on its next poll
	// (MH-382). Only chat visitors ever subscribe, so other channels no-op.
	visitors.publish(convID, "message")
	// Two members can have the same thread open; the one who did not type
	// should see the reply appear. It names its author, so the author's own
	// tab is never alerted by it (replies never alert anyone).
	changed(ctx, a, wsID, realtime.Event{Kind: "message", Direction: "out", ConversationID: convID, AuthorUserID: userID})
	uid := userID
	return &MessageOut{ID: msgID, Direction: "out", AuthorUserID: &uid, AuthorEmail: email, Body: text, CreatedAt: createdAt}, nil
}

// UpdateConversation assigns, closes, reopens, snoozes or retitles a
// conversation, or links it to a CRM contact. Snoozing with a time wakes it
// then; status snoozed with no time snoozes it until the customer writes
// again; any other status clears the time, and closing records closed_at.
func UpdateConversation(ctx context.Context, a *app.App, id string, p ConversationPatch) (*ConversationOut, error) {
	s, err := scope(ctx, authz.InboxWrite)
	if err != nil {
		return nil, err
	}
	u := &update{}
	status := ""
	if p.Status != nil {
		if !statuses[*p.Status] {
			return nil, huma.Error422UnprocessableEntity("status must be open, snoozed or closed")
		}
		status = *p.Status
	}
	if p.SnoozedUntil != nil {
		raw := strings.TrimSpace(*p.SnoozedUntil)
		if raw == "" {
			// Waking it: back to open, unless the same request says otherwise.
			if status == "" || status == "snoozed" {
				status = "open"
			}
		} else {
			until, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("snoozed_until must be an RFC 3339 time")
			}
			if !until.After(time.Now()) {
				return nil, huma.Error422UnprocessableEntity("snoozed_until must be in the future")
			}
			if until.After(time.Now().AddDate(1, 0, 0)) {
				return nil, huma.Error422UnprocessableEntity("a conversation can be snoozed for a year at most")
			}
			if status != "" && status != "snoozed" {
				return nil, huma.Error422UnprocessableEntity("snoozed_until only goes with status snoozed")
			}
			status = "snoozed"
			u.set("snoozed_until", until.UTC())
		}
	}
	if status != "" {
		u.set("status", status)
		if status != "snoozed" {
			u.sets = append(u.sets, "snoozed_until = NULL")
		} else if p.SnoozedUntil == nil || strings.TrimSpace(*p.SnoozedUntil) == "" {
			// Snoozed with no time: until the customer writes again.
			u.sets = append(u.sets, "snoozed_until = NULL")
		}
		// When it was closed is what the stats' time to resolution reads. A
		// close of an already-closed thread keeps the first time.
		if status == "closed" {
			u.sets = append(u.sets, "closed_at = CASE WHEN status = 'closed' AND closed_at IS NOT NULL THEN closed_at ELSE now() END")
		} else {
			u.sets = append(u.sets, "closed_at = NULL")
		}
	}
	if p.AssigneeUserID != nil {
		if err := checkMember(ctx, a.SQLDB, s.WorkspaceID, *p.AssigneeUserID); err != nil {
			return nil, err
		}
		u.set("assignee_user_id", nullable(*p.AssigneeUserID))
	}
	if p.ContactID != nil {
		u.set("contact_id", nullable(*p.ContactID))
		// A member's choice, linking or unlinking, is final: the automatic
		// match on the next email or WhatsApp message leaves it alone.
		u.sets = append(u.sets, "contact_link = 'manual'")
	}
	if p.Subject != nil {
		u.set("subject", strings.TrimSpace(*p.Subject))
	}
	if p.Unread != nil {
		u.set("unread", *p.Unread)
	}
	if len(u.sets) > 0 {
		res, err := a.SQLDB.ExecContext(ctx, `UPDATE conversations SET `+strings.Join(u.sets, ", ")+`, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
			append([]any{s.WorkspaceID, id}, u.args...)...)
		if err != nil {
			if pgCode(err) == "23503" {
				return nil, huma.Error422UnprocessableEntity("that contact or member is not in this workspace")
			}
			return nil, huma.Error500InternalServerError("could not update the conversation")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("conversation not found")
		}
		changed(ctx, a, s.WorkspaceID, realtime.Event{Kind: "conversation", ConversationID: id, AuthorUserID: s.UserID})
		if status != "" {
			// Closed or reopened: the visitor's widget shows the note (MH-382).
			visitors.publish(id, "status")
		}
		if p.ContactID != nil && *p.ContactID != "" {
			// MH-583: a member picking an existing contact for this
			// conversation (the inbox "link to existing contact" action)
			// should not have to also open the contact and add the identity
			// by hand — attach the sender's identity for THIS conversation's
			// channel too, the same identity a later message on that channel
			// would otherwise need autoLink or FileChannelMessage to guess at.
			linkConversationSenderIdentity(ctx, a.SQLDB, s.WorkspaceID, id, *p.ContactID)
		}
	}
	c, err := readConversation(ctx, a.SQLDB, s.WorkspaceID, id)
	if notFound(err) {
		return nil, huma.Error404NotFound("conversation not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the conversation")
	}
	return &c, nil
}

// identityChannelFor maps an inbox channel to the contact_identities enum
// (166_contact_identities.sql): every inbox channel name is already a valid
// identity channel except "facebook", whose messaging surface is Messenger,
// and "chat" (the embeddable widget), which has no stable cross-conversation
// identity to attach.
func identityChannelFor(channel string) string {
	switch channel {
	case "facebook":
		return "messenger"
	case "chat":
		return ""
	default:
		return channel
	}
}

// linkConversationSenderIdentity attaches this conversation's sender to
// contactID via contacts.LinkIdentity (MH-583), so a member using the inbox's
// "link to existing contact" action does not also have to open the contact
// and add the identity by hand. Email and WhatsApp key on the address/number
// already shown in the UI; Telegram, Slack, Discord and the browser-workflow
// channels key on external_thread_id — the platform id FileChannelMessage
// itself writes identities against — with the handle only as a label.
// Best-effort: the conversation is already linked by the caller's UPDATE, so
// a failure here (e.g. that identity already belongs to a DIFFERENT contact)
// must not undo it or fail the request.
func linkConversationSenderIdentity(ctx context.Context, db *sql.DB, wsID, convID, contactID string) {
	var channel, email, phone, handle, threadID string
	if err := db.QueryRowContext(ctx, `
		SELECT channel, customer_email, customer_phone, customer_handle, external_thread_id
		FROM conversations WHERE workspace_id = $1 AND id = $2`,
		wsID, convID).Scan(&channel, &email, &phone, &handle, &threadID); err != nil {
		return
	}

	var identChannel, externalID, label string
	switch channel {
	case "email":
		identChannel, externalID = contacts.ChannelEmail, email
	case "whatsapp":
		identChannel, externalID = contacts.ChannelWhatsApp, phone
	default:
		identChannel = identityChannelFor(channel)
		externalID = strings.TrimSpace(threadID)
		if externalID == "" {
			externalID = strings.TrimSpace(handle)
		}
		label = handle
	}
	if identChannel == "" || strings.TrimSpace(externalID) == "" {
		return
	}
	_, _ = contacts.LinkIdentity(ctx, db, wsID, contactID, identChannel, externalID, label, false)
}

// ---- the CRM side of a conversation (MH-379) --------------------------------------

// NewContactInput is a contact made from a conversation. MH-561: a WhatsApp
// thread's visitor usually has a phone and no email, so at least one of Email
// or Phone is required, not Email alone — the UI prefills whichever the
// conversation already has (guessVisitor).
type NewContactInput struct {
	Email  string `json:"email,omitempty" maxLength:"254"`
	Name   string `json:"name,omitempty" maxLength:"120"`
	Phone  string `json:"phone,omitempty" maxLength:"40"`
	Locale string `json:"locale,omitempty" enum:"en,ar"`
}

// CreateContactFromConversation creates a CRM contact from a conversation and
// links it. Deduped on whichever identity was given — email if present,
// otherwise phone — so an address or number the workspace already has is
// linked rather than duplicated, and only its blank fields are filled in. A
// company that already has the email's domain is attached; none is created.
func CreateContactFromConversation(ctx context.Context, a *app.App, id string, in NewContactInput) (*contactResult, error) {
	wsID, userID, err := crmScope(ctx)
	if err != nil {
		return nil, err
	}
	db := a.SQLDB
	email := ""
	if raw := strings.TrimSpace(in.Email); raw != "" {
		addr, err := mail.ParseAddress(raw)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("that is not a valid email address")
		}
		email = strings.ToLower(addr.Address)
	}
	phone, err := marketing.NormalizePhone(in.Phone)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	if email == "" && phone == "" {
		return nil, huma.Error422UnprocessableEntity("give an email or a phone number")
	}
	locale := "en"
	if in.Locale == "ar" {
		locale = "ar"
	}
	// The contact resolves through the shared internal/contacts.Upsert
	// (MH-583) — matching ANY of the workspace's existing identities, not
	// just the one priority key this used to check, and merging if the
	// conversation's email and phone happen to belong to two different
	// contacts already. It runs its own transaction, so it happens before the
	// conversation-locking one below.
	cres, err := contacts.Upsert(ctx, db, wsID, contacts.Candidate{
		Email: email, Phone: phone, Name: strings.TrimSpace(in.Name), Locale: locale, Source: "inbox",
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("could not create the contact")
	}
	res := &contactResult{ContactID: cres.ContactID, ContactCreated: cres.Created}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not create the contact")
	}
	defer tx.Rollback() //nolint:errcheck
	var exists string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM conversations WHERE workspace_id = $1 AND id = $2 FOR UPDATE`, wsID, id).Scan(&exists); err != nil {
		if notFound(err) {
			return nil, huma.Error404NotFound("conversation not found")
		}
		return nil, huma.Error500InternalServerError("could not read the conversation")
	}
	// A company the workspace already keeps under this domain; never a new one.
	if at := strings.LastIndex(email, "@"); at > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE contacts SET company_id = (SELECT id FROM companies WHERE workspace_id = $1 AND lower(domain) = $3 LIMIT 1), updated_at = now()
			WHERE workspace_id = $1 AND id = $2 AND company_id IS NULL
			  AND EXISTS (SELECT 1 FROM companies WHERE workspace_id = $1 AND lower(domain) = $3)`,
			wsID, res.ContactID, email[at+1:]); err != nil {
			return nil, huma.Error500InternalServerError("could not create the contact")
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE conversations SET contact_id = $3, contact_link = 'manual', updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, wsID, id, res.ContactID); err != nil {
		return nil, huma.Error500InternalServerError("could not link the contact")
	}
	if err := tx.Commit(); err != nil {
		return nil, huma.Error500InternalServerError("could not create the contact")
	}
	if res.Conversation, err = readConversation(ctx, db, wsID, id); err != nil {
		return nil, huma.Error500InternalServerError("could not read the conversation")
	}
	changed(ctx, a, wsID, realtime.Event{Kind: "conversation", ConversationID: id, AuthorUserID: userID})
	return res, nil
}

// NewDealInput is a deal opened from a conversation.
type NewDealInput struct {
	Title      string `json:"title,omitempty" maxLength:"160" doc:"Defaults to who wrote and on which channel."`
	StageID    string `json:"stage_id,omitempty" doc:"Defaults to the pipeline's first open stage."`
	ValueMinor int64  `json:"value_minor,omitempty" minimum:"0" doc:"In the currency's minor unit."`
	Currency   string `json:"currency,omitempty" pattern:"^[A-Z]{3}$" doc:"Defaults to the workspace's."`
}

// OpenDealFromConversation opens a deal from a conversation, with the
// transcript as its first activity. The deal goes on the linked contact and
// their company when there is one. One deal per conversation: a second attempt
// answers 409 naming the first.
func OpenDealFromConversation(ctx context.Context, a *app.App, id string, in NewDealInput) (*dealResult, error) {
	wsID, userID, err := crmScope(ctx)
	if err != nil {
		return nil, err
	}
	db := a.SQLDB
	if in.ValueMinor < 0 {
		return nil, huma.Error422UnprocessableEntity("the value must be 0 or more")
	}
	if in.Currency != "" {
		in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
		if len(in.Currency) != 3 {
			return nil, huma.Error422UnprocessableEntity("the currency is a 3-letter code such as USD")
		}
	}
	// Outside the transaction: EnsureStages takes its own lock and commits.
	if err := crm.EnsureStages(ctx, db, wsID); err != nil {
		return nil, huma.Error500InternalServerError("could not read the pipeline")
	}
	c, err := readConversation(ctx, db, wsID, id)
	if notFound(err) {
		return nil, huma.Error404NotFound("conversation not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the conversation")
	}
	msgs, err := threadFromStart(ctx, db, wsID, id)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the thread")
	}
	if len(msgs) == 0 {
		return nil, huma.Error422UnprocessableEntity("this conversation has no messages to attach")
	}
	var wsLocale, currency, zone string
	if err := db.QueryRowContext(ctx, `SELECT locale, currency, timezone FROM workspaces WHERE id = $1`, wsID).Scan(&wsLocale, &currency, &zone); err != nil {
		return nil, huma.Error500InternalServerError("could not read the workspace")
	}
	if in.Currency != "" {
		currency = in.Currency
	}
	loc, err := time.LoadLocation(strings.TrimSpace(zone))
	if err != nil || strings.TrimSpace(zone) == "" {
		loc = time.UTC
	}
	lang := "en"
	if wsLocale == "ar" {
		lang = "ar"
	}
	transcript := Transcript(c, msgs, memberEmails(ctx, a, wsID), lang, loc)

	title := strings.TrimSpace(in.Title)
	if title == "" {
		who := firstNonEmpty(strings.TrimSpace(c.CustomerName), c.CustomerEmail, c.CustomerPhone)
		if c.Contact != nil {
			who = firstNonEmpty(c.Contact.Name, c.Contact.Email)
		}
		if who == "" {
			who = map[bool]string{false: "Visitor", true: "زائر"}[lang == "ar"]
		}
		label := map[string][2]string{"chat": {"chat", "دردشة"}, "email": {"email", "بريد إلكتروني"}, "whatsapp": {"WhatsApp", "واتساب"}}[c.Channel]
		if lang == "ar" {
			title = who + " — " + label[1]
		} else {
			title = who + " — " + label[0]
		}
	}
	if r := []rune(title); len(r) > 160 {
		title = string(r[:160])
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not open the deal")
	}
	defer tx.Rollback() //nolint:errcheck
	// FOR UPDATE: two members clicking at once open one deal, not two.
	var existing, contactID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT deal_id, contact_id FROM conversations WHERE workspace_id = $1 AND id = $2 FOR UPDATE`,
		wsID, id).Scan(&existing, &contactID); err != nil {
		if notFound(err) {
			return nil, huma.Error404NotFound("conversation not found")
		}
		return nil, huma.Error500InternalServerError("could not read the conversation")
	}
	if existing.Valid {
		return nil, huma.Error409Conflict("this conversation already opened a deal: " + existing.String)
	}
	var companyID sql.NullString
	if contactID.Valid {
		if err := tx.QueryRowContext(ctx, `SELECT company_id FROM contacts WHERE workspace_id = $1 AND id = $2`, wsID, contactID.String).Scan(&companyID); err != nil && !notFound(err) {
			return nil, huma.Error500InternalServerError("could not read the contact")
		}
	}
	stageID := strings.TrimSpace(in.StageID)
	if stageID == "" {
		if err := tx.QueryRowContext(ctx, `SELECT id FROM pipeline_stages WHERE workspace_id = $1 AND kind = 'open'
			ORDER BY position, created_at LIMIT 1`, wsID).Scan(&stageID); err != nil {
			return nil, huma.Error422UnprocessableEntity("the pipeline has no open stage to put a deal in")
		}
	}
	var stageKind string
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM pipeline_stages WHERE workspace_id = $1 AND id = $2`, wsID, stageID).Scan(&stageKind); err != nil {
		if notFound(err) {
			return nil, huma.Error422UnprocessableEntity("that stage is not in this workspace's pipeline")
		}
		return nil, huma.Error500InternalServerError("could not read the pipeline")
	}
	res := &dealResult{}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO deals (workspace_id, title, value_minor, currency, stage_id, position, contact_id, company_id, owner_user_id, created_by, closed_at)
		VALUES ($1, $2, $3, $4, $5, (SELECT coalesce(max(position), 0) + 1 FROM deals WHERE workspace_id = $1 AND stage_id = $5),
		        $6, $7, $8, $8, CASE WHEN $9 <> 'open' THEN now() END)
		RETURNING id`,
		wsID, title, in.ValueMinor, currency, stageID, contactID, companyID, userID, stageKind,
	).Scan(&res.DealID); err != nil {
		return nil, huma.Error500InternalServerError("could not open the deal")
	}
	// The transcript is the deal's first activity, dated when the customer
	// first wrote rather than when the deal was opened.
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO activities (workspace_id, kind, body, contact_id, company_id, deal_id, created_by, created_at)
		VALUES ($1, 'note', $2, $3, $4, $5, $6, $7)
		RETURNING id`,
		wsID, transcript, contactID, companyID, res.DealID, userID, msgs[0].CreatedAt,
	).Scan(&res.ActivityID); err != nil {
		return nil, huma.Error500InternalServerError("could not attach the transcript")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET deal_id = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
		wsID, id, res.DealID); err != nil {
		return nil, huma.Error500InternalServerError("could not link the deal")
	}
	if err := tx.Commit(); err != nil {
		return nil, huma.Error500InternalServerError("could not open the deal")
	}
	if res.Conversation, err = readConversation(ctx, db, wsID, id); err != nil {
		return nil, huma.Error500InternalServerError("could not read the conversation")
	}
	changed(ctx, a, wsID, realtime.Event{Kind: "conversation", ConversationID: id, AuthorUserID: userID})
	return res, nil
}

// ---- canned replies and stats (MH-383) ------------------------------------------

// ListCannedReplies is the workspace's canned replies, in picker order.
func ListCannedReplies(ctx context.Context, a *app.App) ([]CannedReplyOut, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	rows, err := a.SQLDB.QueryContext(ctx, cannedSelect+` ORDER BY position, lower(title), created_at LIMIT 500`, s.WorkspaceID)
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
	return list, nil
}

// InboxStatsFor is the conversations per channel, first-response time and
// resolution over the last `days` days (default 30).
func InboxStatsFor(ctx context.Context, a *app.App, days int) (*InboxStats, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	if days == 0 {
		days = 30
	}
	if days < 1 || days > 365 {
		return nil, huma.Error422UnprocessableEntity("days must be between 1 and 365")
	}
	wakeDue(ctx, a, s.WorkspaceID)
	st, err := Stats(ctx, a.SQLDB, s.WorkspaceID, days, time.Now())
	if err != nil {
		return nil, huma.Error500InternalServerError("could not compute the inbox stats")
	}
	return st, nil
}

// likeEscape makes user text literal inside an ILIKE pattern.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
