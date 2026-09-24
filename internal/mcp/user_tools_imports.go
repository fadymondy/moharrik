package mcp

import (
	"context"
	"encoding/json"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/imports"
)

/*
ImportTools (MH-562): the generic "import a connection's chats/senders into
contacts and inbox threads" surface — inbox_import_sources, inbox_import_preview,
inbox_import, inbox_chat_summary — plus the WhatsApp-named aliases the card
asked for (whatsapp_chats, whatsapp_groups, whatsapp_import, whatsapp_chat_summary),
kept for anyone already using them and for a client that only thinks in
WhatsApp terms.

Every tool calls internal/imports, the same package the REST routes under
/api/w/import/* call — one engine, so an agent's import behaves exactly like a
member's click.
*/

// Shared argument descriptions (MH-562), kept as constants so the same
// English text — and so the same one Arabic translation in docs_imports.go —
// covers every tool that takes it.
const (
	descImportSource         = "A source from inbox_import_sources, e.g. \"whatsapp\""
	descImportConnID         = "A connection id from inbox_import_sources; omit for the source's default connection"
	descImportQ              = "Filter by name/number/address"
	descImportKind           = "direct = one person, group = a group chat/thread"
	descImportFlagged        = "Include items the source flagged (e.g. email's mailing lists/no-reply senders) — off by default"
	descImportItems          = "Item ids from inbox_import_preview"
	descImportHistoryDepth   = "Messages per conversation, newest first; omit for the source's default"
	descImportParticipants   = "Group items only: also create contacts for participants"
	descImportConversationID = "A conversation id, from get_conversation or an inbox_import result"
)

func importInput(m map[string]any) imports.Filters {
	f := imports.Filters{Q: str(m, "q"), Kind: str(m, "kind")}
	if b, ok := m["include_flagged"].(bool); ok {
		f.IncludeFlagged = b
	}
	return f
}

func ImportTools() []Tool {
	previewFn := func(source string) func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
		return func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
			s, err := need(ctx)
			if err != nil {
				return "", err
			}
			m := decodeArgs(raw)
			f := importInput(m)
			items, err := imports.Preview(ctx, a, s.WorkspaceID, source, str(m, "connection_id"), f)
			if err != nil {
				return "", err
			}
			return jsonResult(map[string]any{"source": source, "items": items})
		}
	}
	importFn := func(fixedSource string) func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
		return func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
			s, err := need(ctx)
			if err != nil {
				return "", err
			}
			var in struct {
				Source             string   `json:"source"`
				ConnectionID       string   `json:"connection_id"`
				Items              []string `json:"items"`
				HistoryDepth       int      `json:"history_depth"`
				ImportParticipants bool     `json:"import_participants"`
			}
			if err := bind(raw, &in); err != nil {
				return "", err
			}
			source := fixedSource
			if source == "" {
				source = in.Source
			}
			source, err = required("source", source)
			if err != nil {
				return "", err
			}
			job, err := imports.StartImport(ctx, a, s.WorkspaceID, source, imports.ImportRequest{
				ConnectionID: in.ConnectionID, Items: in.Items, HistoryDepth: in.HistoryDepth, ImportParticipants: in.ImportParticipants,
			})
			if err != nil {
				return "", err
			}
			return jsonResult(map[string]any{"job": job})
		}
	}
	summaryFn := func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
		s, err := need(ctx)
		if err != nil {
			return "", err
		}
		id, err := required("conversation_id", str(decodeArgs(raw), "conversation_id"))
		if err != nil {
			return "", err
		}
		sum, err := imports.ChatSummary(ctx, a, s.WorkspaceID, id)
		if err != nil {
			return "", err
		}
		return jsonResult(map[string]any{"summary": sum})
	}

	return []Tool{
		{
			Name: "inbox_import_sources",
			Description: "Every import source this app knows (whatsapp, email, and any MH-563/564 channels), with the workspace's " +
				"connections for each and whether they are ready to import from.",
			Scope:       "inbox:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				sources, err := imports.ListSources(ctx, a, s.WorkspaceID)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"sources": sources})
			},
		},
		{
			Name: "inbox_import_preview",
			Description: "List one connection's importable chats/senders with their last message, before anything is written. " +
				"already_imported is true when that chat/thread is already an inbox conversation (a re-import would just add it, not duplicate it).",
			Scope: "inbox:read",
			InputSchema: schema([]string{"source"}, map[string]any{
				"source":          prop("string", descImportSource),
				"connection_id":   prop("string", descImportConnID),
				"q":               prop("string", descImportQ),
				"kind":            map[string]any{"type": "string", "enum": []string{"", "direct", "group"}, "description": descImportKind},
				"include_flagged": map[string]any{"type": "boolean", "description": descImportFlagged},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				m := decodeArgs(raw)
				source, err := required("source", str(m, "source"))
				if err != nil {
					return "", err
				}
				items, err := imports.Preview(ctx, a, s.WorkspaceID, source, str(m, "connection_id"), importInput(m))
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"source": source, "items": items})
			},
		},
		{
			Name: "inbox_import",
			Description: "Import the selected items (ids from inbox_import_preview) from one connection: contacts (MH-561's dedup — " +
				"an existing phone/email/handle is reused, not duplicated) plus inbox threads with their history. Idempotent: re-importing " +
				"an already-imported chat adds nothing twice. Runs in the background; poll the returned job with inbox_import_sources' " +
				"sibling status (the job id) — no opt-in to anything is implied by an import.",
			Scope: "inbox:write",
			InputSchema: schema([]string{"source", "items"}, map[string]any{
				"source":              prop("string", descImportSource),
				"connection_id":       prop("string", descImportConnID),
				"items":               map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": descImportItems},
				"history_depth":       map[string]any{"type": "integer", "description": descImportHistoryDepth},
				"import_participants": map[string]any{"type": "boolean", "description": descImportParticipants},
			}),
			Run: importFn(""),
		},
		{
			Name:        "inbox_chat_summary",
			Description: "One conversation's analytics: message/participant counts, in/out split, first and last message time.",
			Scope:       "inbox:read",
			InputSchema: schema([]string{"conversation_id"}, map[string]any{"conversation_id": prop("string", descImportConversationID)}),
			Run:         summaryFn,
		},
		// ---- WhatsApp-named aliases -------------------------------------------
		{
			Name:        "whatsapp_chats",
			Description: "Alias of inbox_import_preview(source=\"whatsapp\", kind=\"direct\"): the workspace's 1:1 WhatsApp chats, with their last message.",
			Scope:       "inbox:read",
			InputSchema: schema(nil, map[string]any{
				"connection_id": prop("string", descImportConnID),
				"q":             prop("string", descImportQ),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				m := decodeArgs(raw)
				m["kind"] = "direct"
				b, _ := json.Marshal(m)
				return previewFn("whatsapp")(ctx, a, b)
			},
		},
		{
			Name:        "whatsapp_groups",
			Description: "Alias of inbox_import_preview(source=\"whatsapp\", kind=\"group\"): the workspace's WhatsApp groups, with their participants and last message.",
			Scope:       "inbox:read",
			InputSchema: schema(nil, map[string]any{
				"connection_id": prop("string", descImportConnID),
				"q":             prop("string", descImportQ),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				m := decodeArgs(raw)
				m["kind"] = "group"
				b, _ := json.Marshal(m)
				return previewFn("whatsapp")(ctx, a, b)
			},
		},
		{
			Name:        "whatsapp_import",
			Description: "Alias of inbox_import(source=\"whatsapp\"): import selected WhatsApp chats/groups (ids from whatsapp_chats/whatsapp_groups).",
			Scope:       "inbox:write",
			InputSchema: schema([]string{"items"}, map[string]any{
				"connection_id":       prop("string", descImportConnID),
				"items":               map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": descImportItems},
				"history_depth":       map[string]any{"type": "integer", "description": descImportHistoryDepth},
				"import_participants": map[string]any{"type": "boolean", "description": descImportParticipants},
			}),
			Run: importFn("whatsapp"),
		},
		{
			Name:        "whatsapp_chat_summary",
			Description: "Alias of inbox_chat_summary for a WhatsApp conversation.",
			Scope:       "inbox:read",
			InputSchema: schema([]string{"conversation_id"}, map[string]any{"conversation_id": prop("string", descImportConversationID)}),
			Run:         summaryFn,
		},
	}
}
