// Package channels is the inbox's Telegram, Slack and Discord connections
// (MH-563), and its browser-workflow channels for LinkedIn, Facebook,
// Instagram and X (MH-564).
//
// Shape: several connections of one kind per workspace ("as many of each as
// a workspace wants"), each with its own sealed credentials, allow-list and
// on/off switch — chat_widgets' shape, generalised to three more platforms.
//
// Like internal/whatsapp, this package never imports internal/inbox: an
// inbound delivery is handed to a Filer set at boot (server.go), and a
// reply is sent out by calling this package FROM internal/inbox/service.go,
// the same one-way edge whatsapp.SendAs already has.
package channels

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

var tags = []string{"Channels"}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

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

// Filer delivers one inbound message to the shared inbox. channel is
// "telegram", "slack" or "discord"; sourceChannelID is the specific
// connection's own id (a workspace can have several of one kind);
// externalThreadID is the platform's own identity for the conversation (a
// Telegram chat id, a Slack channel/DM id, a Discord channel id).
type Filer func(ctx context.Context, workspaceID, channel, sourceChannelID, externalThreadID, handle, name, text, externalID string) error

var file Filer

// SetFiler wires the inbox's own "file this message" in at boot, so this
// package and internal/inbox stay unaware of each other.
func SetFiler(f Filer) { file = f }

func deliver(ctx context.Context, workspaceID, channel, sourceChannelID, threadID, handle, name, text, externalID string) error {
	if file == nil {
		return nil
	}
	return file(ctx, workspaceID, channel, sourceChannelID, threadID, handle, name, text, externalID)
}

// newWebhookID mints the unguessable id used in a public delivery URL — a
// second id, apart from the row's own, the same indirection whatsapp_links'
// hook_id gives its link.
func newWebhookID() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic("channels: no entropy for a webhook id: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func trimTo(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func notFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// publicBase is the origin a public webhook URL is built from — the same
// pair of env vars internal/inbox's own publicBase reads, kept as its own
// copy so this package needs no import from there.
func publicBase() string {
	for _, k := range []string{"PUBLIC_URL", "APP_URL"} {
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv(k)), "/"); v != "" {
			return v
		}
	}
	return "http://localhost:3120"
}

// SetTelegramAPIBase, SetSlackAPIBase and SetDiscordAPIBase point this
// package at a fake platform server for tests, the same shape
// notifications.SetEndpoints already uses. t.Setenv would work too (each
// *_API_BASE is read lazily, not at init — see telegramAPIBase's comment),
// but a direct override needs no process-wide env var and cannot race other
// tests in the same package.
func SetTelegramAPIBase(base string) func() {
	prev := telegramAPIBaseOverride
	telegramAPIBaseOverride = strings.TrimRight(base, "/")
	return func() { telegramAPIBaseOverride = prev }
}

func SetSlackAPIBase(base string) func() {
	prev := slackAPIBaseOverride
	slackAPIBaseOverride = strings.TrimRight(base, "/")
	return func() { slackAPIBaseOverride = prev }
}

func SetDiscordAPIBase(base string) func() {
	prev := discordAPIBaseOverride
	discordAPIBaseOverride = strings.TrimRight(base, "/")
	return func() { discordAPIBaseOverride = prev }
}

// SetDiscordGatewayURL points the gateway supervisor at a fake websocket
// server for tests, instead of wss://gateway.discord.gg.
func SetDiscordGatewayURL(url string) func() {
	prev := gatewayURLOverride
	gatewayURLOverride = url
	return func() { gatewayURLOverride = prev }
}
