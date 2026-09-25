package inbox

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/contacts"
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
func FileChannelMessage(ctx context.Context, a *app.App, workspaceID, channel, sourceChannelID, threadID, handle, name, text, externalID string, isGroup bool) error {
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
			INSERT INTO conversations (workspace_id, channel, source_channel_id, external_thread_id, customer_name, customer_handle, subject, last_message_at, unread, is_group)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now(), true, $8) RETURNING id`,
			workspaceID, channel, sourceChannelID, threadID, trimTo(name, 120), trimTo(handle, 120), trimTo(text, 80), isGroup,
		).Scan(&convID); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// MH-604: a group thread (Telegram/Slack/Discord "channels" only — WhatsApp
	// groups are deliberately never filed) has many senders sharing one
	// conversation, so the CONVERSATION's own contact_id (below, for a 1:1
	// thread) can't say who wrote THIS message. Resolve the per-message
	// sender's own contact before the INSERT, so sender_contact_id/
	// sender_name/sender_external_id land on the same row as the body —
	// that's what lets the UI show a per-sender avatar beside each bubble.
	var senderContactID sql.NullString
	senderName, senderExternalID := "", ""
	if isGroup {
		senderExternalID = strings.TrimSpace(handle)
		senderName = trimTo(name, 120)
		if id := linkChannelMessageSender(sctx, db, workspaceID, channel, senderExternalID, name); id != "" {
			senderContactID = sql.NullString{String: id, Valid: true}
		}
	}

	ins, err := db.ExecContext(sctx, `
		INSERT INTO messages (workspace_id, conversation_id, direction, body, external_id, sender_contact_id, sender_name, sender_external_id)
		VALUES ($1, $2, 'in', $3, $4, $5, $6, $7)
		ON CONFLICT (workspace_id, external_id) WHERE external_id <> '' DO NOTHING`,
		workspaceID, convID, trimTo(text, maxBody), externalID, senderContactID, senderName, senderExternalID)
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

	// MH-583: resolve or create the CRM contact for this platform sender, the
	// same contacts.Upsert path email and WhatsApp already resolve through
	// (see internal/inbox/crm_link.go's autoLink), so a Telegram/Slack/Discord
	// conversation shows who it's with instead of only a raw handle. A group
	// thread has no single "the contact" for the conversation — threadID is
	// the room, not a sender — so this stays unlinked there; the resolved
	// per-message sender above is what the photo refresh and the UI use.
	var contactID string
	if isGroup {
		contactID = senderContactID.String
	} else {
		contactID = linkChannelContact(sctx, db, workspaceID, convID, channel, threadID, name)
	}

	announceInbound(ctx, a, workspaceID, convID, text, time.Time{})

	// MH-588/MH-604: cache this sender's profile photo, in the background —
	// the same "never block filing on a provider round trip" contract
	// refreshWhatsAppPhoto (whatsapp.go) already has. photoUserID is the
	// platform id a profile-photo lookup actually needs: for Telegram a
	// private chat's id IS the sender (threadID); for Slack/Discord, and for
	// ANY group thread regardless of platform, threadID is the room rather
	// than the sender, so it's handle (the sender's own user id) instead.
	photoUserID := threadID
	if channel == "slack" || channel == "discord" || isGroup {
		photoUserID = handle
	}
	queueChannelPhotoRefresh(a, workspaceID, channel, sourceChannelID, convID, contactID, photoUserID)
	return nil
}

// linkChannelMessageSender upserts a contact identity for the sender of ONE
// group-thread message — the isGroup counterpart to linkChannelContact below,
// deliberately not touching conversations.contact_id, since a group thread
// has no single contact the conversation itself belongs to. Returns "" (and
// leaves the message's sender_name/sender_external_id as the only identity)
// when there's no external id to key an upsert on, e.g. a platform that
// didn't hand back a per-sender handle for this delivery.
func linkChannelMessageSender(ctx context.Context, db *sql.DB, workspaceID, channel, externalID, name string) string {
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return ""
	}
	res, err := contacts.Upsert(ctx, db, workspaceID, contacts.Candidate{
		Name:   strings.TrimSpace(name),
		Source: "inbox",
		Identities: []contacts.Identity{
			{Channel: channel, ExternalID: externalID, IsPrimary: true},
		},
	})
	if err != nil {
		return ""
	}
	return res.ContactID
}

// linkChannelContact upserts a contact identity for a Telegram, Slack or
// Discord sender and links it to the conversation, returning the resolved
// contact id (or "" if none) so callers — the photo-fetch trigger below —
// can cache the photo on whichever row (contact when linked, conversation
// otherwise) crm.AvatarURLLite actually reads, the same choice
// refreshWhatsAppPhoto makes for WhatsApp.
//
// It runs on every inbound message, not just the first: contacts.Upsert's
// identity match is what makes a second message from the same platform id
// land on the SAME contact instead of creating a duplicate, and the UPDATE
// below never overrides a link a member set (or removed) by hand, mirroring
// autoLink's guard in crm_link.go.
func linkChannelContact(ctx context.Context, db *sql.DB, workspaceID, convID, channel, externalID, name string) string {
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return ""
	}
	res, err := contacts.Upsert(ctx, db, workspaceID, contacts.Candidate{
		Name:   strings.TrimSpace(name),
		Source: "inbox",
		Identities: []contacts.Identity{
			{Channel: channel, ExternalID: externalID, IsPrimary: true},
		},
	})
	if err != nil {
		return ""
	}
	_, _ = db.ExecContext(ctx, `
		UPDATE conversations SET contact_id = $3, contact_link = 'auto', updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND contact_id IS NULL AND contact_link <> 'manual'`,
		workspaceID, convID, res.ContactID)
	var linkedContactID string
	_ = db.QueryRowContext(ctx, `SELECT contact_id FROM conversations WHERE workspace_id = $1 AND id = $2`,
		workspaceID, convID).Scan(&linkedContactID)
	return linkedContactID
}
