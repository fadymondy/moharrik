package inbox

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// presenceOf maps Evolution/Baileys presence names onto the two states the
// UI shows; anything else (available, unavailable) clears the indicator.
func presenceOf(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "composing":
		return "composing"
	case "recording":
		return "recording"
	default:
		return "" // paused, available, unavailable — all "stopped"
	}
}

// FileWhatsAppTyping turns a WhatsApp presence update into a realtime event
// for the conversation's members (MH-660). Wired into internal/whatsapp at
// boot (whatsapp.SetTypingSink) the same way MarkStatus is.
//
// Never touches the database beyond the read needed to find the
// conversation: a typing indicator is not history, so there is nothing to
// store and nothing to file if the customer has never written before —
// unlike FileWhatsApp, this never creates a conversation.
func FileWhatsAppTyping(ctx context.Context, a *app.App, workspaceID, phone, state string) error {
	phone = strings.TrimSpace(phone)
	if phone == "" || a == nil || a.SQLDB == nil {
		return nil
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var convID string
	err := a.SQLDB.QueryRowContext(sctx, `
		SELECT id FROM conversations
		WHERE workspace_id = $1 AND channel = 'whatsapp' AND customer_phone = $2`,
		workspaceID, phone).Scan(&convID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // nobody has a thread with this number yet — nothing to show it on
	}
	if err != nil {
		return err
	}
	changed(ctx, a, workspaceID, realtime.Event{
		Kind: "typing", ConversationID: convID, Direction: "in",
		Presence: presenceOf(state),
	})
	return nil
}
