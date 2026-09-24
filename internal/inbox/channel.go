package inbox

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// channelError turns a Telegram/Slack/Discord send failure into a status the
// UI can explain — the same role whatsappError and mailError play for their
// own channels.
func channelError(err error) error {
	return huma.Error502BadGateway("the reply could not be sent: " + err.Error())
}

// FileChannelMessage puts one Telegram, Slack, Discord or workflow-channel
// message on the right conversation. It is the generalisation of
// FileWhatsApp: one conversation per (channel, connection, thread), the way
// WhatsApp keys on (channel, phone) — several connections of one kind never
// collapse into each other, because sourceChannelID is part of the identity.
//
// Wired into internal/channels at boot via SetFiler, exactly as FileWhatsApp
// is wired into internal/whatsapp: neither package imports the other.
func FileChannelMessage(ctx context.Context, a *app.App, workspaceID, channel, sourceChannelID, threadID, handle, name, text, externalID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || strings.TrimSpace(text) == "" {
		return nil
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	db := a.SQLDB

	// A redelivery (Telegram/Discord retries, a Slack retry header) is the
	// same row.
	if externalID != "" {
		var exists string
		err := db.QueryRowContext(sctx, `
			SELECT id FROM messages WHERE workspace_id = $1 AND external_id = $2`,
			workspaceID, externalID).Scan(&exists)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}

	var convID string
	err := db.QueryRowContext(sctx, `
		SELECT id FROM conversations
		WHERE workspace_id = $1 AND channel = $2 AND coalesce(source_channel_id, '') = $3 AND external_thread_id = $4`,
		workspaceID, channel, sourceChannelID, threadID).Scan(&convID)
	if errors.Is(err, sql.ErrNoRows) {
		if err := db.QueryRowContext(sctx, `
			INSERT INTO conversations (workspace_id, channel, source_channel_id, external_thread_id, customer_name, customer_handle, subject, last_message_at, unread)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now(), true) RETURNING id`,
			workspaceID, channel, sourceChannelID, threadID, trimTo(name, 120), trimTo(handle, 120), trimTo(text, 80),
		).Scan(&convID); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	ins, err := db.ExecContext(sctx, `
		INSERT INTO messages (workspace_id, conversation_id, direction, body, external_id)
		VALUES ($1, $2, 'in', $3, $4)
		ON CONFLICT (workspace_id, external_id) WHERE external_id <> '' DO NOTHING`,
		workspaceID, convID, trimTo(text, maxBody), externalID)
	if err != nil {
		return err
	}
	if n, _ := ins.RowsAffected(); n == 0 {
		// A redelivery that raced the check above wrote nothing; alert nobody.
		return nil
	}

	if _, err := db.ExecContext(sctx, `
		UPDATE conversations SET last_message_at = now(), unread = true, updated_at = now(),
		  customer_name = CASE WHEN customer_name = '' THEN $3 ELSE customer_name END,
		  customer_handle = CASE WHEN customer_handle = '' THEN $4 ELSE customer_handle END, `+reopenOnInbound+`
		WHERE workspace_id = $1 AND id = $2`, workspaceID, convID, trimTo(name, 120), trimTo(handle, 120)); err != nil {
		return err
	}

	announceInbound(ctx, a, workspaceID, convID, text, time.Time{})
	return nil
}
