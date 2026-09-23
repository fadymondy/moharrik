package notifications

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
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
		s, err := scope(ctx, authz.NotificationsRead)
		if err != nil {
			return nil, err
		}
		rows, err := db().QueryContext(ctx, webhookSourceSelect+` ORDER BY lower(name)`, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list webhook sources")
		}
		defer rows.Close()
		base := publicBase()
		list := []WebhookSourceOut{}
		for rows.Next() {
			src, err := scanSource(rows, base)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not list webhook sources")
			}
			list = append(list, src)
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
		s, err := scope(ctx, authz.NotificationsManage)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSpace(in.Body.Name)
		if name == "" {
			return nil, huma.Error422UnprocessableEntity("a name is required")
		}
		kind := in.Body.Kind
		if kind == "" {
			kind = "generic"
		}
		if !sourceKinds[kind] {
			return nil, huma.Error422UnprocessableEntity("unknown source kind")
		}
		// Sentry signs with the integration's own Client Secret, which Sentry
		// generates and nobody can choose, so a sentry source starts with no
		// secret and refuses every delivery until one is set (PUT .../secret).
		// Every other kind gets a generated one, shown once. legacy_signing is
		// left at its default, false: new sources are timestamped from day one.
		var plain, sealed string
		if kind != "sentry" {
			if plain, err = newSecret(); err != nil {
				return nil, huma.Error500InternalServerError("could not generate a secret")
			}
			if sealed, err = seal(plain); err != nil {
				return nil, huma.Error500InternalServerError(err.Error())
			}
		}
		var id string
		err = db().QueryRowContext(ctx, `
			INSERT INTO webhook_sources (workspace_id, name, kind, secret_sealed, created_by)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			s.WorkspaceID, name, kind, sealed, s.UserID,
		).Scan(&id)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not create the webhook source")
		}
		row := db().QueryRowContext(ctx, webhookSourceSelect+` AND id = $2`, s.WorkspaceID, id)
		src, err := scanSource(row, publicBase())
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the webhook source")
		}
		return out(WebhookSourceWithSecret{Source: src, Secret: plain}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-webhook-source", Method: http.MethodPatch, Path: "/api/w/webhook_sources/{id}",
		Summary: "Rename, enable/disable, or move a webhook source off legacy signing", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Name    *string `json:"name,omitempty" maxLength:"80"`
			Enabled *bool   `json:"enabled,omitempty"`
			// One way only. Turning it back on would reopen replay for a
			// source whose owner already closed it; a sender still on the old
			// scheme is fixed at the sender.
			LegacySigning *bool `json:"legacy_signing,omitempty" doc:"Only false is accepted: stop accepting the pre-MH-381 body-only signature."`
		}
	}) (*body[struct {
		Source WebhookSourceOut `json:"source"`
	}], error) {
		s, err := scope(ctx, authz.NotificationsManage)
		if err != nil {
			return nil, err
		}
		u := &update{}
		if in.Body.Name != nil {
			n := strings.TrimSpace(*in.Body.Name)
			if n == "" {
				return nil, huma.Error422UnprocessableEntity("a name is required")
			}
			u.set("name", n)
		}
		if in.Body.Enabled != nil {
			u.set("enabled", *in.Body.Enabled)
		}
		if in.Body.LegacySigning != nil {
			if *in.Body.LegacySigning {
				return nil, huma.Error422UnprocessableEntity("legacy signing cannot be turned back on; sign deliveries with X-Webhook-Timestamp instead")
			}
			u.set("legacy_signing", false)
		}
		if len(u.sets) > 0 {
			res, err := db().ExecContext(ctx, `UPDATE webhook_sources SET `+strings.Join(u.sets, ", ")+`, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
				append([]any{s.WorkspaceID, in.ID}, u.args...)...)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not update the webhook source")
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, huma.Error404NotFound("webhook source not found")
			}
		}
		row := db().QueryRowContext(ctx, webhookSourceSelect+` AND id = $2`, s.WorkspaceID, in.ID)
		src, err := scanSource(row, publicBase())
		if notFound(err) {
			return nil, huma.Error404NotFound("webhook source not found")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not read the webhook source")
		}
		return out(struct {
			Source WebhookSourceOut `json:"source"`
		}{src}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-rotate-webhook-source", Method: http.MethodPost, Path: "/api/w/webhook_sources/{id}/rotate",
		Summary: "Replace a webhook source's signing secret; the old one stops working immediately", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[WebhookSourceWithSecret], error) {
		s, err := scope(ctx, authz.NotificationsManage)
		if err != nil {
			return nil, err
		}
		var kind string
		err = db().QueryRowContext(ctx, `SELECT kind FROM webhook_sources WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID).Scan(&kind)
		if notFound(err) {
			return nil, huma.Error404NotFound("webhook source not found")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not read the webhook source")
		}
		if kind == "sentry" {
			return nil, huma.Error422UnprocessableEntity("Sentry generates this secret: rotate it in Sentry, then set the new Client Secret here")
		}
		plain, err := newSecret()
		if err != nil {
			return nil, huma.Error500InternalServerError("could not generate a secret")
		}
		sealed, err := seal(plain)
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		res, err := db().ExecContext(ctx, `UPDATE webhook_sources SET secret_sealed = $3, rotated_at = now(), updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID, sealed)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not rotate the secret")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("webhook source not found")
		}
		row := db().QueryRowContext(ctx, webhookSourceSelect+` AND id = $2`, s.WorkspaceID, in.ID)
		src, err := scanSource(row, publicBase())
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the webhook source")
		}
		return out(WebhookSourceWithSecret{Source: src, Secret: plain}), nil
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
		s, err := scope(ctx, authz.NotificationsManage)
		if err != nil {
			return nil, err
		}
		plain := strings.TrimSpace(in.Body.Secret)
		if len(plain) < 16 {
			return nil, huma.Error422UnprocessableEntity("the secret must be at least 16 characters")
		}
		sealed, err := seal(plain)
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		res, err := db().ExecContext(ctx, `UPDATE webhook_sources SET secret_sealed = $3, rotated_at = now(), updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID, sealed)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not set the secret")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("webhook source not found")
		}
		row := db().QueryRowContext(ctx, webhookSourceSelect+` AND id = $2`, s.WorkspaceID, in.ID)
		src, err := scanSource(row, publicBase())
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the webhook source")
		}
		return out(struct {
			Source WebhookSourceOut `json:"source"`
		}{src}), nil
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
		Summary: "The notification feed, newest first", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Unread   bool   `query:"unread" doc:"Only items not yet marked read."`
		Saved    bool   `query:"saved" doc:"Only saved items."`
		SourceID string `query:"source_id"`
		Severity string `query:"severity" enum:",info,warning,error"`
		Limit    int    `query:"limit" minimum:"1" maximum:"500"`
	}) (*body[struct {
		Items       []NotificationItemOut `json:"items"`
		UnreadCount int64                 `json:"unread_count"`
	}], error) {
		s, err := scope(ctx, authz.NotificationsRead)
		if err != nil {
			return nil, err
		}
		q := notificationItemSelect
		args := []any{s.WorkspaceID}
		if in.Unread {
			q += ` AND read_at IS NULL`
		}
		if in.Saved {
			q += ` AND saved_at IS NOT NULL`
		}
		if in.SourceID != "" {
			args = append(args, in.SourceID)
			q += ` AND source_id = $` + strconv.Itoa(len(args))
		}
		if in.Severity != "" {
			args = append(args, in.Severity)
			q += ` AND severity = $` + strconv.Itoa(len(args))
		}
		limit := in.Limit
		if limit <= 0 {
			limit = 200
		}
		q += ` ORDER BY occurred_at DESC LIMIT ` + strconv.Itoa(limit)
		rows, err := db().QueryContext(ctx, q, args...)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list notifications")
		}
		defer rows.Close()
		list := []NotificationItemOut{}
		for rows.Next() {
			it, err := scanItem(rows)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not list notifications")
			}
			list = append(list, it)
		}
		var unread int64
		if err := db().QueryRowContext(ctx, `SELECT count(*) FROM notification_items WHERE workspace_id = $1 AND read_at IS NULL`, s.WorkspaceID).Scan(&unread); err != nil {
			return nil, huma.Error500InternalServerError("could not count unread notifications")
		}
		return out(struct {
			Items       []NotificationItemOut `json:"items"`
			UnreadCount int64                 `json:"unread_count"`
		}{list, unread}), nil
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
		s, err := scope(ctx, authz.NotificationsWrite)
		if err != nil {
			return nil, err
		}
		u := &update{}
		if in.Body.Read != nil {
			if *in.Body.Read {
				u.set("read_at", time.Now())
			} else {
				u.set("read_at", nil)
			}
		}
		if in.Body.Saved != nil {
			if *in.Body.Saved {
				u.set("saved_at", time.Now())
			} else {
				u.set("saved_at", nil)
			}
		}
		if len(u.sets) > 0 {
			res, err := db().ExecContext(ctx, `UPDATE notification_items SET `+strings.Join(u.sets, ", ")+` WHERE workspace_id = $1 AND id = $2`,
				append([]any{s.WorkspaceID, in.ID}, u.args...)...)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not update the notification")
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, huma.Error404NotFound("notification not found")
			}
		}
		row := db().QueryRowContext(ctx, notificationItemSelect+` AND id = $2`, s.WorkspaceID, in.ID)
		it, err := scanItem(row)
		if notFound(err) {
			return nil, huma.Error404NotFound("notification not found")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not read the notification")
		}
		return out(struct {
			Item NotificationItemOut `json:"item"`
		}{it}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-mark-all-notifications-read", Method: http.MethodPost, Path: "/api/w/notifications/mark_all_read",
		Summary: "Mark every unread item read (does not touch saved)", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[struct {
		Marked int64 `json:"marked"`
	}], error) {
		s, err := scope(ctx, authz.NotificationsWrite)
		if err != nil {
			return nil, err
		}
		res, err := db().ExecContext(ctx, `UPDATE notification_items SET read_at = now() WHERE workspace_id = $1 AND read_at IS NULL`, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not mark notifications read")
		}
		n, _ := res.RowsAffected()
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
		res, err := a.SQLDB.ExecContext(sctx, `
			INSERT INTO notification_items (workspace_id, source_id, source_name, external_id, kind, severity, title, body, actor, url, payload, occurred_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
			ON CONFLICT (workspace_id, source_id, external_id) WHERE external_id <> '' DO NOTHING`,
			wsID, in.ID, name, externalID, it.Kind, it.Severity, it.Title, it.Body, it.Actor, it.URL, string(in.RawBody),
		)
		if err != nil {
			fail("delivery refused: could not store it")
			return nil, huma.Error500InternalServerError("could not store the delivery")
		}
		inserted, _ := res.RowsAffected()
		legacyAt := "last_legacy_at"
		if v.Legacy {
			legacyAt = "now()"
		}
		_, _ = a.SysDB.ExecContext(ctx, `UPDATE webhook_sources SET delivery_count = delivery_count + 1, last_delivery_at = now(),
			last_legacy_at = `+legacyAt+`, updated_at = now() WHERE id = $1`, in.ID)

		return out(HookResult{Received: true, Duplicate: inserted == 0}), nil
	})
}
