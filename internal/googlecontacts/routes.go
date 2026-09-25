// Package googlecontacts's REST routes: "Sync now" and the sync status the
// UI's "Import from Google" panel polls. Gating matches internal/vcard's
// import routes (any workspace member, not just an admin) since this is the
// same "import contacts" action, just sourced from a connected Google
// account instead of an uploaded file (MH-585).
package googlecontacts

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/vcard"
)

var restTags = []string{"Contacts"}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

func wsID(ctx context.Context) string {
	if s, ok := tenant.From(ctx); ok {
		return s.WorkspaceID
	}
	return ""
}

func userID(ctx context.Context) string {
	if s, ok := tenant.From(ctx); ok {
		return s.UserID
	}
	return ""
}

func mapErr(err error) error {
	switch {
	case errors.Is(err, google.ErrNotConnected):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, google.ErrScope):
		return huma.Error403Forbidden(err.Error())
	default:
		return huma.Error502BadGateway("Google Contacts sync failed: " + err.Error())
	}
}

// Register mounts the Google Contacts sync REST routes. `a` may be nil
// during OpenAPI export.
func Register(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-google-contacts-connections", Method: http.MethodGet, Path: "/api/w/google/contacts/connections",
		Summary: "Connected Google accounts that have granted Contacts, for the Import from Google panel (any workspace member)", Tags: restTags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[ContactsConnectionsOut], error) {
		list, err := ListConnections(ctx, a, wsID(ctx))
		if err != nil {
			return nil, mapErr(err)
		}
		return &body[ContactsConnectionsOut]{ContactsConnectionsOut{Connections: list}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-google-contacts-preview", Method: http.MethodGet, Path: "/api/w/google/contacts/{connection_id}/preview",
		Summary: "Preview what syncing this Google connection's contacts would do, without writing anything", Tags: restTags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ConnectionID string `path:"connection_id"`
	}) (*body[vcard.VcardPreview], error) {
		pv, err := Preview(ctx, a, wsID(ctx), in.ConnectionID)
		if err != nil {
			return nil, mapErr(err)
		}
		return &body[vcard.VcardPreview]{*pv}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-google-contacts-sync", Method: http.MethodPost, Path: "/api/w/google/contacts/{connection_id}/sync",
		Summary: "Sync now: import every contact/change from this Google connection (incremental once it has synced before)", Tags: restTags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ConnectionID string `path:"connection_id"`
	}) (*body[SyncResult], error) {
		res, err := Sync(ctx, a, wsID(ctx), in.ConnectionID, userID(ctx))
		if err != nil {
			return nil, mapErr(err)
		}
		return &body[SyncResult]{*res}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-google-contacts-status", Method: http.MethodGet, Path: "/api/w/google/contacts/{connection_id}/status",
		Summary: "Last sync time/error for this Google connection, for the Import from Google panel", Tags: restTags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ConnectionID string `path:"connection_id"`
	}) (*body[SyncStatus], error) {
		st, err := Status(ctx, a, wsID(ctx), in.ConnectionID)
		if err != nil {
			return nil, mapErr(err)
		}
		return &body[SyncStatus]{*st}, nil
	})
}
