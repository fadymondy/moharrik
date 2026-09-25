package mcp

import (
	"context"
	"encoding/json"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/googlecontacts"
)

// GoogleContactsTools (MH-585): google_contacts_sync — the manual "Sync now"
// action as a tool, so an agent can trigger the same sync the web UI's
// button does. import_contacts_preview/commit{source:"google"} (see
// user_tools_vcard.go) cover preview and the equivalent commit; this is
// kept as its own tool because "sync a connection" is a distinct verb from
// "import a card" and returns sync-specific counts (added/updated/flagged
// deleted/full_resync) rather than a generic commit result.
func GoogleContactsTools() []Tool {
	return []Tool{
		{
			Name: "google_contacts_sync",
			Description: "Sync now: pull every contact/change from a connected Google account's People API into this workspace's contacts. " +
				"Incremental once it has synced before (only what changed since last time); automatically falls back to a full resync if " +
				"Google reports the stored sync token expired. A contact Google reports deleted is flagged (google_deleted_at), never removed.",
			Scope: "contacts:write",
			InputSchema: schema([]string{"connection_id"}, map[string]any{
				"connection_id": prop("string", descGoogleConnID),
				"full":          prop("boolean", descFullResync),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				m := decodeArgs(raw)
				connID, err := required("connection_id", str(m, "connection_id"))
				if err != nil {
					return "", err
				}
				full, _ := m["full"].(bool)
				res, err := googlecontacts.Sync(ctx, a, s.WorkspaceID, connID, s.UserID, full)
				if err != nil {
					return "", err
				}
				return jsonResult(res)
			},
		},
	}
}
