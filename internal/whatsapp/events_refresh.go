package whatsapp

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
)

/*
A number linked before an event was added to Events (MESSAGES_UPDATE for the
delivery ticks, MH-656) keeps the narrower subscription it was linked with —
the webhook is only set when a number is linked. The first delivery after a
boot checks the instance's subscription once and widens it if it is missing
anything, so nobody has to relink to get new features. It only ever re-sets
the same URL and token with more events; it sends nothing to anyone.
*/

var refreshed sync.Map // hook id → struct{}

func refreshEvents(a *app.App, l *Link) {
	if _, done := refreshed.LoadOrStore(l.HookID, struct{}{}); done {
		return
	}
	cfg := LoadConfig()
	if !cfg.Configured() || cfg.HookBase == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		c := ClientFor(cfg, l)
		wh, err := c.Webhook(ctx)
		if err != nil || wh.URL == "" || !wh.Enabled || !missingEvents(wh.Events) {
			if err != nil {
				refreshed.Delete(l.HookID) // try again on a later delivery
			}
			return
		}
		if err := c.SetWebhook(ctx, cfg.HookBase+"/api/whatsapp/hook/"+l.HookID, l.Token); err != nil {
			refreshed.Delete(l.HookID)
			slog.Warn("whatsapp: could not widen the webhook's events", "workspace", l.WorkspaceID, "err", masked(err))
			return
		}
		slog.Info("whatsapp: webhook events widened", "workspace", l.WorkspaceID, "events", strings.Join(Events, ","))
	}()
}

// missingEvents is whether the instance's subscription lacks any of Events.
func missingEvents(have []string) bool {
	got := map[string]bool{}
	for _, e := range have {
		got[strings.ToUpper(strings.ReplaceAll(e, ".", "_"))] = true
	}
	for _, e := range Events {
		if !got[e] {
			return true
		}
	}
	return false
}
