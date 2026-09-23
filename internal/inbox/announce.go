package inbox

import (
	"context"
	"database/sql"
	"time"

	"github.com/fadymondy/moharrik/internal/alerts"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// staleInbound is how old an inbound message may be and still alert. Mail is
// the case: the first poll of a newly connected mailbox files weeks of old
// mail, and forty chimes for last month's messages would teach a member to
// mute the chime. Older messages still update the inbox and the bell's count.
const staleInbound = 24 * time.Hour

// announceInbound tells the workspace a customer has written (MH-378): the open
// tabs (bell, popup, chime, desktop notification) and, through web push, the
// devices of the members it alerts. Every channel's inbound path calls this —
// the chat widget, email and WhatsApp — so none of them can be the one that
// stays silent.
//
// Audience (realtime.Event.For): members who may read the inbox; when the
// conversation is assigned, only the assignee is alerted and everyone else
// just sees the count move.
func announceInbound(ctx context.Context, a *app.App, workspaceID, convID, text string, sentAt time.Time) {
	if a == nil || a.SQLDB == nil {
		return
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var channel, name, email, phone string
	var assignee sql.NullString
	if err := a.SQLDB.QueryRowContext(sctx, `
		SELECT channel, customer_name, customer_email, coalesce(customer_phone, ''), assignee_user_id
		FROM conversations WHERE workspace_id = $1 AND id = $2`, workspaceID, convID,
	).Scan(&channel, &name, &email, &phone, &assignee); err != nil {
		// The message is saved; failing to read it back for the alert must not
		// undo that. The tabs still get a quiet refresh.
		name = ""
	}
	sender := firstNonEmpty(name, email, phone)
	ev := realtime.Event{
		Kind: "message", Direction: "in", ConversationID: convID, Channel: channel,
		Sender: trim(sender, 80), Snippet: trim(text, 160),
		Permission: authz.InboxRead,
		Alerting:   sentAt.IsZero() || time.Since(sentAt) < staleInbound,
	}
	if assignee.Valid && assignee.String != "" {
		ev.AlertOnly = assignee.String
	}
	alerts.Emit(ctx, a, workspaceID, ev)
}

// changed tells the open tabs a conversation's state moved (read, assigned,
// closed, replied to) so the bell and the inbox list stay in step. Never alerts.
func changed(ctx context.Context, a *app.App, workspaceID string, ev realtime.Event) {
	ev.Permission = authz.InboxRead
	ev.Alerting = false
	alerts.Emit(ctx, a, workspaceID, ev)
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
