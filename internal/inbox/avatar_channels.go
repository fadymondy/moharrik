package inbox

// MH-588: Telegram/Slack/Discord's half of "contact avatars from every
// channel" — WhatsApp already has this (whatsapp.go's refreshWhatsAppPhoto/
// queuePhotoRefresh); this is the same background, deduped, best-effort
// fetch, generalised to three more platforms through
// internal/channels.RefreshChannelPhoto instead of duplicating the queue.

import (
	"context"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/channels"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// queueChannelPhotoRefresh fires a Telegram/Slack/Discord profile-photo fetch
// in the background, sharing photoRefreshQueued (dedupe) and photoRefreshSem
// (concurrency cap) with WhatsApp's own queuePhotoRefresh — one inbound
// message on a busy workspace across several channels still can't open more
// than photoRefreshConcurrency goroutines at once. Never blocks the caller:
// FileChannelMessage must return as soon as the message itself is filed.
func queueChannelPhotoRefresh(a *app.App, workspaceID, channel, connID, convID, contactID, externalUserID string) {
	if connID == "" || externalUserID == "" {
		return
	}
	kind, id := "conversation", convID
	if contactID != "" {
		kind, id = "contact", contactID
	}
	if id == "" {
		return
	}
	key := workspaceID + "|" + kind + "|" + id
	now := time.Now()
	if v, ok := photoRefreshQueued.Load(key); ok {
		if now.Sub(v.(time.Time)) < photoRefreshDedupeWindow {
			return
		}
	}
	photoRefreshQueued.Store(key, now)
	go func() {
		photoRefreshSem <- struct{}{}
		defer func() { <-photoRefreshSem }()
		// A fresh background context, re-scoped for RLS: the request's own ctx
		// is cancelled the moment the webhook handler returns, before a round
		// trip to Telegram/Slack/Discord could ever finish — the exact same
		// reason queuePhotoRefresh does this for WhatsApp.
		bctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		bctx = tenant.WithWorkspace(bctx, workspaceID)
		channels.RefreshChannelPhoto(bctx, a, workspaceID, channel, connID, kind, id, externalUserID)
	}()
}
