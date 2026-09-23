package inbox

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/whatsapp"

	"github.com/danielgtaylor/huma/v2"
)

// FileWhatsApp puts one WhatsApp message on the right conversation.
//
// One conversation per number per workspace, so a customer who writes again
// next week continues the thread they already had — there is no Message-ID to
// thread on, and a phone number is a stable identity in a way an email subject
// is not.
//
// It is wired into internal/whatsapp at boot rather than imported there, so
// neither package depends on the other.
func FileWhatsApp(ctx context.Context, a *app.App, workspaceID, phone, name, text, externalID string) error {
	phone = strings.TrimSpace(phone)
	if phone == "" || strings.TrimSpace(text) == "" {
		return nil
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	db := a.SQLDB

	// A redelivery is the same row. Evolution retries on any non-2xx, so this
	// is the ordinary case rather than an edge one.
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
		WHERE workspace_id = $1 AND channel = 'whatsapp' AND customer_phone = $2`,
		workspaceID, phone).Scan(&convID)
	if errors.Is(err, sql.ErrNoRows) {
		if err := db.QueryRowContext(sctx, `
			INSERT INTO conversations (workspace_id, channel, customer_name, customer_phone, subject, last_message_at, unread)
			VALUES ($1, 'whatsapp', $2, $3, $4, now(), true) RETURNING id`,
			workspaceID, trimTo(name, 120), phone, trimTo(text, 80),
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
	// A redelivery that raced the check above wrote nothing, and alerts nobody.
	if n, _ := ins.RowsAffected(); n == 0 {
		return nil
	}

	if _, err := db.ExecContext(sctx, `
		UPDATE conversations SET last_message_at = now(), unread = true, updated_at = now(),
		  customer_name = CASE WHEN customer_name = '' THEN $3 ELSE customer_name END, `+reopenOnInbound+`
		WHERE workspace_id = $1 AND id = $2`, workspaceID, convID, trimTo(name, 120)); err != nil {
		return err
	}
	// A number the CRM already knows (the same digits, MH-320's normalisation)
	// is linked to the thread (MH-379). Best effort: the message is filed.
	if err := autoLink(sctx, db, workspaceID, convID); err != nil {
		a.Log.Warn("could not match a WhatsApp number to a contact", "workspace", workspaceID, "err", err)
	}

	// The team is told at once, the same as a chat message — a customer on
	// WhatsApp expects an answer in minutes, not on the next refresh.
	announceInbound(ctx, a, workspaceID, convID, text, time.Time{})
	return nil
}

// whatsappError turns a send failure into a status the thread can explain.
func whatsappError(err error) error {
	switch {
	case errors.Is(err, whatsapp.ErrNotConfigured):
		return huma.Error503ServiceUnavailable(err.Error())
	case errors.Is(err, whatsapp.ErrNoLink):
		return huma.Error409Conflict("this workspace has no WhatsApp number linked, so it cannot answer on WhatsApp yet")
	}
	var we *whatsapp.Error
	if errors.As(err, &we) {
		return huma.Error502BadGateway(we.Error())
	}
	return huma.Error500InternalServerError("the reply could not be sent")
}
