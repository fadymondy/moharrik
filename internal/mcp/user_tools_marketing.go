package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// MarketingTools: the mailing lists and campaigns an agent can read, plus the
// two writes that matter — draft a campaign and send it.
//
// send_campaign calls internal/marketing.StartSend, the SAME worker the REST
// route runs, rather than a second thinner implementation of a paced SMTP job
// with bounce handling. It returns as soon as the send is claimed and running;
// list_campaigns carries the progress, so "how far has it got" is a read.
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
		{
			Name:        "create_campaign",
			Description: "Draft an email campaign against a mailing list (named by its exact name). It is not sent until send_campaign.",
			Scope:       "campaigns:write",
			InputSchema: schema([]string{"list", "subject", "body"}, map[string]any{
				"list":    prop("string", "The mailing list's exact name"),
				"subject": prop("string", ""),
				"body":    prop("string", "Markdown"),
				"locale":  map[string]any{"type": "string", "enum": []string{"en", "ar"}, "description": "Default en"},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct{ List, Subject, Body, Locale string }
				if err := json.Unmarshal(args, &in); err != nil {
					return "", fmt.Errorf("bad arguments: %w", err)
				}
				subject := strings.TrimSpace(in.Subject)
				if subject == "" || strings.TrimSpace(in.Body) == "" {
					return "", fmt.Errorf("subject and body are required")
				}
				locale := in.Locale
				if locale == "" {
					locale = "en"
				}
				if locale != "en" && locale != "ar" {
					return "", fmt.Errorf("locale must be en or ar")
				}
				var listID, listName string
				if err := a.SQLDB.QueryRowContext(ctx, `SELECT id, name FROM mail_lists WHERE workspace_id = $1 AND lower(name) = lower($2)`,
					s.WorkspaceID, strings.TrimSpace(in.List)).Scan(&listID, &listName); err != nil {
					if errNoRows(err) {
						return "", fmt.Errorf("no mailing list named %q — call list_mail_lists to see them", in.List)
					}
					return "", fmt.Errorf("could not resolve the list: %w", err)
				}
				var id string
				if err := a.SQLDB.QueryRowContext(ctx, `
					INSERT INTO campaigns (workspace_id, list_id, subject, body, locale, created_by)
					VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
					s.WorkspaceID, listID, subject, strings.TrimSpace(in.Body), locale, s.UserID,
				).Scan(&id); err != nil {
					return "", fmt.Errorf("could not create the campaign: %w", err)
				}
				var recipients int
				_ = a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM list_subscriptions WHERE workspace_id = $1 AND list_id = $2 AND status = 'subscribed'`,
					s.WorkspaceID, listID).Scan(&recipients)
				return fmt.Sprintf("Drafted %q for %s (%d confirmed subscriber(s)). Call send_campaign to send it.", subject, listName, recipients), nil
			},
		},
		{
			Name:        "send_campaign",
			Description: "Send a drafted campaign, by its exact subject. Returns as soon as sending starts; poll list_campaigns for progress.",
			Scope:       "campaigns:write",
			InputSchema: schema([]string{"subject"}, map[string]any{
				"subject": prop("string", "The draft campaign's exact subject"),
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct{ Subject string }
				if err := json.Unmarshal(args, &in); err != nil {
					return "", fmt.Errorf("bad arguments: %w", err)
				}
				var id, status string
				if err := a.SQLDB.QueryRowContext(ctx, `
					SELECT id, status FROM campaigns WHERE workspace_id = $1 AND lower(subject) = lower($2)
					ORDER BY created_at DESC LIMIT 1`,
					s.WorkspaceID, strings.TrimSpace(in.Subject)).Scan(&id, &status); err != nil {
					if errNoRows(err) {
						return "", fmt.Errorf("no campaign with the subject %q", in.Subject)
					}
					return "", fmt.Errorf("could not resolve the campaign: %w", err)
				}
				if status != "draft" && status != "failed" {
					return "", fmt.Errorf("that campaign is already %s", status)
				}
				// Resolve the service first, so a misconfiguration is reported here
				// rather than discovered halfway through the list.
				svc, err := marketing.LoadSMTP(ctx, a.SQLDB, s.WorkspaceID)
				if errors.Is(err, marketing.ErrNoService) {
					return "", fmt.Errorf("configure SMTP for this workspace before sending")
				}
				if err != nil {
					return "", fmt.Errorf("could not read the SMTP settings: %w", err)
				}
				if err := marketing.StartSend(ctx, a, s.WorkspaceID, id, svc); err != nil {
					return "", err
				}
				return fmt.Sprintf("Sending %q. Poll list_campaigns for progress.", strings.TrimSpace(in.Subject)), nil
			},
		},
	}
}
