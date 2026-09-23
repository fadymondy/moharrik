package notifications

import (
	"context"
	"database/sql"
	"strings"
)

/*
Emit puts an item in a workspace's notification center from inside Moharrik
itself rather than from a webhook — the first user is a new lead from a
website inquiry form (MH-390), so the owner sees it until MH-378's alerts land.

The item has no webhook source (source_id NULL); SourceName is the label the
feed shows instead. ExternalID dedupes: emitting the same id twice keeps one
row, the same rule a webhook redelivery follows — but only within source_id
NULL, so an internal id can never collide with a webhook's.

db must be the APP pool and ctx must carry the workspace (tenant.WithWorkspace):
the insert runs under RLS like every other tenant write, and names workspace_id
as well.
*/

// Internal is an item Moharrik raises about itself.
type Internal struct {
	SourceName string
	ExternalID string
	Kind       string
	Severity   string
	Title      string
	Body       string
	Actor      string
	URL        string
}

// maxEmitBody keeps a pasted essay from becoming a wall in the feed; the full
// text lives on the record the item links to.
const maxEmitBody = 500

func Emit(ctx context.Context, db *sql.DB, workspaceID string, it Internal) error {
	body := strings.TrimSpace(it.Body)
	if r := []rune(body); len(r) > maxEmitBody {
		body = string(r[:maxEmitBody]) + "…"
	}
	// The identity index (17_notifications.sql) is on (workspace_id, source_id,
	// external_id); NULL source ids never conflict with each other in it, so
	// the internal dedupe is an explicit NOT EXISTS.
	_, err := db.ExecContext(ctx, `
		INSERT INTO notification_items (workspace_id, source_id, source_name, external_id, kind, severity, title, body, actor, url, payload, occurred_at)
		SELECT $1, NULL, $2, $3, $4, $5, $6, $7, $8, $9, '{}', now()
		WHERE $3 = '' OR NOT EXISTS (
			SELECT 1 FROM notification_items WHERE workspace_id = $1 AND source_id IS NULL AND external_id = $3)`,
		workspaceID, it.SourceName, it.ExternalID, it.Kind, clampSeverity(it.Severity),
		strings.TrimSpace(it.Title), body, it.Actor, it.URL)
	return err
}
