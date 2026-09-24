package whatsapp

import (
	"context"

	"github.com/fadymondy/moharrik/internal/app"
)

// KindRule is a member's WhatsApp notification an automation rule asked for
// (MH-427) — "WhatsApp the on-call member when Sentry reports an error".
const KindRule = "rule"

// QueueNotice queues one notification for one member from an automation rule.
//
// It goes into the same outbox as the member's other WhatsApp notifications
// (MH-411), so everything the member set applies: it is only for a verified
// number they have not paused, it waits out their quiet hours, it counts
// against their daily cap and it travels in a batch with whatever else is
// pending. A rule names the member deliberately, so the kinds they picked for
// the automatic feed do not filter it — pausing does.
//
// It returns "" when queued, or why not, in words for the run log.
func QueueNotice(ctx context.Context, a *app.App, wsID, userID, title, body, link, dedupe string) (string, error) {
	m, err := loadMember(ctx, a.SQLDB, wsID, userID)
	if err != nil {
		return "", err
	}
	switch {
	case m == nil || m.Phone == "":
		return "this member has not set up WhatsApp notifications", nil
	case m.VerifiedAt == nil:
		return "this member has not verified their WhatsApp number", nil
	case !m.Enabled:
		return "this member has paused WhatsApp notifications", nil
	}
	_, err = a.SQLDB.ExecContext(ctx, `
		INSERT INTO member_whatsapp_events (workspace_id, user_id, kind, title, body, link, dedupe_key, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (workspace_id, user_id, dedupe_key) DO NOTHING`,
		wsID, userID, KindRule, clip(title, 200), clip(body, 300), link, dedupe, Clock())
	return "", err
}
