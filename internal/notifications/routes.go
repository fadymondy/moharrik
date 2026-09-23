package notifications

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/alerts"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
)

var tags = []string{"Notifications"}

// publicBase is the origin the webhook URL is built on (see internal/marketing's
// identical helper — duplicated rather than shared, same as every other
// per-package convention in this codebase).
func publicBase() string {
	for _, k := range []string{"PUBLIC_URL", "APP_URL"} {
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv(k)), "/"); v != "" {
			return v
		}
	}
	return "http://localhost:3120"
}

// maxWebhookBody caps an inbound delivery — large enough for any real
// GitHub/Sentry payload, small enough that a misbehaving sender cannot fill
// the database one request at a time.
const maxWebhookBody = 256 * 1024

// Register mounts the notification-center endpoints. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	db := func() *sql.DB { return a.SQLDB }

	// ---- webhook sources -------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-webhook-sources", Method: http.MethodGet, Path: "/api/w/webhook_sources",
		Summary: "Webhook sources this workspace collects from", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[struct {
		Sources []WebhookSourceOut `json:"sources"`
	}], error) {
		list, err := ListSources(ctx, a)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Sources []WebhookSourceOut `json:"sources"`
		}{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-webhook-source", Method: http.MethodPost, Path: "/api/w/webhook_sources",
		DefaultStatus: http.StatusCreated, Summary: "Add a webhook source; its signing secret is shown once", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Name string `json:"name" minLength:"1" maxLength:"80"`
			Kind string `json:"kind,omitempty" enum:"generic,github,sentry"`
		}
	}) (*body[WebhookSourceWithSecret], error) {
		res, err := CreateSource(ctx, a, in.Body.Name, in.Body.Kind, "")
		if err != nil {
			return nil, err
		}
		return out(*res), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-webhook-source", Method: http.MethodPatch, Path: "/api/w/webhook_sources/{id}",
		Summary: "Rename, enable/disable, or move a webhook source off legacy signing", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body SourcePatch
	}) (*body[struct {
		Source WebhookSourceOut `json:"source"`
	}], error) {
		src, err := UpdateSource(ctx, a, in.ID, in.Body)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Source WebhookSourceOut `json:"source"`
		}{*src}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-rotate-webhook-source", Method: http.MethodPost, Path: "/api/w/webhook_sources/{id}/rotate",
		Summary: "Replace a webhook source's signing secret; the old one stops working immediately", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[WebhookSourceWithSecret], error) {
		res, err := RotateSource(ctx, a, in.ID)
		if err != nil {
			return nil, err
		}
		return out(*res), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-set-webhook-source-secret", Method: http.MethodPut, Path: "/api/w/webhook_sources/{id}/secret",
		Summary: "Set a webhook source's signing secret to one the sending platform generated (Sentry's Client Secret)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Secret string `json:"secret" minLength:"16" maxLength:"256" doc:"Write-only; never returned."`
		}
	}) (*body[struct {
		Source WebhookSourceOut `json:"source"`
	}], error) {
		src, err := SetSourceSecret(ctx, a, in.ID, in.Body.Secret)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Source WebhookSourceOut `json:"source"`
		}{*src}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-webhook-source", Method: http.MethodDelete, Path: "/api/w/webhook_sources/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Delete a webhook source (its past items stay, unlinked)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		s, err := scope(ctx, authz.NotificationsManage)
		if err != nil {
			return nil, err
		}
		res, err := db().ExecContext(ctx, `DELETE FROM webhook_sources WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not delete the webhook source")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("webhook source not found")
		}
		return &struct{}{}, nil
	})

	// ---- items -------------------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-notifications", Method: http.MethodGet, Path: "/api/w/notifications",
		Summary: "The notification feed, newest first, one page at a time (follow next_cursor)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Unread   bool   `query:"unread" doc:"Only items not yet marked read."`
		Saved    bool   `query:"saved" doc:"Only saved items."`
		SourceID string `query:"source_id"`
		Severity string `query:"severity" enum:",info,warning,error"`
		Limit    int    `query:"limit" minimum:"1" maximum:"500"`
		Cursor   string `query:"cursor" doc:"next_cursor from the previous page"`
	}) (*body[ItemList], error) {
		res, err := ListItems(ctx, a, ItemFilter{Unread: in.Unread, Saved: in.Saved, SourceID: in.SourceID, Severity: in.Severity, Limit: in.Limit, Cursor: in.Cursor})
		if err != nil {
			return nil, err
		}
		return out(*res), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-notification", Method: http.MethodGet, Path: "/api/w/notifications/{id}",
		Summary: "One notification item, with the raw payload it was made from", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[struct {
		Item NotificationItemOut `json:"item"`
	}], error) {
		it, err := GetItem(ctx, a, in.ID)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Item NotificationItemOut `json:"item"`
		}{*it}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-notification", Method: http.MethodPatch, Path: "/api/w/notifications/{id}",
		Summary: "Mark an item read/unread or saved/unsaved", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Read  *bool `json:"read,omitempty"`
			Saved *bool `json:"saved,omitempty"`
		}
	}) (*body[struct {
		Item NotificationItemOut `json:"item"`
	}], error) {
		it, err := UpdateItem(ctx, a, in.ID, in.Body.Read, in.Body.Saved)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Item NotificationItemOut `json:"item"`
		}{*it}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-mark-all-notifications-read", Method: http.MethodPost, Path: "/api/w/notifications/mark_all_read",
		Summary: "Mark every unread item read (does not touch saved)", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[struct {
		Marked int64 `json:"marked"`
	}], error) {
		n, err := MarkAllRead(ctx, a, "")
		if err != nil {
			return nil, err
		}
		return out(struct {
			Marked int64 `json:"marked"`
		}{n}), nil
	})

	registerPublicHook(api, a)
}

// ---- public: the webhook receiver -----------------------------------------
//
// POST /api/hooks/{id} has no session and no X-Workspace-ID: the id in the
// path plus a valid HMAC signature over the raw body ARE the authorization,
// exactly the shape internal/marketing/handlers.go uses for the mail confirm
// and unsubscribe links (an unguessable value in the URL/body, checked on the
// owner pool, then everything else scoped to the workspace it resolves to).
// It is listed in internal/server/guard.go's classify() as a public prefix.

type hookIn struct {
	ID             string `path:"id"`
	Signature      string `header:"X-Webhook-Signature" doc:"generic: [sha256=]hex HMAC-SHA256(secret, X-Webhook-Timestamp + \".\" + raw body)"`
	Timestamp      string `header:"X-Webhook-Timestamp" doc:"generic: unix seconds; must be within 300 seconds of the server's clock"`
	GithubSig      string `header:"X-Hub-Signature-256" doc:"github: sha256=hex HMAC-SHA256(secret, raw body) — sent by GitHub"`
	GithubEvent    string `header:"X-GitHub-Event" doc:"github: the event name — sent by GitHub"`
	SentrySig      string `header:"Sentry-Hook-Signature" doc:"sentry: hex HMAC-SHA256(client secret, raw body) — sent by Sentry"`
	SentryResource string `header:"Sentry-Hook-Resource" doc:"sentry: the resource (issue, event_alert, error, metric_alert) — sent by Sentry"`
	RawBody        []byte
}

// headers rebuilds the signature-relevant headers for Verify, which is written
// against http.Header so it can be tested without huma.
func (in *hookIn) headers() http.Header {
	h := http.Header{}
	set := func(k, v string) {
		if v != "" {
			h.Set(k, v)
		}
	}
	set("X-Webhook-Signature", in.Signature)
	set("X-Webhook-Timestamp", in.Timestamp)
	set("X-Hub-Signature-256", in.GithubSig)
	set("X-GitHub-Event", in.GithubEvent)
	set("Sentry-Hook-Signature", in.SentrySig)
	set("Sentry-Hook-Resource", in.SentryResource)
	return h
}

// HookResult is the receiver's answer. Duplicate is true when the same signed
// bytes were delivered before — a platform redelivery, or a replay inside the
// window — and no new item was made.
type HookResult struct {
	Received  bool `json:"received"`
	Duplicate bool `json:"duplicate,omitempty"`
}

func registerPublicHook(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "receive-webhook", Method: http.MethodPost, Path: "/api/hooks/{id}",
		DefaultStatus: http.StatusAccepted, Summary: "Receive a delivery for one webhook source (GitHub, Sentry or a generic JSON body)", Tags: tags,
		Description: "The signature is the authorization. github: X-Hub-Signature-256 over the raw body. " +
			"sentry: Sentry-Hook-Signature over the raw body, with the integration's Client Secret. " +
			"generic: X-Webhook-Timestamp (unix seconds, within 300 s) and X-Webhook-Signature = HMAC-SHA256(secret, timestamp + \".\" + body). " +
			"A delivery that verifies always becomes an item; an unrecognised body gets a plain title.",
	}, func(ctx context.Context, in *hookIn) (*body[HookResult], error) {
		if len(in.RawBody) > maxWebhookBody {
			return nil, huma.Error400BadRequest("the body is too large")
		}
		var wsID, name, kind, sealed string
		var enabled, legacy bool
		err := a.SysDB.QueryRowContext(ctx, `SELECT workspace_id, name, kind, secret_sealed, enabled, legacy_signing FROM webhook_sources WHERE id = $1`, in.ID).
			Scan(&wsID, &name, &kind, &sealed, &enabled, &legacy)
		if notFound(err) {
			return nil, huma.Error404NotFound("this webhook does not exist")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not resolve the webhook")
		}
		// A fixed sentence on the source row, for its owner — never a header
		// value, a signature or a body.
		fail := func(msg string) {
			_, _ = a.SysDB.ExecContext(ctx, `UPDATE webhook_sources SET last_error = $2, last_error_at = now(), updated_at = now() WHERE id = $1`, in.ID, msg)
		}
		if !enabled {
			fail("delivery refused: source disabled")
			return nil, huma.Error403Forbidden("this webhook is disabled")
		}
		secret, err := unseal(sealed)
		if err != nil {
			fail("delivery refused: " + err.Error())
			return nil, huma.Error500InternalServerError("this webhook is misconfigured")
		}

		// The signature, over the raw bytes, before anything is parsed.
		v, err := Verify(kind, secret, legacy, in.headers(), in.RawBody, time.Now())
		if err != nil {
			fail("delivery refused: " + err.Error())
			return nil, huma.Error401Unauthorized("invalid or missing signature")
		}

		// Only now is the body trusted enough to read.
		it := Normalise(kind, v.Event, name, in.RawBody)
		externalID := v.Identity
		if it.ExternalID != "" {
			// A body-supplied id is covered by the signature, like the body.
			externalID = it.ExternalID
		}

		sctx := tenant.WithWorkspace(ctx, wsID)
		// RETURNING yields no row on the conflict branch: a redelivery is
		// sql.ErrNoRows here, which is "duplicate", not a failure.
		var itemID string
		err = a.SQLDB.QueryRowContext(sctx, `
			INSERT INTO notification_items (workspace_id, source_id, source_name, external_id, kind, severity, title, body, actor, url, payload, occurred_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
			ON CONFLICT (workspace_id, source_id, external_id) WHERE external_id <> '' DO NOTHING
			RETURNING id`,
			wsID, in.ID, name, externalID, it.Kind, it.Severity, it.Title, it.Body, it.Actor, it.URL, string(in.RawBody),
		).Scan(&itemID)
		if err != nil && !notFound(err) {
			fail("delivery refused: could not store it")
			return nil, huma.Error500InternalServerError("could not store the delivery")
		}
		inserted := itemID != ""
		if inserted {
			// The notifications page and every tab's bell update without a
			// reload, and the members who may read the feed are alerted —
			// in the tab and on their devices (MH-378). A redelivery is not
			// news and says nothing.
			alerts.Emit(ctx, a, wsID, realtime.Event{
				Kind: "notification", ItemID: itemID, Title: it.Title, Severity: it.Severity, Source: name,
				Permission: authz.NotificationsRead, Alerting: true,
			})
		}
		legacyAt := "last_legacy_at"
		if v.Legacy {
			legacyAt = "now()"
		}
		_, _ = a.SysDB.ExecContext(ctx, `UPDATE webhook_sources SET delivery_count = delivery_count + 1, last_delivery_at = now(),
			last_legacy_at = `+legacyAt+`, updated_at = now() WHERE id = $1`, in.ID)

		return out(HookResult{Received: true, Duplicate: !inserted}), nil
	})
}
