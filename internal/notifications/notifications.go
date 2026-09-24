// Package notifications is Moharrik's notification center: per-workspace
// webhook sources (a named, signed endpoint at POST /api/hooks/{id}) and the
// items they deliver — read, marked read, saved, and filtered by source or
// severity.
//
// Same rules as internal/marketing and internal/crm: every workspace-scoped
// handler runs on the app (RLS) pool AND names workspace_id in every
// predicate; links use composite (workspace_id, id) foreign keys. The one
// exception is POST /api/hooks/{id}, which by definition has no session and
// no X-Workspace-ID — see routes.go's public section for how it still ends up
// correctly scoped (the same pattern internal/marketing uses for the mail
// confirm/unsubscribe links).
package notifications

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// Source kinds. Anything else falls back to "generic" shape parsing.
var sourceKinds = map[string]bool{"generic": true, "github": true, "sentry": true}

var severities = map[string]bool{"info": true, "warning": true, "error": true}

// ---- wire types -----------------------------------------------------------

type WebhookSourceOut struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Kind           string     `json:"kind"`
	Enabled        bool       `json:"enabled"`
	URL            string     `json:"url" doc:"POST here. The signing secret is shown only once, at creation or rotation."`
	DeliveryCount  int64      `json:"delivery_count"`
	LastDeliveryAt *time.Time `json:"last_delivery_at"`
	LastError      string     `json:"last_error"`
	LastErrorAt    *time.Time `json:"last_error_at"`
	// LegacySigning: this source predates MH-381 and still accepts the old
	// body-only signature, which can be replayed. New sources are false.
	LegacySigning bool `json:"legacy_signing" doc:"Still accepts the pre-MH-381 body-only signature (replayable). Switch off with PATCH legacy_signing=false."`
	// LastLegacyAt is the last delivery that verified ONLY by the legacy
	// scheme, which tells the owner whether switching off would break a sender.
	LastLegacyAt *time.Time `json:"last_legacy_at"`
	// HasSecret is false for a sentry source whose Client Secret is not set
	// yet; every delivery is refused until it is.
	HasSecret bool      `json:"has_secret"`
	RotatedAt time.Time `json:"rotated_at"`
	CreatedAt time.Time `json:"created_at"`
}

// WebhookSourceWithSecret is the response for the two moments a plaintext
// secret is ever shown: creating a source and rotating one. A single named
// type, not two anonymous structs with the same shape — huma's schema
// registry names a generated schema after its Go type, and two anonymous
// structs that differ only in a doc string collide under that name and panic
// at startup (caught by the isolation test booting the real stack).
type WebhookSourceWithSecret struct {
	Source WebhookSourceOut `json:"source"`
	Secret string           `json:"secret" doc:"Shown once. Put it wherever the sending platform asks for a webhook signing secret. Empty for sentry, whose secret Sentry generates: set it with PUT /api/w/webhook_sources/{id}/secret."`
}

type NotificationItemOut struct {
	ID       string  `json:"id"`
	SourceID *string `json:"source_id" doc:"The webhook source that delivered it; null for collected and internal items."`
	// FeedSourceID is the collected source (MH-384) that found it.
	FeedSourceID *string `json:"feed_source_id" doc:"The collected source (Bluesky, YouTube, RSS) that found it; null otherwise."`
	// Origin is where it came from: webhook, collected or internal.
	Origin     string     `json:"origin" enum:"webhook,collected,internal"`
	SourceKey  string     `json:"source_key" doc:"The value to filter this item's source by (source_id on the list, mark_all_read)."`
	SourceName string     `json:"source_name"`
	Kind       string     `json:"kind"`
	Severity   string     `json:"severity"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	Actor      string     `json:"actor"`
	URL        string     `json:"url"`
	Payload    string     `json:"payload"`
	OccurredAt time.Time  `json:"occurred_at"`
	ReadAt     *time.Time `json:"read_at"`
	SavedAt    *time.Time `json:"saved_at"`
}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type idPath struct {
	wsHeader
	ID string `path:"id"`
}

type body[T any] struct{ Body T }

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

// ---- helpers ----------------------------------------------------------

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

func notFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func pgCode(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}

func timep(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	v := n.Time
	return &v
}

func strp(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

// update collects SET clauses for a PATCH. Args start after the two fixed
// predicates ($1 = workspace_id, $2 = id).
type update struct {
	sets []string
	args []any
}

func (u *update) set(col string, v any) {
	u.args = append(u.args, v)
	u.sets = append(u.sets, col+" = $"+strconv.Itoa(len(u.args)+2))
}

const webhookSourceSelect = `
	SELECT id, name, kind, enabled, delivery_count, last_delivery_at, last_error, last_error_at,
		legacy_signing, last_legacy_at, secret_sealed <> '', rotated_at, created_at
	FROM webhook_sources WHERE workspace_id = $1`

func scanSource(row scanner, base string) (WebhookSourceOut, error) {
	var s WebhookSourceOut
	var lastDeliveryAt, lastErrorAt, lastLegacyAt sql.NullTime
	if err := row.Scan(&s.ID, &s.Name, &s.Kind, &s.Enabled, &s.DeliveryCount, &lastDeliveryAt, &s.LastError, &lastErrorAt,
		&s.LegacySigning, &lastLegacyAt, &s.HasSecret, &s.RotatedAt, &s.CreatedAt); err != nil {
		return s, err
	}
	s.LastDeliveryAt = timep(lastDeliveryAt)
	s.LastErrorAt = timep(lastErrorAt)
	s.LastLegacyAt = timep(lastLegacyAt)
	s.URL = base + "/api/hooks/" + s.ID
	return s, nil
}

const notificationItemSelect = `
	SELECT id, source_id, feed_source_id, source_name, kind, severity, title, body, actor, url, payload, occurred_at, read_at, saved_at
	FROM notification_items WHERE workspace_id = $1`

func scanItem(row scanner) (NotificationItemOut, error) {
	var it NotificationItemOut
	var sourceID, feedID sql.NullString
	var readAt, savedAt sql.NullTime
	if err := row.Scan(&it.ID, &sourceID, &feedID, &it.SourceName, &it.Kind, &it.Severity, &it.Title, &it.Body, &it.Actor, &it.URL, &it.Payload, &it.OccurredAt, &readAt, &savedAt); err != nil {
		return it, err
	}
	it.SourceID = strp(sourceID)
	it.FeedSourceID = strp(feedID)
	it.Origin, it.SourceKey = originOf(sourceID, feedID, it.SourceName)
	it.ReadAt = timep(readAt)
	it.SavedAt = timep(savedAt)
	return it, nil
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func clampSeverity(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if severities[s] {
		return s
	}
	return "info"
}

// originOf says where an item came from, and the key its source is filtered by.
// A webhook's key is its source id and a collected source's is its id (both
// UUIDs, so they never collide). An item Moharrik raised itself (a lead, a
// YouTube comment) has neither, so its key is "internal:" + its label.
func originOf(sourceID, feedID sql.NullString, name string) (string, string) {
	switch {
	case sourceID.Valid:
		return "webhook", sourceID.String
	case feedID.Valid:
		return "collected", feedID.String
	}
	return "internal", internalKeyPrefix + name
}

const internalKeyPrefix = "internal:"
