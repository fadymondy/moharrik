package alerts

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"

	"github.com/fadymondy/moharrik/internal/realtime"
)

/*
The apps' half of an alert (docs/apps-plan.md §4): the same audience Push
already decided, delivered to the member's phones over FCM, and the member's
own push preferences, which gate BOTH transports.

device_tokens is platform data (owner pool, one registration per app install,
owned by a user and not by a workspace): every query here names its user. The
workspace side — who among its members is alerted, and what they are told — is
still decided entirely by realtime.Event.For in Push.
*/

// Prefs is what a member wants pushed to their devices in one workspace.
// The zero value is not the default: DefaultPrefs is (everything on).
type Prefs struct {
	Messages      bool `json:"messages" doc:"A customer's new message."`
	AssignedOnly  bool `json:"assigned_only" doc:"Only messages in conversations assigned to me."`
	Assignments   bool `json:"assignments" doc:"A conversation assigned to me by someone else."`
	Notifications bool `json:"notifications" doc:"A new notification-center item."`
}

// DefaultPrefs is what a member with no saved preferences gets.
var DefaultPrefs = Prefs{Messages: true, Assignments: true, Notifications: true}

// wants reports whether a member's preferences let this (already per-viewer
// rendered, alerting) event be pushed to them.
func (p Prefs) wants(ev realtime.Event, userID string) bool {
	switch ev.Kind {
	case "notification":
		return p.Notifications
	case "assigned":
		return p.Assignments
	case "test":
		return true
	default: // a customer's message
		if !p.Messages {
			return false
		}
		// AlertOnly is the assignee on an assigned conversation; an unassigned
		// one has none, and "assigned to me only" keeps it quiet.
		return !p.AssignedOnly || ev.AlertOnly == userID
	}
}

// prefsOf reads every member's saved preferences in a workspace. A member
// missing from the map has DefaultPrefs.
func prefsOf(ctx context.Context, db *sql.DB, workspaceID string) (map[string]Prefs, error) {
	rows, err := db.QueryContext(ctx, `SELECT user_id, messages, assigned_only, assignments, notifications
		FROM push_prefs WHERE workspace_id = $1`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Prefs{}
	for rows.Next() {
		var uid string
		var p Prefs
		if err := rows.Scan(&uid, &p.Messages, &p.AssignedOnly, &p.Assignments, &p.Notifications); err != nil {
			return nil, err
		}
		out[uid] = p
	}
	return out, rows.Err()
}

// PrefsFor reads one member's preferences in a workspace (DefaultPrefs when unsaved).
func PrefsFor(ctx context.Context, db *sql.DB, workspaceID, userID string) (Prefs, error) {
	p := DefaultPrefs
	err := db.QueryRowContext(ctx, `SELECT messages, assigned_only, assignments, notifications
		FROM push_prefs WHERE workspace_id = $1 AND user_id = $2`, workspaceID, userID,
	).Scan(&p.Messages, &p.AssignedOnly, &p.Assignments, &p.Notifications)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultPrefs, nil
	}
	return p, err
}

// nativeDevice is one app install that receives FCM.
type nativeDevice struct {
	ID, Token, Locale, Platform string
}

// nativeDevicesOf lists a user's app installs. Owner pool: device_tokens is
// platform data, and the user predicate is the whole scope.
func nativeDevicesOf(ctx context.Context, db *sql.DB, userID string) ([]nativeDevice, error) {
	if db == nil {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT id, token, locale, platform FROM device_tokens WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []nativeDevice
	for rows.Next() {
		var d nativeDevice
		if err := rows.Scan(&d.ID, &d.Token, &d.Locale, &d.Platform); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// nativeMessage turns the payload a web device would show into the FCM
// message an app shows. The data block is everything the app needs to open the
// right screen in the right workspace when the notification is tapped.
func nativeMessage(p Payload, workspaceID, slug string) NativeMessage {
	data := map[string]string{
		"kind":         p.Kind,
		"workspace_id": workspaceID,
		"slug":         slug,
		"url":          p.URL,
	}
	if p.ConversationID != "" {
		data["conversation_id"] = p.ConversationID
	}
	if p.ItemID != "" {
		data["item_id"] = p.ItemID
	}
	return NativeMessage{Title: p.Title, Body: p.Body, Collapse: p.Tag, Data: data}
}

// deliverNative sends one message to one app install and records what
// happened on its row: accepted → last_ok_at; dead token → the row is deleted
// (the app was uninstalled or its token rotated); anything else → last_error.
func deliverNative(ctx context.Context, db *sql.DB, log *slog.Logger, s NativeSender, d nativeDevice, m NativeMessage) Result {
	err := s.Send(ctx, d.Token, m)
	switch {
	case err == nil:
		_, _ = db.ExecContext(ctx, `UPDATE device_tokens SET last_ok_at = now(), last_error = '', updated_at = now() WHERE id = $1`, d.ID)
		return Result{Sent: 1}
	case errors.Is(err, ErrDeadToken):
		_, _ = db.ExecContext(ctx, `DELETE FROM device_tokens WHERE id = $1`, d.ID)
		if log != nil {
			log.Info("app device token gone; removed", "platform", d.Platform)
		}
		return Result{Removed: 1}
	default:
		// A fixed sentence, never Google's response body.
		msg := "not sent: FCM could not be reached"
		if strings.Contains(err.Error(), "refused") || strings.Contains(err.Error(), "rejected") {
			msg = "refused by FCM"
		}
		_, _ = db.ExecContext(ctx, `UPDATE device_tokens SET last_error = $2, last_error_at = now(), updated_at = now() WHERE id = $1`, d.ID, msg)
		if log != nil {
			log.Warn("app push failed", "platform", d.Platform, "err", err)
		}
		return Result{Failed: 1}
	}
}
