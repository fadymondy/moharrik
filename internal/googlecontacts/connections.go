package googlecontacts

import (
	"context"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
)

// ContactsConnection is a connected Google account, trimmed to what the
// "Import from Google" panel needs. Unlike google.Connection
// (internal/google/connections.go, gated to integrations:manage), this is
// what ListConnections returns to ANY workspace member — same gating as
// Preview/Sync/Status below — so it deliberately carries no scopes list or
// connected-by identity, only what a non-admin member needs to pick a
// connection and read its health.
type ContactsConnection struct {
	ID string `json:"id"`
	// Email is who the address book belongs to.
	Email string `json:"email"`
	// "" or "reauth" (google.Connection.Status): the stored grant is stale
	// and needs an admin to reconnect it.
	Status string `json:"status"`
}

// ContactsConnectionsOut is the "Import from Google" panel's connections list.
type ContactsConnectionsOut struct {
	Connections []ContactsConnection `json:"connections"`
}

// ListConnections is this workspace's Google connections that have granted
// the Contacts product — the accounts the "Import from Google" panel can
// sync. Any workspace member may call this (unlike
// GET /api/w/google/connections, which needs integrations:manage): a member
// without admin still needs to see and sync an account an admin already
// connected, just not to connect or disconnect one themselves.
func ListConnections(ctx context.Context, a *app.App, wsID string) ([]ContactsConnection, error) {
	all, err := google.List(ctx, a, wsID)
	if err != nil {
		return nil, err
	}
	out := []ContactsConnection{}
	for _, c := range all {
		hasContacts := false
		for _, p := range c.Products {
			if p == google.Contacts {
				hasContacts = true
				break
			}
		}
		if !hasContacts {
			continue
		}
		out = append(out, ContactsConnection{ID: c.ID, Email: c.Email, Status: c.Status})
	}
	return out, nil
}
