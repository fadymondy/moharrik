package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/inbox"
)

/*
OrganiseTools (MH-657): what the inbox row's context menu does — pin, archive,
mute, favourite, colour, icon, labels, block, clear and delete — and the
workspace's chat labels. They call internal/inbox's Organise, Clear, Delete and
label functions, the same ones the REST routes call, so the same role rules
apply: pin, archive, mute and favourite are the calling member's own; colour,
icon and labels are the team's (inbox:write); block, clear, delete and deleting
a label need inbox:manage.
*/

var tagColorEnum = []string{"gray", "red", "orange", "amber", "green", "teal", "blue", "violet", "pink"}

func labelSummary(l inbox.Label) map[string]any {
	return map[string]any{"id": l.ID, "name": l.Name, "color": l.Color, "count": l.Count, "created_at": l.CreatedAt}
}

func OrganiseTools() []Tool {
	idOnly := func(tool string, do func(context.Context, *app.App, string) error, done string) func(context.Context, *app.App, json.RawMessage) (string, error) {
		return func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
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
			if err := do(ctx, a, id); err != nil {
				return "", err
			}
			return jsonResult(map[string]any{"id": id, done: true})
		}
	}
	return []Tool{
		{
			Name: "organise_conversation",
			Description: "Organise a conversation the way the inbox row's context menu does. Your own view: pinned (top of your list, at most 5; " +
				"pinning unarchives), archived (hidden from your list until the customer writes again, unless muted; archiving unpins), " +
				"mute (8h, 1w or always — no alerts for you; empty string unmutes) and favourite. The team's view (inbox:write): color, " +
				"icon and label_ids — the full set of labels, replacing the current one (list_conversation_labels shows ids). " +
				"Pass only what changes. Blocking is block_conversation.",
			Scope: "inbox:write",
			InputSchema: schema([]string{"id"}, map[string]any{
				"id":        prop("string", "Conversation id"),
				"pinned":    map[string]any{"type": "boolean", "description": "Pin it to the top of your inbox, or unpin it"},
				"archived":  map[string]any{"type": "boolean", "description": "Archive it in your inbox, or bring it back"},
				"mute":      map[string]any{"type": "string", "enum": []string{"", "8h", "1w", "always"}, "description": "Mute your alerts for 8h, 1w or always; empty string unmutes"},
				"favourite": map[string]any{"type": "boolean", "description": "Add it to your favourites, or remove it"},
				"color":     map[string]any{"type": "string", "enum": append([]string{""}, tagColorEnum...), "description": "The chat's colour for the whole team; empty string clears it"},
				"icon":      prop("string", "The chat's icon for the whole team, a lowercase icon name such as star or briefcase; empty string clears it"),
				"label_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The full set of label ids the chat carries; an empty list removes them all"},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					ID string `json:"id"`
					inbox.OrganisePatch
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				in.OrganisePatch.Blocked = nil
				p := in.OrganisePatch
				if p.Pinned == nil && p.Archived == nil && p.Mute == nil && p.Favourite == nil && p.Color == nil && p.Icon == nil && p.LabelIDs == nil {
					return "", fmt.Errorf("pass at least one of pinned, archived, mute, favourite, color, icon or label_ids")
				}
				c, err := inbox.Organise(ctx, a, id, p)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"conversation": conversationSummary(*c)})
			},
		},
		{
			Name: "block_conversation",
			Description: "Block (or unblock) a conversation's customer. While blocked, what they send is dropped and the chat cannot be " +
				"answered; on WhatsApp the number is also blocked on the workspace's linked phone. Pass blocked:false to unblock.",
			Scope: "inbox:manage",
			InputSchema: schema([]string{"id", "blocked"}, map[string]any{
				"id":      prop("string", "Conversation id"),
				"blocked": map[string]any{"type": "boolean", "description": "true blocks the customer, false unblocks them"},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					ID      string `json:"id"`
					Blocked *bool  `json:"blocked"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				if in.Blocked == nil {
					return "", fmt.Errorf("blocked is required")
				}
				c, err := inbox.Organise(ctx, a, id, inbox.OrganisePatch{Blocked: in.Blocked})
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"conversation": conversationSummary(*c)})
			},
		},
		{
			Name: "clear_conversation",
			Description: "Delete every message and attachment in a conversation but keep the conversation, its customer, contact link and " +
				"labels. It cannot be undone.",
			Scope:       "inbox:manage",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "Conversation id")}),
			Run:         idOnly("clear_conversation", inbox.ClearConversation, "cleared"),
		},
		{
			Name: "delete_conversation",
			Description: "Delete a conversation with all its messages and attachments. The customer's CRM contact is kept. It cannot be " +
				"undone; if they write again a new conversation starts.",
			Scope:       "inbox:manage",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "Conversation id")}),
			Run:         idOnly("delete_conversation", inbox.DeleteConversation, "deleted"),
		},
		{
			Name:        "list_conversation_labels",
			Description: "The workspace's chat labels, each with its id, name, colour and how many chats carry it. Filter list_conversations by one with label_id.",
			Scope:       "inbox:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				ls, err := inbox.ListLabels(ctx, a)
				if err != nil {
					return "", err
				}
				out := make([]map[string]any, 0, len(ls))
				for _, l := range ls {
					out = append(out, labelSummary(l))
				}
				return jsonResult(map[string]any{"labels": out, "count": len(out)})
			},
		},
		{
			Name:        "create_conversation_label",
			Description: "Create a chat label for the workspace. Names are unique, case-insensitive. color defaults to gray.",
			Scope:       "inbox:write",
			InputSchema: schema([]string{"name"}, map[string]any{
				"name":  prop("string", "The label's name, at most 40 characters"),
				"color": map[string]any{"type": "string", "enum": tagColorEnum, "description": "The label's colour, gray by default"},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in inbox.LabelInput
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				if in.Name == nil {
					return "", fmt.Errorf("name is required")
				}
				l, err := inbox.CreateLabel(ctx, a, in)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"label": labelSummary(*l)})
			},
		},
		{
			Name:        "update_conversation_label",
			Description: "Rename a chat label or change its colour. Every chat carrying it shows the change.",
			Scope:       "inbox:write",
			InputSchema: schema([]string{"id"}, map[string]any{
				"id":    prop("string", "The label's id (list_conversation_labels)"),
				"name":  prop("string", "The label's name, at most 40 characters"),
				"color": map[string]any{"type": "string", "enum": tagColorEnum, "description": "The label's new colour"},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					ID string `json:"id"`
					inbox.LabelInput
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				l, err := inbox.UpdateLabel(ctx, a, id, in.LabelInput)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"label": labelSummary(*l)})
			},
		},
		{
			Name:        "delete_conversation_label",
			Description: "Delete a chat label. It comes off every chat that carried it; the chats themselves are kept.",
			Scope:       "inbox:manage",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "The label's id (list_conversation_labels)")}),
			Run:         idOnly("delete_conversation_label", inbox.DeleteLabel, "deleted"),
		},
	}
}
