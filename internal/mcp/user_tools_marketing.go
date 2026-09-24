package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/paging"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// MarketingTools: the mailing lists and campaigns an agent can read and preview, plus the
// writes that matter — draft a campaign, edit it, and send it.
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
			// MH-467: this stopped at 200 campaigns without saying so. It now
			// reads through marketing.ListCampaignsPage, the page the campaigns
			// screen uses, and says how many there are and how to get more.
			Name: "list_campaigns",
			Description: "Campaigns and their delivery progress, newest first, one page at a time. " +
				"The reply says how many there are in total; pass its cursor to get the next page.",
			Scope: "campaigns:read",
			InputSchema: schema(nil, map[string]any{
				"cursor": prop("string", "The cursor from the previous reply, for the next page"),
				"limit":  prop("number", "Page size, default 50, at most 200"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				args := decodeArgs(raw)
				limit, err := paging.Limit(intArg(args, "limit", 0), marketing.DefaultCampaignPage, marketing.MaxCampaignPage)
				if err != nil {
					return "", err
				}
				page, err := marketing.ListCampaignsPage(ctx, a.SQLDB, s.WorkspaceID, str(args, "cursor"), limit)
				if err != nil {
					return "", fmt.Errorf("could not list campaigns: %w", err)
				}
				if page.Total == 0 {
					return "No campaigns yet.", nil
				}
				var b strings.Builder
				for _, c := range page.Campaigns {
					fmt.Fprintf(&b, "%s -> %s [%s, %s] %d/%d sent", c.Subject, c.ListName, c.Channel, c.Status, c.SentCount, c.RecipientCount)
					if c.FailedCount > 0 {
						fmt.Fprintf(&b, ", %d failed", c.FailedCount)
					}
					b.WriteByte('\n')
				}
				head := fmt.Sprintf("Showing %d of %d campaign(s):\n", len(page.Campaigns), page.Total)
				if page.NextCursor != "" {
					return head + b.String() + "More: call list_campaigns again with cursor=" + page.NextCursor, nil
				}
				return head + b.String(), nil
			},
		},
		{
			Name:        "create_campaign",
			Description: "Draft an email campaign against a mailing list (named by its exact name). It is not sent until send_campaign.",
			Scope:       "campaigns:write",
			InputSchema: schema([]string{"list", "subject", "body"}, map[string]any{
				"list":    prop("string", "The mailing list's exact name"),
				"subject": prop("string", ""),
				"body":         prop("string", "Markdown"),
				"locale":       map[string]any{"type": "string", "enum": []string{"en", "ar"}, "description": "Default en"},
				"smtp_service": prop("string", "The SMTP service to send through, by its exact name (list_smtp_services). Omit for the campaign default."),
				"audience_locale": map[string]any{"type": "string", "enum": []string{"", "en", "ar"},
					"description": "Send only to contacts in this language; omit for everyone on the list"},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					List, Subject, Body, Locale string
					SMTPService                 string `json:"smtp_service"`
					AudienceLocale              string `json:"audience_locale"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", fmt.Errorf("bad arguments: %w", err)
				}
				svcID, err := smtpServiceByName(ctx, a, s.WorkspaceID, in.SMTPService)
				if err != nil {
					return "", err
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
					INSERT INTO campaigns (workspace_id, list_id, subject, body, locale, created_by, smtp_service_id, audience_locale)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
					s.WorkspaceID, listID, subject, strings.TrimSpace(in.Body), locale, s.UserID, svcID, marketing.AudienceLocale(in.AudienceLocale),
				).Scan(&id); err != nil {
					return "", fmt.Errorf("could not create the campaign: %w", err)
				}
				recipients, _ := marketing.CountEmailAudienceFor(ctx, a.SQLDB, s.WorkspaceID, listID, in.AudienceLocale)
				skipped, _ := marketing.CountSkippedEmailAudienceFor(ctx, a.SQLDB, s.WorkspaceID, listID, in.AudienceLocale)
				msg := fmt.Sprintf("Drafted %q for %s (%d confirmed subscriber(s)).", subject, listName, recipients)
				if skipped > 0 {
					msg += fmt.Sprintf(" %d subscriber(s) will be skipped — they have no email.", skipped)
				}
				return msg + " Call send_campaign to send it.", nil
			},
		},
		{
			// MH-407: the same UpdateCampaign the REST PUT runs, so an agent is
			// refused at the same moment a person is — once sending has started.
			Name:        "update_campaign",
			Description: "Edit a campaign that has not gone out yet (a draft, or a failed one awaiting retry), found by its exact current subject. Only the fields you pass change. Refused once sending has started.",
			Scope:       "campaigns:write",
			InputSchema: schema([]string{"subject"}, map[string]any{
				"subject":     prop("string", "The campaign's exact current subject"),
				"new_subject": prop("string", "A new subject (or internal name, for WhatsApp)"),
				"body":        prop("string", "The new body. Markdown for email; plain text for WhatsApp"),
				"list":        prop("string", "Move it to this mailing list (exact name)"),
				"locale":      map[string]any{"type": "string", "enum": []string{"en", "ar"}},
				"channel":     map[string]any{"type": "string", "enum": []string{"email", "whatsapp"}},
				"smtp_service": prop("string", "Send through this SMTP service (exact name, list_smtp_services); an empty string returns it to the campaign default"),
				"audience_locale": map[string]any{"type": "string", "enum": []string{"", "en", "ar"},
					"description": "Send only to contacts in this language; an empty string sends to everyone"},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var in struct {
					Subject    string  `json:"subject"`
					NewSubject *string `json:"new_subject"`
					Body       *string `json:"body"`
					List       *string `json:"list"`
					Locale     *string `json:"locale"`
					Channel    *string `json:"channel"`
					SMTP       *string `json:"smtp_service"`
					Audience   *string `json:"audience_locale"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return "", fmt.Errorf("bad arguments: %w", err)
				}
				var id string
				var cur marketing.CampaignInput
				if err := a.SQLDB.QueryRowContext(ctx, `
					SELECT id, list_id, channel, subject, body, locale, smtp_service_id, audience_locale FROM campaigns
					WHERE workspace_id = $1 AND lower(subject) = lower($2)
					ORDER BY created_at DESC LIMIT 1`,
					s.WorkspaceID, strings.TrimSpace(in.Subject)).Scan(&id, &cur.ListID, &cur.Channel, &cur.Subject, &cur.Body, &cur.Locale, &cur.SMTPServiceID, &cur.AudienceLocale); err != nil {
					if errNoRows(err) {
						return "", fmt.Errorf("no campaign with the subject %q — call list_campaigns to see them", in.Subject)
					}
					return "", fmt.Errorf("could not resolve the campaign: %w", err)
				}
				if in.NewSubject != nil {
					cur.Subject = *in.NewSubject
				}
				if in.Body != nil {
					cur.Body = *in.Body
				}
				if in.Locale != nil {
					if *in.Locale != "en" && *in.Locale != "ar" {
						return "", fmt.Errorf("locale must be en or ar")
					}
					cur.Locale = *in.Locale
				}
				if in.Channel != nil {
					if *in.Channel != "email" && *in.Channel != "whatsapp" {
						return "", fmt.Errorf("channel must be email or whatsapp")
					}
					cur.Channel = *in.Channel
				}
				if in.Audience != nil {
					if *in.Audience != "" && *in.Audience != "en" && *in.Audience != "ar" {
						return "", fmt.Errorf("audience_locale must be en, ar or empty")
					}
					cur.AudienceLocale = *in.Audience
				}
				if in.SMTP != nil {
					svcID, err := smtpServiceByName(ctx, a, s.WorkspaceID, *in.SMTP)
					if err != nil {
						return "", err
					}
					cur.SMTPServiceID = svcID
				}
				if in.List != nil {
					if err := a.SQLDB.QueryRowContext(ctx, `SELECT id FROM mail_lists WHERE workspace_id = $1 AND lower(name) = lower($2)`,
						s.WorkspaceID, strings.TrimSpace(*in.List)).Scan(&cur.ListID); err != nil {
						if errNoRows(err) {
							return "", fmt.Errorf("no mailing list named %q — call list_mail_lists to see them", *in.List)
						}
						return "", fmt.Errorf("could not resolve the list: %w", err)
					}
				}
				if err := marketing.UpdateCampaign(ctx, a.SQLDB, s.WorkspaceID, id, cur); err != nil {
					return "", err
				}
				return fmt.Sprintf("Saved %q. Call preview_campaign to see it as recipients will.", strings.TrimSpace(cur.Subject)), nil
			},
		},
		{
			// MH-407: rendered by marketing.RenderPreview, which is the send
			// path's own renderer — not a description of it.
			Name:        "preview_campaign",
			Description: "Show a campaign exactly as recipients get it, found by its exact subject: the email's subject, plain text and HTML (in its language, right-to-left for Arabic), or the WhatsApp message with the stop line every broadcast carries.",
			Scope:       "campaigns:read",
			InputSchema: schema([]string{"subject"}, map[string]any{
				"subject": prop("string", "The campaign's exact subject"),
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
				var channel, subject, body, locale, status string
				if err := a.SQLDB.QueryRowContext(ctx, `
					SELECT channel, subject, body, locale, status FROM campaigns
					WHERE workspace_id = $1 AND lower(subject) = lower($2)
					ORDER BY created_at DESC LIMIT 1`,
					s.WorkspaceID, strings.TrimSpace(in.Subject)).Scan(&channel, &subject, &body, &locale, &status); err != nil {
					if errNoRows(err) {
						return "", fmt.Errorf("no campaign with the subject %q", in.Subject)
					}
					return "", fmt.Errorf("could not resolve the campaign: %w", err)
				}
				p := marketing.RenderPreview(channel, locale, subject, body)
				var b strings.Builder
				fmt.Fprintf(&b, "%s campaign, %s (%s), %s.\n\n", p.Channel, p.Locale, p.Dir, status)
				if p.Channel == "whatsapp" {
					b.WriteString("The WhatsApp message, as delivered:\n\n")
					b.WriteString(p.Text)
					return b.String(), nil
				}
				fmt.Fprintf(&b, "Subject: %s\n\nPlain-text part:\n\n%s\n\nHTML part:\n\n%s", p.Subject, p.Text, p.HTML)
				return b.String(), nil
			},
		},
		{
			// MH-412: names and status only. The host, the sender, the username
			// and the password are the workspace's mail credentials and never
			// leave the settings page.
			Name: "list_smtp_services",
			Description: "The workspace's SMTP services by name: which is the default for confirmation and reply emails, " +
				"which is the campaign default, and whether each one's last test worked. Never includes credentials.",
			Scope:       "campaigns:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				list, err := marketing.ListSMTPServices(ctx, a.SQLDB, s.WorkspaceID)
				if err != nil {
					return "", fmt.Errorf("could not list SMTP services")
				}
				if len(list) == 0 {
					return "No SMTP services yet. A member with the mail settings permission adds one under Settings → Email.", nil
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d SMTP service(s):\n", len(list))
				for _, o := range list {
					var roles []string
					if o.IsDefault {
						roles = append(roles, "default for confirmations and replies")
					}
					if o.IsCampaignDefault {
						roles = append(roles, "campaign default")
					}
					test := "never tested"
					if o.LastTestOK != nil && *o.LastTestOK {
						test = "last test worked"
					} else if o.LastTestOK != nil {
						test = "last test failed"
					}
					fmt.Fprintf(&b, "- %s", o.Name)
					if len(roles) > 0 {
						fmt.Fprintf(&b, " (%s)", strings.Join(roles, ", "))
					}
					fmt.Fprintf(&b, ": %s; %d campaign(s) name it\n", test, o.Campaigns)
				}
				return b.String(), nil
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
				// rather than discovered halfway through the list: the campaign's
				// own SMTP service, else the campaign default (MH-412).
				svc, err := marketing.LoadCampaignSMTP(ctx, a.SQLDB, s.WorkspaceID, id)
				if errors.Is(err, marketing.ErrNoService) {
					return "", fmt.Errorf("configure SMTP for this workspace before sending")
				}
				if err != nil {
					return "", fmt.Errorf("could not read the SMTP settings: %w", err)
				}
				if err := marketing.StartSend(ctx, a, s.WorkspaceID, id, svc); err != nil {
					return "", err
				}
				return fmt.Sprintf("Sending %q through the SMTP service %q. Poll list_campaigns for progress.", strings.TrimSpace(in.Subject), svc.ServiceName), nil
			},
		},
	}
}

// smtpServiceByName resolves a service an agent named (MH-412). "" is the
// campaign default (nil).
func smtpServiceByName(ctx context.Context, a *app.App, wsID, name string) (*string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	var id string
	if err := a.SQLDB.QueryRowContext(ctx, `SELECT id FROM smtp_services WHERE workspace_id = $1 AND lower(name) = lower($2)`,
		wsID, name).Scan(&id); err != nil {
		if errNoRows(err) {
			return nil, fmt.Errorf("no SMTP service named %q — call list_smtp_services to see them", name)
		}
		return nil, fmt.Errorf("could not resolve the SMTP service")
	}
	return &id, nil
}
