package inbox

import (
	"context"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// MarkStatus records a delivery receipt (MH-656) against the outbound message
// stored under externalID, and tells the open tabs so the tick changes in
// place. Wired into internal/whatsapp at boot (whatsapp.SetStatusSink).
func MarkStatus(ctx context.Context, a *app.App, workspaceID, externalID, status string) error {
	if externalID == "" || status == "" {
		return nil
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	// Receipts can arrive out of order — a "delivered" after the "read" — so
	// a message only ever moves forward.
	var convID string
	err := a.SQLDB.QueryRowContext(sctx, `
		UPDATE messages SET status = $3::text
		WHERE workspace_id = $1 AND external_id = $2 AND direction = 'out'
		  AND (CASE $3::text WHEN 'sent' THEN 1 WHEN 'delivered' THEN 2 WHEN 'read' THEN 3 ELSE 0 END)
		    > (CASE status WHEN 'sent' THEN 1 WHEN 'delivered' THEN 2 WHEN 'read' THEN 3 ELSE 0 END)
		RETURNING conversation_id`, workspaceID, externalID, status).Scan(&convID)
	if notFound(err) {
		return nil // not ours, or already at (or past) that tick
	}
	if err != nil {
		return err
	}
	changed(ctx, a, workspaceID, realtime.Event{Kind: "message_status", ConversationID: convID})
	return nil
}
