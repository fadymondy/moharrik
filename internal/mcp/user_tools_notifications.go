package mcp

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// NotificationTools: the feed (notifications:read) and marking/configuring
// (notifications:write — scopes.go already describes that scope as covering
// both "mark, save" and "configure notification sources and webhooks", so one
// scope gates both here). The secret-sealing here duplicates
// internal/notifications/secret.go's construction exactly (same domain
// string, so a source created from either package can be rotated from the
// other) — this package cannot import internal/notifications's unexported
// seal/unseal, and duplication is this codebase's own convention for a few
// lines shared between otherwise-independent packages (see internal/crm and
// internal/marketing's separate body[T]/out helpers).
func NotificationTools() []Tool {
	return []Tool{
		{
			Name:        "list_notifications",
			Description: "The notification feed, newest first. unread_only defaults to true.",
			Scope:       "notifications:read",
			InputSchema: schema(nil, map[string]any{
				"unread_only": map[string]any{"type": "boolean", "description": "Default true"},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				unreadOnly := true
				var in struct {
					UnreadOnly *bool `json:"unread_only"`
				}
				if len(args) > 0 {
					_ = json.Unmarshal(args, &in)
					if in.UnreadOnly != nil {
						unreadOnly = *in.UnreadOnly
					}
				}
				q := `SELECT source_name, severity, title, actor, occurred_at, read_at IS NOT NULL FROM notification_items WHERE workspace_id = $1`
				if unreadOnly {
					q += ` AND read_at IS NULL`
				}
				q += ` ORDER BY occurred_at DESC LIMIT 100`
				rows, err := a.SQLDB.QueryContext(ctx, q, s.WorkspaceID)
				if err != nil {
					return "", fmt.Errorf("could not list notifications: %w", err)
				}
				defer rows.Close()
				var b strings.Builder
				n := 0
				for rows.Next() {
					var source, severity, title, actor string
					var occurred any
					var read bool
					if err := rows.Scan(&source, &severity, &title, &actor, &occurred, &read); err != nil {
						return "", fmt.Errorf("could not list notifications: %w", err)
					}
					n++
					fmt.Fprintf(&b, "[%s] %s", severity, title)
					if source != "" {
						fmt.Fprintf(&b, " (%s)", source)
					}
					if actor != "" {
						fmt.Fprintf(&b, " by %s", actor)
					}
					b.WriteByte('\n')
				}
				if n == 0 {
					if unreadOnly {
						return "No unread notifications.", nil
					}
					return "No notifications yet.", nil
				}
				return fmt.Sprintf("%d notification(s):\n%s", n, b.String()), nil
			},
		},
		{
			Name:        "mark_notification",
			Description: "Mark a notification read/unread by matching its exact title, or mark every unread notification read.",
			Scope:       "notifications:write",
			InputSchema: schema(nil, map[string]any{
				"title":         prop("string", "Exact title; omit to act on every unread item"),
				"mark_all_read": map[string]any{"type": "boolean", "description": "Mark every unread item read, ignoring title"},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					Title       string `json:"title"`
					MarkAllRead bool   `json:"mark_all_read"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", fmt.Errorf("bad arguments: %w", err)
				}
				if in.MarkAllRead || strings.TrimSpace(in.Title) == "" {
					res, err := a.SQLDB.ExecContext(ctx, `UPDATE notification_items SET read_at = now() WHERE workspace_id = $1 AND read_at IS NULL`, s.WorkspaceID)
					if err != nil {
						return "", fmt.Errorf("could not mark notifications read: %w", err)
					}
					n, _ := res.RowsAffected()
					return fmt.Sprintf("Marked %d notification(s) read.", n), nil
				}
				res, err := a.SQLDB.ExecContext(ctx, `UPDATE notification_items SET read_at = now() WHERE workspace_id = $1 AND lower(title) = lower($2) AND read_at IS NULL`,
					s.WorkspaceID, strings.TrimSpace(in.Title))
				if err != nil {
					return "", fmt.Errorf("could not mark the notification read: %w", err)
				}
				if n, _ := res.RowsAffected(); n == 0 {
					return "No matching unread notification.", nil
				}
				return "Marked read.", nil
			},
		},
		{
			Name:        "list_webhook_sources",
			Description: "This workspace's webhook sources and their status.",
			Scope:       "notifications:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				rows, err := a.SQLDB.QueryContext(ctx, `SELECT name, kind, enabled, delivery_count FROM webhook_sources WHERE workspace_id = $1 ORDER BY lower(name)`, s.WorkspaceID)
				if err != nil {
					return "", fmt.Errorf("could not list webhook sources: %w", err)
				}
				defer rows.Close()
				var b strings.Builder
				n := 0
				for rows.Next() {
					var name, kind string
					var enabled bool
					var deliveries int64
					if err := rows.Scan(&name, &kind, &enabled, &deliveries); err != nil {
						return "", fmt.Errorf("could not list webhook sources: %w", err)
					}
					n++
					status := "enabled"
					if !enabled {
						status = "disabled"
					}
					fmt.Fprintf(&b, "%s [%s, %s] — %d deliveries\n", name, kind, status, deliveries)
				}
				if n == 0 {
					return "No webhook sources yet.", nil
				}
				return fmt.Sprintf("%d source(s):\n%s", n, b.String()), nil
			},
		},
		{
			Name:        "create_webhook_source",
			Description: "Create a webhook source (e.g. for GitHub or Sentry) and return its URL and one-time signing secret.",
			Scope:       "notifications:write",
			InputSchema: schema([]string{"name"}, map[string]any{
				"name": prop("string", ""),
				"kind": map[string]any{"type": "string", "enum": []string{"generic", "github", "sentry"}, "description": "Default generic"},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct{ Name, Kind string }
				if err := json.Unmarshal(args, &in); err != nil {
					return "", fmt.Errorf("bad arguments: %w", err)
				}
				name := strings.TrimSpace(in.Name)
				if name == "" {
					return "", fmt.Errorf("name is required")
				}
				kind := in.Kind
				if kind == "" {
					kind = "generic"
				}
				if kind != "generic" && kind != "github" && kind != "sentry" {
					return "", fmt.Errorf("kind must be generic, github or sentry")
				}
				secretBytes := make([]byte, 32)
				if _, err := rand.Read(secretBytes); err != nil {
					return "", fmt.Errorf("could not generate a secret: %w", err)
				}
				plain := hex.EncodeToString(secretBytes)
				sealed, err := sealWebhookSecret(plain)
				if err != nil {
					return "", err
				}
				var id string
				if err := a.SQLDB.QueryRowContext(ctx, `
					INSERT INTO webhook_sources (workspace_id, name, kind, secret_sealed, created_by)
					VALUES ($1, $2, $3, $4, $5) RETURNING id`,
					s.WorkspaceID, name, kind, sealed, s.UserID,
				).Scan(&id); err != nil {
					return "", fmt.Errorf("could not create the webhook source: %w", err)
				}
				url := publicBaseMCP() + "/api/hooks/" + id
				return fmt.Sprintf("Created %q. URL: %s\nSigning secret (shown once — save it now): %s", name, url, plain), nil
			},
		},
	}
}

func publicBaseMCP() string {
	for _, k := range []string{"PUBLIC_URL", "APP_URL"} {
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv(k)), "/"); v != "" {
			return v
		}
	}
	return "http://localhost:3120"
}

func webhookSecretKey() ([]byte, error) {
	sec := os.Getenv("AUTH_SECRET")
	if len(sec) < 16 {
		return nil, errors.New("AUTH_SECRET is not set; cannot seal a webhook secret")
	}
	k := sha256.Sum256([]byte("moharrik/webhook-secret/v1:" + sec))
	return k[:], nil
}

func sealWebhookSecret(plain string) (string, error) {
	k, err := webhookSecretKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}
