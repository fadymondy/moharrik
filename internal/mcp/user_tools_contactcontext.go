package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/contactcontext"
)

/*
ContactContextTools (MH-586): get_contact_context / ask_about_contact /
add_contact_context / generate_contact_report / import_contact_whatsapp_*.

Shares internal/contactcontext with the REST routes (routes.go) — one
retrieval function, one ask/report path, one rate limiter — so an agent's
answer is grounded in exactly the same passages a member sees in the UI.
ask_about_contact and generate_contact_report both work with no AI provider
configured: they answer from the raw retrieved/gathered facts instead of
erroring (Answer.NoProvider / ContactReport.NoProvider say so explicitly).

Reuses contacts:read / contacts:write rather than adding new scopes — this is
still contact data, just a different shape of it.
*/

func ContactContextTools() []Tool {
	return []Tool{
		{
			Name: "get_contact_context",
			Description: "Everything on record for a contact in one call: recent notes/files, recent messages across every channel, " +
				"WhatsApp exports imported, and open deals. Use before ask_about_contact when you want the raw picture rather than an answer to one question.",
			Scope: "contacts:read",
			InputSchema: schema([]string{"contact_id"}, map[string]any{
				"contact_id": prop("string", "The contact's id"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				m := decodeArgs(raw)
				contactID, err := required("contact_id", str(m, "contact_id"))
				if err != nil {
					return "", err
				}
				sum, err := contactcontext.GetContextSummary(ctx, a, s.WorkspaceID, contactID)
				if err != nil {
					return "", err
				}
				return jsonResult(sum)
			},
		},
		{
			Name: "ask_about_contact",
			Description: "Ask a free-text question about a contact, grounded ONLY in their retrieved context (notes, messages, deals), with citations. " +
				"Works even with no AI provider configured: the answer is then the raw relevant passages instead of a generated sentence " +
				"(see \"no_provider\" in the reply) — this never errors just because no model is set up.",
			Scope: "contacts:read",
			InputSchema: schema([]string{"contact_id", "question"}, map[string]any{
				"contact_id": prop("string", "The contact's id"),
				"question":   prop("string", "The free-text question to answer about this contact"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				m := decodeArgs(raw)
				contactID, err := required("contact_id", str(m, "contact_id"))
				if err != nil {
					return "", err
				}
				question, err := required("question", str(m, "question"))
				if err != nil {
					return "", err
				}
				ans, err := contactcontext.AskAboutContact(ctx, a, contactcontext.ResolveAIClient(a.Log), s.WorkspaceID, contactID, question)
				if err != nil {
					return "", err
				}
				return jsonResult(ans)
			},
		},
		{
			Name: "add_contact_context",
			Description: "Add free-text context to a contact: a note, or a small text/markdown file's decoded content. " +
				"Indexed immediately for get_contact_context / ask_about_contact / generate_contact_report.",
			Scope: "contacts:write",
			InputSchema: schema([]string{"contact_id", "content"}, map[string]any{
				"contact_id": prop("string", "The contact's id"),
				"kind":       prop("string", "\"note\" (default) or \"file\""),
				"title":      prop("string", "Optional short title"),
				"content":    prop("string", "The note text, or the file's text content"),
				"filename":   prop("string", "Set when kind=\"file\""),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				m := decodeArgs(raw)
				contactID, err := required("contact_id", str(m, "contact_id"))
				if err != nil {
					return "", err
				}
				content, err := required("content", str(m, "content"))
				if err != nil {
					return "", err
				}
				id, err := contactcontext.AddNote(ctx, a, s.WorkspaceID, contactID, str(m, "kind"), str(m, "title"), content, str(m, "filename"), s.UserID)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"id": id})
			},
		},
		{
			Name: "generate_contact_report",
			Description: "Generate a new, versioned relationship report for a contact: summary, topics, open promises, sentiment, last touch, " +
				"recommended next step, and a cited timeline underneath. Every claim traces to a source id. Works deterministically with no AI " +
				"provider configured — see \"no_provider\" in the reply.",
			Scope: "contacts:write",
			InputSchema: schema([]string{"contact_id"}, map[string]any{
				"contact_id": prop("string", "The contact's id"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				m := decodeArgs(raw)
				contactID, err := required("contact_id", str(m, "contact_id"))
				if err != nil {
					return "", err
				}
				rep, err := contactcontext.GenerateReport(ctx, a, contactcontext.ResolveAIClient(a.Log), s.WorkspaceID, contactID, s.UserID)
				if err != nil {
					return "", err
				}
				return jsonResult(rep)
			},
		},
		{
			Name:        "list_contact_reports",
			Description: "List a contact's previously generated relationship reports, newest version first.",
			Scope:       "contacts:read",
			InputSchema: schema([]string{"contact_id"}, map[string]any{
				"contact_id": prop("string", "The contact's id"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				m := decodeArgs(raw)
				contactID, err := required("contact_id", str(m, "contact_id"))
				if err != nil {
					return "", err
				}
				list, err := contactcontext.ListReports(ctx, a, s.WorkspaceID, contactID)
				if err != nil {
					return "", err
				}
				return jsonResult(list)
			},
		},
		{
			Name: "import_contact_whatsapp_preview",
			Description: "Preview a WhatsApp export (.txt or a .zip export) for a contact before committing it: message/media counts, " +
				"senders found, a short sample, and how many of the sampled messages already exist for this contact. Writes nothing.",
			Scope: "contacts:read",
			InputSchema: schema([]string{"contact_id", "filename", "data_base64"}, map[string]any{
				"contact_id":  prop("string", "The contact's id"),
				"filename":    prop("string", "Original filename (.txt or .zip)"),
				"data_base64": prop("string", "Base64-encoded file bytes"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				if !contactcontext.CheckRateLimit(s.WorkspaceID) {
					return "", errTooManyContactContextImports
				}
				m := decodeArgs(raw)
				contactID, err := required("contact_id", str(m, "contact_id"))
				if err != nil {
					return "", err
				}
				filename, err := required("filename", str(m, "filename"))
				if err != nil {
					return "", err
				}
				data, err := base64.StdEncoding.DecodeString(str(m, "data_base64"))
				if err != nil {
					return "", err
				}
				sum, err := contactcontext.GetContextSummary(ctx, a, s.WorkspaceID, contactID)
				if err != nil {
					return "", err
				}
				pv, err := contactcontext.BuildPreview(ctx, a, s.WorkspaceID, contactID, filename, data, sum.Contact.Name)
				if err != nil {
					return "", err
				}
				return jsonResult(pv)
			},
		},
		{
			Name: "import_contact_whatsapp_commit",
			Description: "Import a previewed WhatsApp export into a contact's conversation. Re-committing the same bytes adds nothing " +
				"(deduped by minute+sender+text). This NEVER triggers automations, notifications or announcements — imported history is filed silently.",
			Scope: "contacts:write",
			InputSchema: schema([]string{"contact_id", "filename", "data_base64", "contact_sender"}, map[string]any{
				"contact_id":     prop("string", "The contact's id"),
				"filename":       prop("string", "Original filename (.txt or .zip) — the same one passed to preview"),
				"data_base64":    prop("string", "Base64-encoded file bytes — the same bytes passed to preview"),
				"contact_sender": prop("string", "Which sender name in the export is this contact (from the preview's senders list)"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				if !contactcontext.CheckRateLimit(s.WorkspaceID) {
					return "", errTooManyContactContextImports
				}
				m := decodeArgs(raw)
				contactID, err := required("contact_id", str(m, "contact_id"))
				if err != nil {
					return "", err
				}
				filename, err := required("filename", str(m, "filename"))
				if err != nil {
					return "", err
				}
				contactSender, err := required("contact_sender", str(m, "contact_sender"))
				if err != nil {
					return "", err
				}
				data, err := base64.StdEncoding.DecodeString(str(m, "data_base64"))
				if err != nil {
					return "", err
				}
				res, err := contactcontext.Commit(ctx, a, s.WorkspaceID, contactID, s.UserID, filename, data, contactSender)
				if err != nil {
					return "", err
				}
				return jsonResult(res)
			},
		},
		{
			Name:        "remove_contact_whatsapp_import",
			Description: "Remove one previously committed WhatsApp import: deletes exactly its messages, their attachments, and any notes tagged with it.",
			Scope:       "contacts:write",
			InputSchema: schema([]string{"contact_id", "import_id"}, map[string]any{
				"contact_id": prop("string", "The contact's id"),
				"import_id":  prop("string", "The import's id (from preview/commit's reply, or get_contact_context's imports list)"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				m := decodeArgs(raw)
				contactID, err := required("contact_id", str(m, "contact_id"))
				if err != nil {
					return "", err
				}
				importID, err := required("import_id", str(m, "import_id"))
				if err != nil {
					return "", err
				}
				if err := contactcontext.RemoveImport(ctx, a, s.WorkspaceID, contactID, importID); err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"removed": importID})
			},
		},
	}
}

var errTooManyContactContextImports = &toolError{"too many WhatsApp import operations for this workspace in the last minute — try again shortly"}
