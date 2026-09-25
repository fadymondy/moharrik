package mcp

import (
	"context"
	"encoding/json"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/inbox"
)

/*
MailboxTools (MH-555): managing a workspace's several IMAP mailboxes the same
way whatsapp_link manages a linked number — list/create/update/delete, a
connection test, and a manual "collect now". A mailbox's password is
write-only here exactly like over REST: mailboxOut never includes it, only
has_password.
*/

func mailboxOut(m inbox.MailboxOut) map[string]any {
	return map[string]any{
		"id": m.ID, "name": m.Name, "is_primary": m.IsPrimary, "host": m.Host, "port": m.Port,
		"username": m.Username, "has_password": m.HasPassword, "tls_mode": m.TLSMode, "folder": m.Folder,
		"smtp_service_id": m.SMTPServiceID, "smtp_service_name": m.SMTPServiceName, "enabled": m.Enabled,
		"last_polled_at": m.LastPolledAt, "last_error": m.LastError,
		"last_test_at": m.LastTestAt, "last_test_ok": m.LastTestOK, "last_test_error": m.LastTestError,
		"message_count": m.MessageCount, "created_at": m.CreatedAt,
	}
}

func MailboxTools() []Tool {
	return []Tool{
		{
			Name: "list_mailboxes",
			Description: "This workspace's IMAP mailboxes (MH-555): several are allowed, each polled on its own, the way a " +
				"workspace can have several SMTP services. Each carries its id for update_mailbox, delete_mailbox, " +
				"test_mailbox and check_mailbox, and whether it is the primary one (used when a new email thread has no " +
				"mailbox to attach to). Passwords are never returned, only has_password.",
			Scope:       "inbox:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				list, err := inbox.ListMailboxesTool(ctx, a)
				if err != nil {
					return "", err
				}
				out := make([]map[string]any, 0, len(list))
				for _, m := range list {
					out = append(out, mailboxOut(m))
				}
				return jsonResult(map[string]any{"count": len(out), "mailboxes": out})
			},
		},
		{
			Name: "create_mailbox",
			Description: "Add a mailbox to collect email from over IMAP (MH-555). smtp_service_id picks which of the " +
				"workspace's SMTP services (list_smtp_services, if available, or the settings page) a reply on this mailbox's " +
				"conversations goes out through; empty uses the workspace's default. The password is sealed at rest and never " +
				"returned by any tool.",
			Scope: "inbox:manage",
			InputSchema: schema([]string{"host", "username", "password"}, map[string]any{
				"name":            prop("string", "Defaults to \"Mailbox N\""),
				"host":            prop("string", "IMAP host, e.g. imap.gmail.com"),
				"port":            prop("integer", "Defaults to 993"),
				"username":        prop("string", ""),
				"password":        prop("string", ""),
				"tls_mode":        map[string]any{"type": "string", "enum": []string{"tls", "starttls", "none"}, "description": "Defaults to tls"},
				"folder":          prop("string", "Defaults to INBOX"),
				"smtp_service_id": prop("string", "Empty = the workspace's default SMTP service"),
				"is_primary":      map[string]any{"type": "boolean", "description": "The first mailbox is always primary regardless of this"},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				in, err := bindMailboxIn(raw, true)
				if err != nil {
					return "", err
				}
				got, err := inbox.CreateMailboxTool(ctx, a, in)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"mailbox": mailboxOut(got)})
			},
		},
		{
			Name:        "update_mailbox",
			Description: "Update a mailbox (MH-555). An omitted password keeps the one already stored.",
			Scope:       "inbox:manage",
			InputSchema: schema([]string{"id", "host", "username"}, map[string]any{
				"id":              prop("string", "Mailbox id (list_mailboxes)"),
				"name":            prop("string", ""),
				"host":            prop("string", ""),
				"port":            prop("integer", ""),
				"username":        prop("string", ""),
				"password":        prop("string", "Leave empty to keep the stored one"),
				"tls_mode":        map[string]any{"type": "string", "enum": []string{"tls", "starttls", "none"}},
				"folder":          prop("string", ""),
				"smtp_service_id": prop("string", "Empty = the workspace's default SMTP service"),
				"enabled":         map[string]any{"type": "boolean", "description": "false pauses collecting from it"},
				"is_primary":      map[string]any{"type": "boolean", "description": ""},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var idIn struct {
					ID string `json:"id"`
				}
				if err := bind(raw, &idIn); err != nil {
					return "", err
				}
				id, err := required("id", idIn.ID)
				if err != nil {
					return "", err
				}
				in, err := bindMailboxIn(raw, false)
				if err != nil {
					return "", err
				}
				got, err := inbox.UpdateMailboxTool(ctx, a, id, in)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"mailbox": mailboxOut(got)})
			},
		},
		{
			Name:        "delete_mailbox",
			Description: "Remove a mailbox (MH-555). Conversations and messages already collected stay; they only lose their mailbox_id.",
			Scope:       "inbox:manage",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "Mailbox id")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					ID string `json:"id"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				if err := inbox.DeleteMailboxTool(ctx, a, id); err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"deleted": true, "id": id})
			},
		},
		{
			Name: "test_mailbox",
			Description: "Check a mailbox's IMAP credentials by logging in and opening its folder — nothing is collected or " +
				"marked. Use before relying on a newly configured mailbox, or after changing its password.",
			Scope:       "inbox:manage",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "Mailbox id")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					ID string `json:"id"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				got, terr := inbox.TestMailboxTool(ctx, a, id)
				if terr != nil {
					return "", terr
				}
				return jsonResult(map[string]any{"mailbox": mailboxOut(got), "ok": got.LastTestOK != nil && *got.LastTestOK})
			},
		},
		{
			Name:        "check_mailbox",
			Description: "Collect this mailbox's unread mail right now, the same code path the background poller uses.",
			Scope:       "inbox:manage",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "Mailbox id")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					ID string `json:"id"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				filed, cerr, got := inbox.CheckMailboxTool(ctx, a, id)
				if got.ID == "" {
					return "", cerr
				}
				res := map[string]any{"filed": filed, "mailbox": mailboxOut(got)}
				if cerr != nil {
					res["error"] = cerr.Error()
				}
				return jsonResult(res)
			},
		},
	}
}

func bindMailboxIn(raw json.RawMessage, create bool) (inbox.MailboxIn, error) {
	var in inbox.MailboxIn
	if err := bind(raw, &in); err != nil {
		return in, err
	}
	if create {
		if _, err := required("host", in.Host); err != nil {
			return in, err
		}
		if _, err := required("username", in.Username); err != nil {
			return in, err
		}
		if _, err := required("password", in.Password); err != nil {
			return in, err
		}
	}
	return in, nil
}
