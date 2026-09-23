package notifications

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/alerts"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/realtime"
)

/*
The notification center's operations, once (MH-380).

routes.go's REST handlers and internal/mcp's notification tools both call
these, so marking, saving and every change to a webhook source goes through
one path with one set of rules, and the members' open tabs hear about it the
same way whoever made the change.

Each reads the caller from ctx and checks the role itself. Errors are huma
status errors: REST returns them as they are, MCP hands back their message.
*/

// ItemFilter narrows the feed. Every field is optional.
type ItemFilter struct {
	Unread   bool
	Saved    bool
	SourceID string
	Severity string
	Limit    int
	// Cursor is NextCursor from the previous page.
	Cursor string
}

// ItemList is one page of the feed.
type ItemList struct {
	Items       []NotificationItemOut `json:"items"`
	UnreadCount int64                 `json:"unread_count"`
	// NextCursor fetches the page after this one; empty on the last page.
	NextCursor string `json:"next_cursor"`
}

// SourcePatch is a webhook source update. nil = unchanged.
type SourcePatch struct {
	Name    *string `json:"name,omitempty" maxLength:"80"`
	Enabled *bool   `json:"enabled,omitempty"`
	// One way only. Turning it back on would reopen replay for a source whose
	// owner already closed it; a sender still on the old scheme is fixed at
	// the sender.
	LegacySigning *bool `json:"legacy_signing,omitempty" doc:"Only false is accepted: stop accepting the pre-MH-381 body-only signature."`
}

// ListItems is the feed, newest first, one page at a time.
func ListItems(ctx context.Context, a *app.App, f ItemFilter) (*ItemList, error) {
	s, err := scope(ctx, authz.NotificationsRead)
	if err != nil {
		return nil, err
	}
	if f.Severity != "" && !severities[f.Severity] {
		return nil, huma.Error422UnprocessableEntity("severity must be info, warning or error")
	}
	q := notificationItemSelect
	args := []any{s.WorkspaceID}
	if f.Unread {
		q += ` AND read_at IS NULL`
	}
	if f.Saved {
		q += ` AND saved_at IS NOT NULL`
	}
	if f.SourceID != "" {
		args = append(args, f.SourceID)
		q += ` AND source_id = $` + strconv.Itoa(len(args))
	}
	if f.Severity != "" {
		args = append(args, f.Severity)
		q += ` AND severity = $` + strconv.Itoa(len(args))
	}
	if f.Cursor != "" {
		at, id, ok := decodeCursor(f.Cursor)
		if !ok {
			return nil, huma.Error422UnprocessableEntity("that page cursor is not valid")
		}
		args = append(args, at, id)
		q += ` AND (occurred_at, id) < ($` + strconv.Itoa(len(args)-1) + `, $` + strconv.Itoa(len(args)) + `)`
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 500 {
		limit = 500
	}
	// One more than asked: whether it exists is whether there is a next page.
	q += ` ORDER BY occurred_at DESC, id DESC LIMIT ` + strconv.Itoa(limit+1)
	rows, err := a.SQLDB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not list notifications")
	}
	defer rows.Close()
	res := &ItemList{Items: []NotificationItemOut{}}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list notifications")
		}
		res.Items = append(res.Items, it)
	}
	if len(res.Items) > limit {
		res.Items = res.Items[:limit]
		last := res.Items[limit-1]
		res.NextCursor = encodeCursor(last.OccurredAt, last.ID)
	}
	if err := a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM notification_items WHERE workspace_id = $1 AND read_at IS NULL`, s.WorkspaceID).Scan(&res.UnreadCount); err != nil {
		return nil, huma.Error500InternalServerError("could not count unread notifications")
	}
	return res, nil
}

// GetItem reads one item, with the raw payload it was made from.
func GetItem(ctx context.Context, a *app.App, id string) (*NotificationItemOut, error) {
	s, err := scope(ctx, authz.NotificationsRead)
	if err != nil {
		return nil, err
	}
	it, err := scanItem(a.SQLDB.QueryRowContext(ctx, notificationItemSelect+` AND id = $2`, s.WorkspaceID, id))
	if notFound(err) {
		return nil, huma.Error404NotFound("notification not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the notification")
	}
	return &it, nil
}

// UpdateItem marks an item read/unread and/or saved/unsaved.
func UpdateItem(ctx context.Context, a *app.App, id string, read, saved *bool) (*NotificationItemOut, error) {
	s, err := scope(ctx, authz.NotificationsWrite)
	if err != nil {
		return nil, err
	}
	u := &update{}
	if read != nil {
		if *read {
			u.set("read_at", time.Now())
		} else {
			u.set("read_at", nil)
		}
	}
	if saved != nil {
		if *saved {
			u.set("saved_at", time.Now())
		} else {
			u.set("saved_at", nil)
		}
	}
	if len(u.sets) > 0 {
		res, err := a.SQLDB.ExecContext(ctx, `UPDATE notification_items SET `+strings.Join(u.sets, ", ")+` WHERE workspace_id = $1 AND id = $2`,
			append([]any{s.WorkspaceID, id}, u.args...)...)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not update the notification")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("notification not found")
		}
		// Read/unread moved: every tab's bell recounts.
		alerts.Emit(ctx, a, s.WorkspaceID, realtime.Event{Kind: "notifications", ItemID: id, AuthorUserID: s.UserID, Permission: authz.NotificationsRead})
	}
	it, err := scanItem(a.SQLDB.QueryRowContext(ctx, notificationItemSelect+` AND id = $2`, s.WorkspaceID, id))
	if notFound(err) {
		return nil, huma.Error404NotFound("notification not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the notification")
	}
	return &it, nil
}

// MarkAllRead marks every unread item read (saved items stay saved), or only
// one source's when sourceID is given, and returns how many it marked.
func MarkAllRead(ctx context.Context, a *app.App, sourceID string) (int64, error) {
	s, err := scope(ctx, authz.NotificationsWrite)
	if err != nil {
		return 0, err
	}
	q := `UPDATE notification_items SET read_at = now() WHERE workspace_id = $1 AND read_at IS NULL`
	args := []any{s.WorkspaceID}
	if sourceID = strings.TrimSpace(sourceID); sourceID != "" {
		q += ` AND source_id = $2`
		args = append(args, sourceID)
	}
	res, err := a.SQLDB.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, huma.Error500InternalServerError("could not mark notifications read")
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		alerts.Emit(ctx, a, s.WorkspaceID, realtime.Event{Kind: "notifications", AuthorUserID: s.UserID, Permission: authz.NotificationsRead})
	}
	return n, nil
}

// ---- webhook sources ---------------------------------------------------------

// ListSources lists the webhook sources, each with the URL to POST to.
func ListSources(ctx context.Context, a *app.App) ([]WebhookSourceOut, error) {
	s, err := scope(ctx, authz.NotificationsRead)
	if err != nil {
		return nil, err
	}
	rows, err := a.SQLDB.QueryContext(ctx, webhookSourceSelect+` ORDER BY lower(name)`, s.WorkspaceID)
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
	return list, nil
}

// CreateSource adds a webhook source. Every kind but sentry gets a generated
// secret, returned once; Sentry generates its own (its Client Secret), so a
// sentry source takes the one given, or starts with none and refuses every
// delivery until one is set.
func CreateSource(ctx context.Context, a *app.App, name, kind, sentrySecret string) (*WebhookSourceWithSecret, error) {
	s, err := scope(ctx, authz.NotificationsManage)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, huma.Error422UnprocessableEntity("a name is required")
	}
	if kind == "" {
		kind = "generic"
	}
	if !sourceKinds[kind] {
		return nil, huma.Error422UnprocessableEntity("the kind must be generic, github or sentry")
	}
	sentrySecret = strings.TrimSpace(sentrySecret)
	if sentrySecret != "" && kind != "sentry" {
		return nil, huma.Error422UnprocessableEntity("a secret can only be given for a sentry source; the others get a generated one")
	}
	if sentrySecret != "" && len(sentrySecret) < 16 {
		return nil, huma.Error422UnprocessableEntity("the Sentry Client Secret must be at least 16 characters")
	}
	// legacy_signing is left at its default, false: new sources are
	// timestamped from day one (MH-381).
	var plain, sealed string
	if kind != "sentry" {
		if plain, err = newSecret(); err != nil {
			return nil, huma.Error500InternalServerError("could not generate a secret")
		}
	}
	if toSeal := firstOf(plain, sentrySecret); toSeal != "" {
		if sealed, err = seal(toSeal); err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
	}
	var id string
	if err := a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO webhook_sources (workspace_id, name, kind, secret_sealed, created_by)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		s.WorkspaceID, name, kind, sealed, s.UserID,
	).Scan(&id); err != nil {
		return nil, huma.Error500InternalServerError("could not create the webhook source")
	}
	src, err := scanSource(a.SQLDB.QueryRowContext(ctx, webhookSourceSelect+` AND id = $2`, s.WorkspaceID, id), publicBase())
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the webhook source")
	}
	return &WebhookSourceWithSecret{Source: src, Secret: plain}, nil
}

// UpdateSource renames, enables/disables, or moves a source off legacy signing.
func UpdateSource(ctx context.Context, a *app.App, id string, p SourcePatch) (*WebhookSourceOut, error) {
	s, err := scope(ctx, authz.NotificationsManage)
	if err != nil {
		return nil, err
	}
	u := &update{}
	if p.Name != nil {
		n := strings.TrimSpace(*p.Name)
		if n == "" {
			return nil, huma.Error422UnprocessableEntity("a name is required")
		}
		u.set("name", n)
	}
	if p.Enabled != nil {
		u.set("enabled", *p.Enabled)
	}
	if p.LegacySigning != nil {
		if *p.LegacySigning {
			return nil, huma.Error422UnprocessableEntity("legacy signing cannot be turned back on; sign deliveries with X-Webhook-Timestamp instead")
		}
		u.set("legacy_signing", false)
	}
	if len(u.sets) > 0 {
		res, err := a.SQLDB.ExecContext(ctx, `UPDATE webhook_sources SET `+strings.Join(u.sets, ", ")+`, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
			append([]any{s.WorkspaceID, id}, u.args...)...)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not update the webhook source")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("webhook source not found")
		}
	}
	src, err := scanSource(a.SQLDB.QueryRowContext(ctx, webhookSourceSelect+` AND id = $2`, s.WorkspaceID, id), publicBase())
	if notFound(err) {
		return nil, huma.Error404NotFound("webhook source not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the webhook source")
	}
	return &src, nil
}

// RotateSource replaces a source's signing secret; the old one stops
// verifying at once. The new one is returned, once.
func RotateSource(ctx context.Context, a *app.App, id string) (*WebhookSourceWithSecret, error) {
	s, err := scope(ctx, authz.NotificationsManage)
	if err != nil {
		return nil, err
	}
	var kind string
	err = a.SQLDB.QueryRowContext(ctx, `SELECT kind FROM webhook_sources WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, id).Scan(&kind)
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
	res, err := a.SQLDB.ExecContext(ctx, `UPDATE webhook_sources SET secret_sealed = $3, rotated_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, id, sealed)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not rotate the secret")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, huma.Error404NotFound("webhook source not found")
	}
	src, err := scanSource(a.SQLDB.QueryRowContext(ctx, webhookSourceSelect+` AND id = $2`, s.WorkspaceID, id), publicBase())
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the webhook source")
	}
	return &WebhookSourceWithSecret{Source: src, Secret: plain}, nil
}

// SetSourceSecret sets a source's secret to one the sending platform
// generated (Sentry's Client Secret).
func SetSourceSecret(ctx context.Context, a *app.App, id, secret string) (*WebhookSourceOut, error) {
	s, err := scope(ctx, authz.NotificationsManage)
	if err != nil {
		return nil, err
	}
	plain := strings.TrimSpace(secret)
	if len(plain) < 16 {
		return nil, huma.Error422UnprocessableEntity("the secret must be at least 16 characters")
	}
	sealed, err := seal(plain)
	if err != nil {
		return nil, huma.Error500InternalServerError(err.Error())
	}
	res, err := a.SQLDB.ExecContext(ctx, `UPDATE webhook_sources SET secret_sealed = $3, rotated_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, id, sealed)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not set the secret")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, huma.Error404NotFound("webhook source not found")
	}
	src, err := scanSource(a.SQLDB.QueryRowContext(ctx, webhookSourceSelect+` AND id = $2`, s.WorkspaceID, id), publicBase())
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the webhook source")
	}
	return &src, nil
}

// ---- cursor ----------------------------------------------------------------------

func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(s string) (time.Time, string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, "", false
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok || id == "" {
		return time.Time{}, "", false
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, "", false
	}
	return t, id, true
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
