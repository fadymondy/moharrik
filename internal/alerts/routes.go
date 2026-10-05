package alerts

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/crm"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
)

var tags = []string{"Alerts"}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

func scope(ctx context.Context, action string) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, action) {
		return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
	}
	return s, nil
}

// Device is one of the signed-in member's own push subscriptions. Only ever
// the caller's own: nobody lists, removes or tests another member's devices.
type Device struct {
	ID          string     `json:"id"`
	Endpoint    string     `json:"endpoint" doc:"The push service URL — the browser compares it with its own subscription to recognise itself."`
	UserAgent   string     `json:"user_agent"`
	Locale      string     `json:"locale"`
	CreatedAt   time.Time  `json:"created_at"`
	LastOKAt    *time.Time `json:"last_ok_at"`
	LastError   string     `json:"last_error"`
	LastErrorAt *time.Time `json:"last_error_at"`
}

type PushStatus struct {
	Configured bool     `json:"configured" doc:"false when the server has no VAPID keys; push cannot be enabled."`
	PublicKey  string   `json:"public_key" doc:"The VAPID public key: the browser's applicationServerKey."`
	Devices    []Device `json:"devices"`
}

// PushTestResult is what a test push did, across the caller's devices.
type PushTestResult struct {
	Result
	Devices int `json:"devices" doc:"How many of the caller's devices it was sent to."`
}

// BellItem is one row in the header bell: a conversation or a notification item.
type BellItem struct {
	Kind           string    `json:"kind" enum:"conversation,notification"`
	ID             string    `json:"id"`
	Title          string    `json:"title" doc:"The customer (conversation) or the item's title (notification)."`
	Snippet        string    `json:"snippet" doc:"The latest message (conversation) or the source's name (notification)."`
	Channel        string    `json:"channel,omitempty"`
	Severity       string    `json:"severity,omitempty"`
	Unread         bool      `json:"unread"`
	At             time.Time `json:"at"`
	AssigneeUserID *string   `json:"assignee_user_id,omitempty"`
	// AvatarURL is a conversation item's best-available photo (MH-588): the
	// linked contact's, else the conversation's own — the same signed
	// /api/provider-photos/ link inbox.ConversationOut already carries, so the
	// floating chat launcher (web/components/inbox/floating-chat.tsx) can show
	// the same avatar the full inbox list and thread do. Never set for a
	// notification item.
	AvatarURL string `json:"avatar_url,omitempty"`
}

type Bell struct {
	UnreadConversations int64      `json:"unread_conversations"`
	UnreadNotifications int64      `json:"unread_notifications"`
	CanInbox            bool       `json:"can_inbox"`
	CanNotifications    bool       `json:"can_notifications"`
	Items               []BellItem `json:"items"`
}

// bellLimit is how many of each kind the bell reads; the merged list shows the
// newest of them.
const bellLimit = 10

// Register mounts the alert endpoints. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	db := func() *sql.DB { return a.SQLDB }

	// ---- the bell ---------------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-alerts", Method: http.MethodGet, Path: "/api/w/alerts",
		Summary: "The header bell: unread counts and the latest conversations and notification items the caller may read", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[Bell], error) {
		s, err := scope(ctx, authz.WorkspaceRead)
		if err != nil {
			return nil, err
		}
		b := Bell{CanInbox: authz.Can(s.Role, authz.InboxRead), CanNotifications: authz.Can(s.Role, authz.NotificationsRead), Items: []BellItem{}}
		fail := huma.Error500InternalServerError("could not read the alerts")
		if b.CanInbox {
			if err := db().QueryRowContext(ctx, `SELECT count(*) FROM conversations WHERE workspace_id = $1 AND unread`, s.WorkspaceID).Scan(&b.UnreadConversations); err != nil {
				return nil, fail
			}
			rows, err := db().QueryContext(ctx, `
				SELECT c.id, c.channel, coalesce(nullif(c.customer_name, ''), nullif(c.customer_email, ''), c.customer_phone, ''),
				       coalesce((SELECT m.body FROM messages m WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id
				                 ORDER BY m.created_at DESC LIMIT 1), c.subject),
				       c.unread, c.last_message_at, c.assignee_user_id, c.contact_id,
				       ct.photo_attachment_id,
				       (ct.photo_url LIKE 'data:%'), CASE WHEN ct.photo_url LIKE 'data:%' THEN '' ELSE coalesce(ct.photo_url, '') END,
				       (c.photo_url LIKE 'data:%'), CASE WHEN c.photo_url LIKE 'data:%' THEN '' ELSE coalesce(c.photo_url, '') END
				FROM conversations c
				LEFT JOIN contacts ct ON ct.workspace_id = c.workspace_id AND ct.id = c.contact_id
				WHERE c.workspace_id = $1
				ORDER BY c.last_message_at DESC LIMIT $2`, s.WorkspaceID, bellLimit)
			if err != nil {
				return nil, fail
			}
			for rows.Next() {
				it := BellItem{Kind: "conversation"}
				var assignee, contactID, ctPhotoAttachmentID sql.NullString
				var ctHasProviderPhoto, hasProviderPhoto sql.NullBool
				var ctExternalPhotoURL, ownExternalPhotoURL sql.NullString
				if err := rows.Scan(&it.ID, &it.Channel, &it.Title, &it.Snippet, &it.Unread, &it.At, &assignee, &contactID,
					&ctPhotoAttachmentID, &ctHasProviderPhoto, &ctExternalPhotoURL, &hasProviderPhoto, &ownExternalPhotoURL); err != nil {
					rows.Close()
					return nil, fail
				}
				it.Snippet = clip(it.Snippet, 140)
				if assignee.Valid {
					it.AssigneeUserID = &assignee.String
				}
				// MH-588: same "linked contact's photo wins" precedence the full
				// inbox list uses (internal/inbox/routes.go's scanConversation).
				if contactID.Valid {
					it.AvatarURL = crm.AvatarURLLite(s.WorkspaceID, "contact", contactID.String, ctPhotoAttachmentID.String, ctHasProviderPhoto.Bool, ctExternalPhotoURL.String)
				} else {
					it.AvatarURL = crm.AvatarURLLite(s.WorkspaceID, "conversation", it.ID, "", hasProviderPhoto.Bool, ownExternalPhotoURL.String)
				}
				b.Items = append(b.Items, it)
			}
			rows.Close()
		}
		if b.CanNotifications {
			if err := db().QueryRowContext(ctx, `SELECT count(*) FROM notification_items WHERE workspace_id = $1 AND read_at IS NULL AND (resolved_at IS NULL AND (snoozed_until IS NULL OR snoozed_until <= now()))`, s.WorkspaceID).Scan(&b.UnreadNotifications); err != nil {
				return nil, fail
			}
			rows, err := db().QueryContext(ctx, `
				SELECT id, title, source_name, severity, read_at IS NULL, occurred_at
				FROM notification_items WHERE workspace_id = $1 AND (resolved_at IS NULL AND (snoozed_until IS NULL OR snoozed_until <= now()))
				ORDER BY occurred_at DESC LIMIT $2`, s.WorkspaceID, bellLimit)
			if err != nil {
				return nil, fail
			}
			for rows.Next() {
				it := BellItem{Kind: "notification"}
				if err := rows.Scan(&it.ID, &it.Title, &it.Snippet, &it.Severity, &it.Unread, &it.At); err != nil {
					rows.Close()
					return nil, fail
				}
				it.Title = clip(it.Title, 140)
				b.Items = append(b.Items, it)
			}
			rows.Close()
		}
		sort.SliceStable(b.Items, func(i, j int) bool { return b.Items[i].At.After(b.Items[j].At) })
		if len(b.Items) > bellLimit {
			b.Items = b.Items[:bellLimit]
		}
		return out(b), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-alerts-mark-all-read", Method: http.MethodPost, Path: "/api/w/alerts/mark_all_read",
		Summary: "Mark every unread conversation and notification item read — each only if the caller's role may", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[struct {
		Conversations int64 `json:"conversations"`
		Notifications int64 `json:"notifications"`
	}], error) {
		s, err := scope(ctx, authz.WorkspaceRead)
		if err != nil {
			return nil, err
		}
		inbox, notes := authz.Can(s.Role, authz.InboxWrite), authz.Can(s.Role, authz.NotificationsWrite)
		if !inbox && !notes {
			return nil, huma.Error403Forbidden("your role cannot mark anything read in this workspace")
		}
		var res struct {
			Conversations int64 `json:"conversations"`
			Notifications int64 `json:"notifications"`
		}
		if inbox {
			r, err := db().ExecContext(ctx, `UPDATE conversations SET unread = false, updated_at = now() WHERE workspace_id = $1 AND unread`, s.WorkspaceID)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not mark conversations read")
			}
			res.Conversations, _ = r.RowsAffected()
			if res.Conversations > 0 {
				Emit(ctx, a, s.WorkspaceID, realtime.Event{Kind: "conversation", Permission: authz.InboxRead})
			}
		}
		if notes {
			r, err := db().ExecContext(ctx, `UPDATE notification_items SET read_at = now() WHERE workspace_id = $1 AND read_at IS NULL AND (resolved_at IS NULL AND (snoozed_until IS NULL OR snoozed_until <= now()))`, s.WorkspaceID)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not mark notifications read")
			}
			res.Notifications, _ = r.RowsAffected()
			if res.Notifications > 0 {
				Emit(ctx, a, s.WorkspaceID, realtime.Event{Kind: "notifications", Permission: authz.NotificationsRead})
			}
		}
		return out(res), nil
	})

	// ---- web push: this member's devices ------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-push", Method: http.MethodGet, Path: "/api/w/push",
		Summary: "Whether web push is available here, the VAPID public key, and the caller's own devices", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[PushStatus], error) {
		s, err := scope(ctx, authz.PushManage)
		if err != nil {
			return nil, err
		}
		v, ok := vapidFromEnv()
		st := PushStatus{Configured: ok, Devices: []Device{}}
		if ok {
			st.PublicKey = v.Public
		}
		rows, err := db().QueryContext(ctx, `
			SELECT id, endpoint, user_agent, locale, created_at, last_ok_at, last_error, last_error_at
			FROM push_subscriptions WHERE workspace_id = $1 AND user_id = $2 ORDER BY created_at DESC`, s.WorkspaceID, s.UserID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list your devices")
		}
		defer rows.Close()
		for rows.Next() {
			var d Device
			if err := rows.Scan(&d.ID, &d.Endpoint, &d.UserAgent, &d.Locale, &d.CreatedAt, &d.LastOKAt, &d.LastError, &d.LastErrorAt); err != nil {
				return nil, huma.Error500InternalServerError("could not list your devices")
			}
			st.Devices = append(st.Devices, d)
		}
		return out(st), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-push-subscribe", Method: http.MethodPost, Path: "/api/w/push/subscriptions",
		DefaultStatus: http.StatusCreated, Summary: "Enable web push on this browser for the signed-in member (idempotent per browser)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		UserAgentHeader string `header:"User-Agent"`
		Body            struct {
			Endpoint string `json:"endpoint" minLength:"1" maxLength:"2000" doc:"PushSubscription.endpoint"`
			Keys     struct {
				P256dh string `json:"p256dh" minLength:"1" maxLength:"200"`
				Auth   string `json:"auth" minLength:"1" maxLength:"100"`
			} `json:"keys"`
			Locale string `json:"locale,omitempty" enum:"en,ar" doc:"The language the notifications are written in."`
		}
	}) (*body[struct {
		Device Device `json:"device"`
	}], error) {
		s, err := scope(ctx, authz.PushManage)
		if err != nil {
			return nil, err
		}
		if !Configured() {
			return nil, huma.Error503ServiceUnavailable("web push is not configured on this server (VAPID keys)")
		}
		endpoint := strings.TrimSpace(in.Body.Endpoint)
		if err := checkEndpoint(endpoint); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		keys, _ := json.Marshal(sealedKeys{P256dh: strings.TrimSpace(in.Body.Keys.P256dh), Auth: strings.TrimSpace(in.Body.Keys.Auth)})
		sealed, err := seal(string(keys))
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		locale := in.Body.Locale
		if locale != "ar" {
			locale = "en"
		}
		ua := clip(in.UserAgentHeader, 300)
		// Same browser again (or another member now signed in on it): the row
		// moves to the caller, with fresh keys, rather than doubling up.
		var d Device
		if err := db().QueryRowContext(ctx, `
			INSERT INTO push_subscriptions (workspace_id, user_id, endpoint, keys_sealed, user_agent, locale)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (workspace_id, endpoint) DO UPDATE SET user_id = EXCLUDED.user_id, keys_sealed = EXCLUDED.keys_sealed,
			  user_agent = EXCLUDED.user_agent, locale = EXCLUDED.locale, last_error = '', last_error_at = NULL, updated_at = now()
			RETURNING id, endpoint, user_agent, locale, created_at, last_ok_at, last_error, last_error_at`,
			s.WorkspaceID, s.UserID, endpoint, sealed, ua, locale,
		).Scan(&d.ID, &d.Endpoint, &d.UserAgent, &d.Locale, &d.CreatedAt, &d.LastOKAt, &d.LastError, &d.LastErrorAt); err != nil {
			return nil, huma.Error500InternalServerError("could not save the subscription")
		}
		return out(struct {
			Device Device `json:"device"`
		}{d}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-push-unsubscribe", Method: http.MethodDelete, Path: "/api/w/push/subscriptions/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Stop web push on one of the caller's own devices", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*struct{}, error) {
		s, err := scope(ctx, authz.PushManage)
		if err != nil {
			return nil, err
		}
		res, err := db().ExecContext(ctx, `DELETE FROM push_subscriptions WHERE workspace_id = $1 AND user_id = $2 AND id = $3`, s.WorkspaceID, s.UserID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not remove the device")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("device not found")
		}
		return &struct{}{}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-push-test", Method: http.MethodPost, Path: "/api/w/push/test",
		Summary: "Send a test notification to every one of the caller's own devices in this workspace", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[PushTestResult], error) {
		s, err := scope(ctx, authz.PushManage)
		if err != nil {
			return nil, err
		}
		if !Configured() {
			return nil, huma.Error503ServiceUnavailable("web push is not configured on this server (VAPID keys)")
		}
		res, n, err := PushTest(ctx, a, s.WorkspaceID, s.UserID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not send the test")
		}
		if n == 0 {
			return nil, huma.Error409Conflict("push is not enabled on any of your devices in this workspace")
		}
		return out(PushTestResult{Result: res, Devices: n}), nil
	})
}
