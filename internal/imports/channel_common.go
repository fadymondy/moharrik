package imports

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
)

/*
channel_common.go holds what MH-563/564's Slack, Discord, Telegram and
workflow import providers share: the same find-or-create-by-key conversation
lookup internal/inbox/channel.go's FileChannelMessage uses for a live
delivery (and internal/channels/workflow.go's PushMessages, through the same
deliver() call), and the same dedup-on-external_id history insert
whatsapp.go's insertHistory and email.go's insertEmailHistory already have —
so a thread imported here and one filed live are always the same
conversation, never two.
*/

// channelConversation finds or creates the one conversation for
// (channel, sourceChannelID, threadID) — exactly the key FileChannelMessage
// and PushMessages key a live/pushed message on, so importing a chat's
// history lands in the same conversation a live message on it already has,
// or ever will.
func channelConversation(ctx context.Context, a *app.App, wsID, channel, sourceChannelID, threadID string, isGroup bool, name, handle string) (id string, created bool, err error) {
	err = a.SQLDB.QueryRowContext(ctx, `
		SELECT id FROM conversations
		WHERE workspace_id = $1 AND channel = $2 AND coalesce(source_channel_id, '') = $3 AND external_thread_id = $4`,
		wsID, channel, sourceChannelID, threadID).Scan(&id)
	if err == nil {
		_, uerr := a.SQLDB.ExecContext(ctx, `
			UPDATE conversations SET
			  customer_name = CASE WHEN customer_name = '' THEN $3 ELSE customer_name END,
			  customer_handle = CASE WHEN customer_handle = '' THEN $4 ELSE customer_handle END
			WHERE workspace_id = $1 AND id = $2`, wsID, id, trimText(name, 120), trimText(handle, 120))
		return id, false, uerr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	err = a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO conversations (workspace_id, channel, source_channel_id, external_thread_id, is_group, customer_name, customer_handle, subject, last_message_at, unread)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $6, now(), false)
		RETURNING id`,
		wsID, channel, sourceChannelID, threadID, isGroup, trimText(name, 120), trimText(handle, 120),
	).Scan(&id)
	if err != nil {
		// A race with a live delivery landed first: the row now exists under
		// the same key, so find it rather than fail the import.
		if selErr := a.SQLDB.QueryRowContext(ctx, `
			SELECT id FROM conversations
			WHERE workspace_id = $1 AND channel = $2 AND coalesce(source_channel_id, '') = $3 AND external_thread_id = $4`,
			wsID, channel, sourceChannelID, threadID).Scan(&id); selErr == nil {
			return id, false, nil
		}
		return "", false, err
	}
	return id, true, nil
}

func trimText(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// channelHistoryMsg is one historical message to file, source-agnostic.
type channelHistoryMsg struct {
	ExternalID string
	Text       string
	At         time.Time
}

// insertChannelHistory files a batch of past inbound messages, dedup'd on
// external_id (messages_external_uq), the same index whatsapp.go's
// insertHistory and email.go's insertEmailHistory use — so a message
// imported here and one already filed live (or filed live after) collide
// into one row instead of duplicating.
func insertChannelHistory(ctx context.Context, a *app.App, wsID, convID string, msgs []channelHistoryMsg) (int, error) {
	n := 0
	var lastAt time.Time
	for _, m := range msgs {
		if m.Text == "" || m.ExternalID == "" {
			continue
		}
		res, err := a.SQLDB.ExecContext(ctx, `
			INSERT INTO messages (workspace_id, conversation_id, direction, body, external_id, created_at)
			VALUES ($1, $2, 'in', $3, $4, $5)
			ON CONFLICT (workspace_id, external_id) WHERE external_id <> '' DO NOTHING`,
			wsID, convID, m.Text, m.ExternalID, m.At)
		if err != nil {
			return n, err
		}
		if rows, _ := res.RowsAffected(); rows > 0 {
			n++
		}
		if m.At.After(lastAt) {
			lastAt = m.At
		}
	}
	if !lastAt.IsZero() {
		if err := syncConversationTimes(ctx, a, wsID, convID); err != nil {
			return n, err
		}
	}
	return n, nil
}

// syncConversationTimes dates a conversation by its messages once history has
// been imported into it (MH-590). A thread the import creates is inserted at
// now(), so raising last_message_at with GREATEST kept the import time: a
// chat last active weeks ago sorted as brand new. The newest message is the
// last-message time, and the oldest one bounds created_at, so imported history
// is never counted as a conversation that started today.
func syncConversationTimes(ctx context.Context, a *app.App, wsID, convID string) error {
	_, err := a.SQLDB.ExecContext(ctx, `
		UPDATE conversations c
		   SET last_message_at = m.newest, created_at = LEAST(c.created_at, m.oldest), updated_at = now()
		  FROM (SELECT max(created_at) AS newest, min(created_at) AS oldest
		          FROM messages WHERE workspace_id = $1 AND conversation_id = $2) m
		 WHERE c.workspace_id = $1 AND c.id = $2 AND m.newest IS NOT NULL`, wsID, convID)
	return err
}
