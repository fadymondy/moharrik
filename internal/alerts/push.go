// Package alerts tells a member the moment a customer writes (MH-378).
//
// One entry point, Emit, is called wherever an alerting thing is written — a
// customer's message on any channel, a notification-center item. It publishes
// the workspace realtime event (the bell, the popup, the chime and the desktop
// notification in every open tab listen to it) and sends web push to the
// devices of the members the event alerts, so a member with no Moharrik tab
// open hears about it too.
//
// WHO is told is decided once, by realtime.Event.For, for both paths: the tab
// and the lock screen cannot disagree. Conversation events need inbox:read;
// an assigned conversation alerts only its assignee; notification items need
// notifications:read; a visitor or a client-role member gets nothing; and a
// member without inbox:read never receives a customer's words in any form.
package alerts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SherClockHolmes/webpush-go"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// dispatch runs the push side of Emit. Asynchronous in the app — a push service
// that is slow or down must never delay (or fail) the request that saved a
// customer's message. The tests make it synchronous.
var dispatch = func(f func()) { go f() }

// httpClient is what pushes are POSTed with. A bounded timeout, because the
// push services are someone else's servers.
var httpClient webpush.HTTPClient = &http.Client{Timeout: 15 * time.Second}

// Emit publishes ev to the workspace's open tabs and, when it alerts anyone,
// pushes it to their devices. It never fails the caller.
func Emit(ctx context.Context, a *app.App, workspaceID string, ev realtime.Event) {
	if a == nil || workspaceID == "" {
		return
	}
	if a.Events != nil {
		a.Events.Publish(workspaceID, ev)
	}
	if !ev.Alerting || (!Configured() && !NativeConfigured()) || a.SQLDB == nil {
		return
	}
	// Detached from the request: the push outlives the handler that caused it.
	bg := context.WithoutCancel(ctx)
	dispatch(func() {
		pctx, cancel := context.WithTimeout(bg, 60*time.Second)
		defer cancel()
		if _, err := Push(pctx, a, workspaceID, ev); err != nil && a.Log != nil {
			a.Log.Warn("push failed", "workspace", workspaceID, "kind", ev.Kind, "err", err)
		}
	})
}

// Result counts what one push round did.
type Result struct {
	Sent    int `json:"sent" doc:"Accepted by the push service."`
	Removed int `json:"removed" doc:"Subscriptions the push service said are gone (404/410) — deleted."`
	Failed  int `json:"failed" doc:"Refused or unreachable; the device keeps its subscription and shows the error."`
}

func (r *Result) add(o Result) {
	r.Sent += o.Sent
	r.Removed += o.Removed
	r.Failed += o.Failed
}

// Payload is what the service worker receives (web/public/sw-push.js).
type Payload struct {
	Kind           string `json:"kind"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	URL            string `json:"url"`
	Tag            string `json:"tag"`
	ConversationID string `json:"conversation_id,omitempty"`
	ItemID         string `json:"item_id,omitempty"`
}

// Push sends ev to the devices of every active member it alerts — their
// browsers (web push) and their phones (FCM, docs/apps-plan.md) — unless the
// member's own push preferences in this workspace say otherwise.
func Push(ctx context.Context, a *app.App, workspaceID string, ev realtime.Event) (Result, error) {
	var res Result
	v, webOK := vapidFromEnv()
	native, nativeOK := fcmFromEnv()
	if !webOK && !nativeOK {
		return res, errors.New("neither VAPID keys nor an FCM service account are configured")
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	viewers, err := activeMembers(sctx, a.SQLDB, workspaceID)
	if err != nil {
		return res, err
	}
	prefs, err := prefsOf(sctx, a.SQLDB, workspaceID)
	if err != nil {
		return res, err
	}
	slug := workspaceSlug(sctx, a.SQLDB, workspaceID)
	for _, viewer := range viewers {
		mine, ok := ev.For(viewer)
		if !ok || !mine.Alert {
			continue
		}
		p, saved := prefs[viewer.UserID]
		if !saved {
			p = DefaultPrefs
		}
		if !p.wants(ev, viewer.UserID) {
			continue
		}
		if webOK {
			subs, err := subscriptionsOf(sctx, a.SQLDB, workspaceID, viewer.UserID)
			if err != nil {
				return res, err
			}
			for _, s := range subs {
				res.add(deliver(sctx, a.SQLDB, a.Log, v, workspaceID, s, payloadFor(mine, s.Locale, slug)))
			}
		}
		if nativeOK {
			devs, err := nativeDevicesOf(sctx, a.SysDB, viewer.UserID)
			if err != nil {
				return res, err
			}
			for _, d := range devs {
				res.add(deliverNative(sctx, a.SysDB, a.Log, native, d, nativeMessage(payloadFor(mine, d.Locale, slug), workspaceID, slug)))
			}
		}
	}
	return res, nil
}

// PushTest sends a test notification to one member's own devices in a
// workspace: their browsers here, and their phones.
func PushTest(ctx context.Context, a *app.App, workspaceID, userID string) (Result, int, error) {
	var res Result
	v, webOK := vapidFromEnv()
	native, nativeOK := fcmFromEnv()
	if !webOK && !nativeOK {
		return res, 0, errors.New("neither VAPID keys nor an FCM service account are configured")
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	slug := workspaceSlug(sctx, a.SQLDB, workspaceID)
	test := func(locale string) Payload {
		return Payload{Kind: "test", Title: say(locale, "test.title"), Body: say(locale, "test.body"),
			URL: fmt.Sprintf("/%s/w/%s/settings/delivery", locale, url.PathEscape(slug)), Tag: "moharrik-test"}
	}
	n := 0
	if webOK {
		subs, err := subscriptionsOf(sctx, a.SQLDB, workspaceID, userID)
		if err != nil {
			return res, 0, err
		}
		for _, s := range subs {
			res.add(deliver(sctx, a.SQLDB, a.Log, v, workspaceID, s, test(s.Locale)))
		}
		n += len(subs)
	}
	if nativeOK {
		devs, err := nativeDevicesOf(sctx, a.SysDB, userID)
		if err != nil {
			return res, 0, err
		}
		for _, d := range devs {
			res.add(deliverNative(sctx, a.SysDB, a.Log, native, d, nativeMessage(test(d.Locale), workspaceID, slug)))
		}
		n += len(devs)
	}
	return res, n, nil
}

// payloadFor writes the notification one device shows. Everything it says
// comes from the viewer's rendering of the event, so a field For() stripped
// (conversation content, for a member without inbox:read) cannot reappear here.
func payloadFor(ev realtime.Event, locale, slug string) Payload {
	base := fmt.Sprintf("/%s/w/%s", locale, url.PathEscape(slug))
	switch ev.Kind {
	case "notification":
		title := strings.TrimSpace(ev.Title)
		if title == "" {
			title = say(locale, "notification.title")
		}
		return Payload{Kind: ev.Kind, Title: clip(title, 120), Body: clip(ev.Source, 120),
			URL: base + "/notifications", Tag: "item-" + ev.ItemID, ItemID: ev.ItemID}
	case "assigned":
		body := say(locale, "assigned.body")
		if ev.Sender != "" {
			body = fmt.Sprintf(say(locale, "assigned.bodyFrom"), clip(ev.Sender, 60))
		}
		if ev.Snippet != "" {
			body += " — " + clip(ev.Snippet, 120)
		}
		return Payload{Kind: "assigned", Title: say(locale, "assigned.title"), Body: body,
			URL: base + "/inbox?c=" + url.QueryEscape(ev.ConversationID), Tag: "conv-" + ev.ConversationID,
			ConversationID: ev.ConversationID}
	default: // an inbound message
		title := say(locale, "message.title")
		if ev.Sender != "" {
			title = fmt.Sprintf(say(locale, "message.titleFrom"), clip(ev.Sender, 60))
		}
		return Payload{Kind: "message", Title: title, Body: clip(ev.Snippet, 160),
			URL: base + "/inbox?c=" + url.QueryEscape(ev.ConversationID), Tag: "conv-" + ev.ConversationID,
			ConversationID: ev.ConversationID}
	}
}

var phrases = map[string]map[string]string{
	"en": {
		"message.title":      "New message",
		"message.titleFrom":  "New message from %s",
		"notification.title": "New notification",
		"assigned.title":     "Assigned to you",
		"assigned.body":      "A conversation was assigned to you.",
		"assigned.bodyFrom":  "The conversation with %s is yours now.",
		"test.title":         "Moharrik test notification",
		"test.body":          "Push notifications work on this device.",
	},
	"ar": {
		"message.title":      "رسالة جديدة",
		"message.titleFrom":  "رسالة جديدة من %s",
		"notification.title": "إشعار جديد",
		"assigned.title":     "أُسندت إليك",
		"assigned.body":      "أُسندت إليك محادثة.",
		"assigned.bodyFrom":  "المحادثة مع %s أصبحت لك.",
		"test.title":         "إشعار تجريبي من محرّك",
		"test.body":          "الإشعارات تعمل على هذا الجهاز.",
	},
}

func say(locale, key string) string {
	if m, ok := phrases[locale]; ok {
		if s, ok := m[key]; ok {
			return s
		}
	}
	return phrases["en"][key]
}

func clip(s string, n int) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// deliver sends one payload to one device and records what happened on its row:
// accepted → last_ok_at; gone (404/410) → the row is deleted, because the
// browser unsubscribed or the member cleared its data and the endpoint will
// never work again; anything else → last_error, and the row stays.
func deliver(ctx context.Context, db *sql.DB, log *slog.Logger, v vapid, workspaceID string, s subscription, p Payload) Result {
	fail := func(msg string) Result {
		_, _ = db.ExecContext(ctx, `UPDATE push_subscriptions SET last_error = $3, last_error_at = now(), updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, workspaceID, s.ID, msg)
		return Result{Failed: 1}
	}
	if err := checkEndpoint(s.Endpoint); err != nil {
		return fail("not sent: the endpoint is not an allowed push service")
	}
	body, err := json.Marshal(p)
	if err != nil {
		return fail("not sent: could not encode the notification")
	}
	resp, err := webpush.SendNotificationWithContext(ctx, body, &webpush.Subscription{
		Endpoint: s.Endpoint,
		Keys:     webpush.Keys{P256dh: s.P256dh, Auth: s.Auth},
	}, &webpush.Options{
		HTTPClient:      httpClient,
		Subscriber:      v.subscriber(),
		VAPIDPublicKey:  v.Public,
		VAPIDPrivateKey: v.Private,
		// A customer's message is worth a day; after that the inbox has it.
		TTL:     24 * 60 * 60,
		Urgency: webpush.UrgencyHigh,
		// A second message on the same thread replaces a pending first one
		// rather than stacking up behind it on a phone that was offline.
		Topic: topic(p.Tag),
	})
	if err != nil {
		return fail("not sent: the push service could not be reached")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		_, _ = db.ExecContext(ctx, `UPDATE push_subscriptions SET last_ok_at = now(), last_error = '', updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, workspaceID, s.ID)
		return Result{Sent: 1}
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		_, _ = db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE workspace_id = $1 AND id = $2`, workspaceID, s.ID)
		if log != nil {
			// The host and status only: the endpoint path is the device's
			// capability and does not belong in a log.
			log.Info("push subscription gone; removed", "workspace", workspaceID, "status", resp.StatusCode, "service", endpointHost(s.Endpoint))
		}
		return Result{Removed: 1}
	default:
		if log != nil {
			log.Warn("push refused", "workspace", workspaceID, "status", resp.StatusCode, "service", endpointHost(s.Endpoint))
		}
		return fail(fmt.Sprintf("refused by the push service (HTTP %d)", resp.StatusCode))
	}
}

func endpointHost(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return ""
}

// topic is the Topic header: at most 32 URL-safe characters (RFC 8030 §5.4).
func topic(tag string) string {
	sum := sha256.Sum256([]byte(tag))
	return hex.EncodeToString(sum[:])[:32]
}

// ---- storage ---------------------------------------------------------------

type subscription struct {
	ID, Endpoint, P256dh, Auth, Locale string
}

type sealedKeys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

func subscriptionsOf(ctx context.Context, db *sql.DB, workspaceID, userID string) ([]subscription, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, endpoint, keys_sealed, locale FROM push_subscriptions
		WHERE workspace_id = $1 AND user_id = $2 ORDER BY created_at`, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []subscription
	for rows.Next() {
		var s subscription
		var sealed string
		if err := rows.Scan(&s.ID, &s.Endpoint, &sealed, &s.Locale); err != nil {
			return nil, err
		}
		plain, err := unseal(sealed)
		if err != nil {
			continue // shown on the device row the next time it fails; nothing to send with
		}
		var k sealedKeys
		if json.Unmarshal([]byte(plain), &k) != nil {
			continue
		}
		s.P256dh, s.Auth = k.P256dh, k.Auth
		out = append(out, s)
	}
	return out, rows.Err()
}

// activeMembers is the workspace's audience: every ACTIVE member and their role.
// A suspended member is nobody's audience.
func activeMembers(ctx context.Context, db *sql.DB, workspaceID string) ([]realtime.Viewer, error) {
	rows, err := db.QueryContext(ctx, `SELECT user_id, role FROM workspace_members WHERE workspace_id = $1 AND status = 'active'`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []realtime.Viewer
	for rows.Next() {
		var v realtime.Viewer
		if err := rows.Scan(&v.UserID, &v.Role); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func workspaceSlug(ctx context.Context, db *sql.DB, workspaceID string) string {
	var slug string
	if err := db.QueryRowContext(ctx, `SELECT slug FROM workspaces WHERE id = $1`, workspaceID).Scan(&slug); err != nil {
		return workspaceID // unreachable in practice: the event was raised inside this workspace
	}
	return slug
}
