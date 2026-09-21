package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// MarketingTools: read-only for P0. Sending a campaign runs a paced background
// job with SMTP delivery and per-recipient bounce handling
// (internal/marketing/handlers.go) — worth exposing over MCP, but as its own
// piece of work once there is a story for a long-running tool call, not
// duplicated here as a second, thinner implementation of the same job.
func MarketingTools() []Tool {
	return []Tool{
		{
			Name:        "list_mail_lists",
			Description: "Mailing lists, with subscriber and pending counts.",
			Scope:       "contacts:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				rows, err := a.SQLDB.QueryContext(ctx, `
					SELECT l.name, l.double_opt_in,
					       count(sub.id) FILTER (WHERE sub.status = 'subscribed'),
					       count(sub.id) FILTER (WHERE sub.status = 'pending')
					FROM mail_lists l LEFT JOIN list_subscriptions sub ON sub.list_id = l.id AND sub.workspace_id = l.workspace_id
					WHERE l.workspace_id = $1 GROUP BY l.id ORDER BY lower(l.name)`, s.WorkspaceID)
				if err != nil {
					return "", fmt.Errorf("could not list mailing lists: %w", err)
				}
				defer rows.Close()
				var b strings.Builder
				n := 0
				for rows.Next() {
					var name string
					var doi bool
					var subscribed, pending int
					if err := rows.Scan(&name, &doi, &subscribed, &pending); err != nil {
						return "", fmt.Errorf("could not list mailing lists: %w", err)
					}
					n++
					fmt.Fprintf(&b, "%s — %d subscribed, %d pending", name, subscribed, pending)
					if doi {
						b.WriteString(" (double opt-in)")
					}
					b.WriteByte('\n')
				}
				if n == 0 {
					return "No mailing lists yet.", nil
				}
				return fmt.Sprintf("%d list(s):\n%s", n, b.String()), nil
			},
		},
		{
			Name:        "list_campaigns",
			Description: "Email campaigns and their delivery progress.",
			Scope:       "campaigns:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				rows, err := a.SQLDB.QueryContext(ctx, `
					SELECT c.subject, l.name, c.status, c.recipient_count, c.sent_count, c.failed_count
					FROM campaigns c JOIN mail_lists l ON l.id = c.list_id AND l.workspace_id = c.workspace_id
					WHERE c.workspace_id = $1 ORDER BY c.created_at DESC LIMIT 200`, s.WorkspaceID)
				if err != nil {
					return "", fmt.Errorf("could not list campaigns: %w", err)
				}
				defer rows.Close()
				var b strings.Builder
				n := 0
				for rows.Next() {
					var subject, list, status string
					var recipients, sent, failed int
					if err := rows.Scan(&subject, &list, &status, &recipients, &sent, &failed); err != nil {
						return "", fmt.Errorf("could not list campaigns: %w", err)
					}
					n++
					fmt.Fprintf(&b, "%s -> %s [%s] %d/%d sent", subject, list, status, sent, recipients)
					if failed > 0 {
						fmt.Fprintf(&b, ", %d failed", failed)
					}
					b.WriteByte('\n')
				}
				if n == 0 {
					return "No campaigns yet.", nil
				}
				return fmt.Sprintf("%d campaign(s):\n%s", n, b.String()), nil
			},
		},
	}
}
