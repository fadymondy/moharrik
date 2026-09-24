package mcp

import (
	"context"
	"encoding/json"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/channels"
)

/*
ChannelTools (MH-563, MH-564): manage the inbox's Telegram, Slack, Discord and
browser-workflow connections. They call internal/channels — the same package
the settings pages' REST routes call — so a connection made or tested by an
agent is exactly the connection a member would see on the setup page.

Bot tokens and signing secrets are write-only: a tool that creates or updates
a connection takes one, but nothing here ever reads one back.
*/

func telegramSummary(c channels.TelegramChannel) map[string]any {
	return map[string]any{
		"id": c.ID, "name": c.Name, "bot_username": c.BotUsername, "has_token": c.HasToken,
		"allowed_chat_ids": c.AllowedChatIDs, "enabled": c.Enabled, "webhook_url": c.WebhookURL,
		"last_delivery_at": c.LastDeliveryAt, "last_error": c.LastError, "last_test_ok": c.LastTestOK,
	}
}

func slackSummary(c channels.SlackChannel) map[string]any {
	return map[string]any{
		"id": c.ID, "name": c.Name, "team_name": c.TeamName, "bot_user_id": c.BotUserID,
		"has_bot_token": c.HasBotToken, "has_signing_secret": c.HasSigningSecret,
		"allowed_channel_ids": c.AllowedChannelIDs, "enabled": c.Enabled, "webhook_url": c.WebhookURL,
		"last_delivery_at": c.LastDeliveryAt, "last_error": c.LastError, "last_test_ok": c.LastTestOK,
	}
}

func discordSummary(c channels.DiscordChannel) map[string]any {
	return map[string]any{
		"id": c.ID, "name": c.Name, "bot_username": c.BotUsername, "has_token": c.HasToken,
		"allowed_guild_ids": c.AllowedGuildIDs, "allowed_channel_ids": c.AllowedChannelIDs,
		"enabled": c.Enabled, "gateway_state": c.GatewayState, "interactions_url": c.InteractionsURL,
		"last_delivery_at": c.LastDeliveryAt, "last_error": c.LastError, "last_test_ok": c.LastTestOK,
	}
}

func workflowChannelSummary(c channels.WorkflowChannel) map[string]any {
	return map[string]any{
		"id": c.ID, "name": c.Name, "network": c.Network, "workflow_id": c.WorkflowID,
		"allow_replies": c.AllowReplies, "enabled": c.Enabled, "last_run_at": c.LastRunAt,
		"last_error": c.LastError, "last_pushed_count": c.LastPushedCount,
	}
}

func ChannelTools() []Tool {
	return []Tool{
		{
			Name:        "list_telegram_channels",
			Description: "This workspace's Telegram bot connections, with status and webhook health. Bot tokens are never returned.",
			Scope:       "inbox:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				list, err := channels.ListTelegram(ctx, a.SQLDB, s.WorkspaceID)
				if err != nil {
					return "", err
				}
				out := make([]map[string]any, 0, len(list))
				for _, c := range list {
					out = append(out, telegramSummary(c))
				}
				return jsonResult(map[string]any{"count": len(out), "channels": out})
			},
		},
		{
			Name: "connect_telegram_channel",
			Description: "Add a Telegram bot connection (from @BotFather). Given a token, its webhook is registered with Telegram at once. " +
				"allowed_chat_ids is comma-separated; empty allows every chat that reaches the bot.",
			Scope: "inbox:manage",
			InputSchema: schema([]string{"name", "bot_token"}, map[string]any{
				"name": prop("string", "A label for this connection"), "bot_token": prop("string", "From @BotFather"),
				"allowed_chat_ids": prop("string", "Comma-separated chat/group ids; empty allows every chat"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct{ Name, BotToken, AllowedChatIDs string }
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				c, err := channels.CreateTelegram(ctx, a, s.WorkspaceID, s.UserID, channels.TelegramInput{
					Name: in.Name, BotToken: in.BotToken, AllowedChatIDs: in.AllowedChatIDs})
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"channel": telegramSummary(*c)})
			},
		},
		{
			Name:        "test_telegram_channel",
			Description: "Verify a Telegram connection's bot token (getMe) and re-register its webhook.",
			Scope:       "inbox:manage",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "The connection's id")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct{ ID string }
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				c, err := channels.TestTelegram(ctx, a, s.WorkspaceID, id)
				if err != nil {
					if c == nil {
						return "", err
					}
					return jsonResult(map[string]any{"ok": false, "error": err.Error(), "channel": telegramSummary(*c)})
				}
				return jsonResult(map[string]any{"ok": true, "channel": telegramSummary(*c)})
			},
		},
		{
			Name:        "list_slack_channels",
			Description: "This workspace's Slack app connections, with status and webhook health. Bot tokens and signing secrets are never returned.",
			Scope:       "inbox:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				list, err := channels.ListSlack(ctx, a.SQLDB, s.WorkspaceID)
				if err != nil {
					return "", err
				}
				out := make([]map[string]any, 0, len(list))
				for _, c := range list {
					out = append(out, slackSummary(c))
				}
				return jsonResult(map[string]any{"count": len(out), "channels": out})
			},
		},
		{
			Name: "connect_slack_channel",
			Description: "Add a Slack app connection (a bot token and signing secret from the Slack app's own pages). Returns webhook_url — " +
				"paste it into the Slack app's Event Subscriptions Request URL. allowed_channel_ids is comma-separated; empty allows every channel/DM the bot is in.",
			Scope: "inbox:manage",
			InputSchema: schema([]string{"name", "bot_token", "signing_secret"}, map[string]any{
				"name": prop("string", "A label for this connection"), "bot_token": prop("string", "xoxb-… from OAuth & Permissions"),
				"signing_secret":      prop("string", "From Basic Information"),
				"allowed_channel_ids": prop("string", "Comma-separated Slack channel ids; empty allows every channel/DM"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct{ Name, BotToken, SigningSecret, AllowedChannelIDs string }
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				c, err := channels.CreateSlack(ctx, a, s.WorkspaceID, s.UserID, channels.SlackInput{
					Name: in.Name, BotToken: in.BotToken, SigningSecret: in.SigningSecret, AllowedChannelIDs: in.AllowedChannelIDs})
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"channel": slackSummary(*c)})
			},
		},
		{
			Name:        "test_slack_channel",
			Description: "Verify a Slack connection's bot token (auth.test) and record its team and bot identity.",
			Scope:       "inbox:manage",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "The connection's id")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct{ ID string }
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				c, err := channels.TestSlack(ctx, a, s.WorkspaceID, id)
				if err != nil {
					if c == nil {
						return "", err
					}
					return jsonResult(map[string]any{"ok": false, "error": err.Error(), "channel": slackSummary(*c)})
				}
				return jsonResult(map[string]any{"ok": true, "channel": slackSummary(*c)})
			},
		},
		{
			Name:        "list_discord_channels",
			Description: "This workspace's Discord bot connections, with status and gateway state. Bot tokens are never returned.",
			Scope:       "inbox:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				list, err := channels.ListDiscord(ctx, a.SQLDB, s.WorkspaceID)
				if err != nil {
					return "", err
				}
				out := make([]map[string]any, 0, len(list))
				for _, c := range list {
					out = append(out, discordSummary(c))
				}
				return jsonResult(map[string]any{"count": len(out), "channels": out})
			},
		},
		{
			Name: "connect_discord_channel",
			Description: "Add a Discord bot connection (a bot token from the developer portal's Bot page). Given a token, its gateway session " +
				"starts at once — the gateway is the only way to receive ordinary messages and DMs. allowed_guild_ids/allowed_channel_ids are comma-separated; empty allows every guild/channel.",
			Scope: "inbox:manage",
			InputSchema: schema([]string{"name", "bot_token"}, map[string]any{
				"name": prop("string", "A label for this connection"), "bot_token": prop("string", "From the developer portal's Bot page"),
				"application_id":      prop("string", ""),
				"public_key":          prop("string", "For verifying interactions"),
				"allowed_guild_ids":   prop("string", "Comma-separated guild ids; empty allows every guild"),
				"allowed_channel_ids": prop("string", "Comma-separated channel ids; empty allows every channel in an allowed guild, plus DMs"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct{ Name, BotToken, ApplicationID, PublicKey, AllowedGuildIDs, AllowedChannelIDs string }
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				c, err := channels.CreateDiscord(ctx, a, s.WorkspaceID, s.UserID, channels.DiscordInput{
					Name: in.Name, BotToken: in.BotToken, ApplicationID: in.ApplicationID, PublicKey: in.PublicKey,
					AllowedGuildIDs: in.AllowedGuildIDs, AllowedChannelIDs: in.AllowedChannelIDs})
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"channel": discordSummary(*c)})
			},
		},
		{
			Name:        "test_discord_channel",
			Description: "Verify a Discord connection's bot token (GET /users/@me) and (re)start its gateway session.",
			Scope:       "inbox:manage",
			InputSchema: schema([]string{"id"}, map[string]any{"id": prop("string", "The connection's id")}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct{ ID string }
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				c, err := channels.TestDiscord(ctx, a, s.WorkspaceID, id)
				if err != nil {
					if c == nil {
						return "", err
					}
					return jsonResult(map[string]any{"ok": false, "error": err.Error(), "channel": discordSummary(*c)})
				}
				return jsonResult(map[string]any{"ok": true, "channel": discordSummary(*c)})
			},
		},
		{
			Name: "list_workflow_channels",
			Description: "This workspace's browser-workflow inbox channels (MH-564): LinkedIn, Facebook, Instagram and X, each fed by a " +
				"workflow's \"Push to inbox\" step on a schedule.",
			Scope:       "inbox:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				list, err := channels.ListWorkflowChannels(ctx, a.SQLDB, s.WorkspaceID)
				if err != nil {
					return "", err
				}
				out := make([]map[string]any, 0, len(list))
				for _, c := range list {
					out = append(out, workflowChannelSummary(c))
				}
				return jsonResult(map[string]any{"count": len(out), "channels": out})
			},
		},
		{
			Name: "connect_workflow_channel",
			Description: "Add a browser-workflow inbox channel for a network with no usable API: linkedin, facebook, instagram or x. " +
				"Point it at a workflow (list_workflows) that reads that network's inbox with a \"Push to inbox\" step.",
			Scope: "inbox:manage",
			InputSchema: schema([]string{"name", "network"}, map[string]any{
				"name": prop("string", "A label for this connection"), "network": map[string]any{"type": "string", "enum": []string{"linkedin", "facebook", "instagram", "x"}},
				"workflow_id":   prop("string", "The workflow that reads this network's inbox; can be set later"),
				"allow_replies": map[string]any{"type": "boolean", "description": "Reply through the network's reply preset, with confirmation (off by default)"},
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				s, err := need(ctx)
				if err != nil {
					return "", err
				}
				var in struct {
					Name, Network, WorkflowID string
					AllowReplies              bool
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				var wfID *string
				if in.WorkflowID != "" {
					wfID = &in.WorkflowID
				}
				c, err := channels.CreateWorkflowChannel(ctx, a, s.WorkspaceID, s.UserID, channels.WorkflowChannelInput{
					Name: in.Name, Network: in.Network, WorkflowID: wfID, AllowReplies: &in.AllowReplies})
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"channel": workflowChannelSummary(*c)})
			},
		},
	}
}
