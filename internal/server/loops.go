package server

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/fadymondy/moharrik/internal/analytics"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/audit"
	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/channels"
	"github.com/fadymondy/moharrik/internal/conversions"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/notifications"
	"github.com/fadymondy/moharrik/internal/research"
	"github.com/fadymondy/moharrik/internal/searchconsole"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/trends"
	"github.com/fadymondy/moharrik/internal/whatsapp"
	"github.com/fadymondy/moharrik/internal/youtube"
)

// wireChannelCallbacks wires the inbox's "file this message"/"mark this
// status" callbacks into the WhatsApp and channels (Telegram/Slack/Discord)
// packages. Neither package imports the other, or inbox, so this is where the
// wiring happens — shared by Boot (whose webhook ROUTES call these) and
// BootWorker (whose gateway LOOPS, e.g. the Discord gateway, call the exact
// same package-level channels.SetFiler).
func wireChannelCallbacks(a *app.App) {
	// Delivery receipts (MH-656): the ticks on a sent WhatsApp message.
	whatsapp.SetStatusSink(func(ctx context.Context, workspaceID, externalID, status string) error {
		return inbox.MarkStatus(ctx, a, workspaceID, externalID, status)
	})
	// What the linked number sent itself — a reply typed on the phone (MH-665).
	whatsapp.SetOwnFiler(func(ctx context.Context, workspaceID, phone, text, externalID string, media *whatsapp.Media, download whatsapp.MediaDownloader) error {
		return inbox.FileWhatsAppOwn(ctx, a, workspaceID, phone, text, externalID, media, download)
	})
	// Typing / recording indicators (MH-660): PRESENCE_UPDATE from a contact.
	whatsapp.SetTypingSink(func(ctx context.Context, workspaceID, phone, state string) error {
		return inbox.FileWhatsAppTyping(ctx, a, workspaceID, phone, state)
	})
	// Emoji reactions (MH-664): the customer's, and the number's own from the
	// phone — filed against the message reacted to, never as a message.
	whatsapp.SetReactionSink(func(ctx context.Context, workspaceID, phone, targetExternalID, emoji string, fromMe bool) error {
		return inbox.FileWhatsAppReaction(ctx, a, workspaceID, phone, targetExternalID, emoji, fromMe)
	})
	whatsapp.SetFiler(func(ctx context.Context, workspaceID, phone, name, text, externalID string, media *whatsapp.Media, download whatsapp.MediaDownloader) error {
		// "trends" / "ترندات" from a member's verified number is a question to
		// the workspace, answered with its top trend stories (MH-389) — not a
		// customer writing in. From anyone else it is filed like any message.
		if handled, err := trends.HandleWhatsAppCommand(ctx, a, workspaceID, phone, text); handled {
			if err != nil {
				a.Log.Warn("could not answer a WhatsApp trends command", "workspace", workspaceID, "err", err)
			}
			return nil
		} else if err != nil {
			a.Log.Warn("could not check a WhatsApp trends command", "workspace", workspaceID, "err", err)
		}
		// "STOP" is the ONLY way out of a WhatsApp broadcast — there is no
		// unsubscribe header the way email has one — so it is honoured before
		// anything else happens to the message (MH-320).
		//
		// The message is still filed afterwards: the owner should see the
		// customer asking to stop in the thread, not just find a flag changed.
		if stopped, err := marketing.HandleStopWord(ctx, a, workspaceID, phone, text); err != nil {
			a.Log.Warn("could not record a WhatsApp opt-out", "workspace", workspaceID, "err", err)
		} else if stopped {
			var locale string
			_ = a.SQLDB.QueryRowContext(tenant.WithWorkspace(ctx, workspaceID), `
				SELECT coalesce(locale, 'en') FROM contacts
				WHERE workspace_id = $1 AND regexp_replace(coalesce(phone, ''), '[^0-9]', '', 'g') = $2
				LIMIT 1`, workspaceID, marketing.OnlyDigits(phone)).Scan(&locale)
			// Confirmed once, in their language. Silence after a STOP reads as
			// having been ignored.
			if _, err := whatsapp.Send(ctx, a, workspaceID, phone, marketing.StopConfirmation(locale)); err != nil {
				a.Log.Warn("could not confirm a WhatsApp opt-out", "workspace", workspaceID, "err", err)
			}
		}
		return inbox.FileWhatsApp(ctx, a, workspaceID, phone, name, text, externalID, media, download)
	})
	channels.SetFiler(func(ctx context.Context, workspaceID, channel, sourceChannelID, externalThreadID, handle, name, text, externalID string, isGroup bool) error {
		return inbox.FileChannelMessage(ctx, a, workspaceID, channel, sourceChannelID, externalThreadID, handle, name, text, externalID, isGroup)
	})
}

// startMovableLoops starts every background loop that has NO dependency on a
// live browser WebSocket connection (see the comment in Boot next to
// agentHub.StartScheduler and rules.Start for the two that do, and therefore
// are not here). These are exactly the loops Hosbah's worker process runs
// (BootWorker), and exactly the loops the web process must NOT also run once
// a worker exists (Boot, gated by externalWorkers()).
//
// Every one of these already turns itself off for SCHEDULER=off (or its own
// *_COLLECTOR / *_SENDER / *_WAKER switch), which is what the tests and the
// OpenAPI export need — that behaviour is unchanged; startMovableLoops only
// changes WHICH PROCESS calls them, never whether they run at all.
func startMovableLoops(a *app.App, agentHub *automation.Hub, researchEngine *research.Engine, log *slog.Logger) {
	// The trends collector: one ticker looking for sources that are due. When
	// each source is due is its own interval (6, 12 or 24 hours; MH-387).
	// TRENDS_COLLECTOR=off turns it off, which is what the tests and the
	// OpenAPI export want — neither should reach out to the internet.
	if os.Getenv("TRENDS_COLLECTOR") != "off" {
		trends.StartCollector(a, 5*time.Minute, 25)
	}

	// Reconnects every enabled Discord connection's gateway session on boot.
	// DISCORD_GATEWAY=off turns it off for the tests and the OpenAPI export —
	// same switch shape as TRENDS_COLLECTOR. Each connection's own session
	// takes a cluster-wide advisory lock for as long as it runs (see
	// internal/channels/discord_gateway.go) so a second worker replica never
	// opens a second gateway socket for the same bot token.
	channels.StartAllGateways(a)

	// Members' WhatsApp notifications (MH-411): reads new events and sends
	// each member's batch when their rules allow. WHATSAPP_NOTIFY=off turns
	// it off. Takes the cluster-wide lock per tick (internal/whatsapp/notify.go)
	// so two replicas never double-send a member's batch.
	whatsapp.StartNotifier(a, 30*time.Second)

	// The research clock (MH-388): scheduled research, spending each
	// workspace's daily budget. RESEARCH_SCHEDULER=off turns it off, and so
	// does SCHEDULER=off — the tests and the OpenAPI export must not search
	// the web.
	if os.Getenv("RESEARCH_SCHEDULER") != "off" && os.Getenv("SCHEDULER") != "off" {
		researchEngine.StartScheduler(a, 2*time.Minute)
	}

	// The trends digest: sends the members' daily and weekly digests when
	// they are due. DIGEST=off turns it off.
	trends.StartDigest(a)

	// Run retention: finished automation runs deleted past each workspace's
	// window (MH-234). It reads SCHEDULER itself.
	automation.StartRetention(a, log)
	// Audit log retention (MH-392): each workspace's window, 365 days by default.
	audit.StartRetention(a, log)

	// The inbox's mail collector: polls the mailboxes that are due.
	// MAIL_COLLECTOR=off turns it off — the tests and the OpenAPI export must
	// not open a connection to anyone's mail server.
	if os.Getenv("MAIL_COLLECTOR") != "off" {
		inbox.StartMailCollector(a, 2*time.Minute, 20)
	}

	// The Google data collector (MH-440..447): discovery, then the enabled
	// properties, sites and channels that are due. GOOGLE_COLLECTOR=off turns
	// it off — the tests and the OpenAPI export must not call Google.
	analytics.RegisterSync()     // GA4's nightly snapshot (MH-444)
	searchconsole.RegisterSync() // Search Console's daily import (MH-445)
	youtube.RegisterSync()       // YouTube sync + quota ledger (MH-441)
	if os.Getenv("GOOGLE_COLLECTOR") != "off" {
		google.StartCollector(a, 5*time.Minute, 20)
	}

	// The notification center's collected sources (MH-384): Bluesky, YouTube
	// and RSS, each on its own interval. NOTIFY_COLLECTOR=off turns it off —
	// the tests and the OpenAPI export must not call anybody's servers.
	if os.Getenv("NOTIFY_COLLECTOR") != "off" {
		notifications.StartCollector(a, time.Minute, 20)
	}

	// GA4 conversions (MH-449): drains the outbox the lead and deal triggers
	// fill, a few seconds after each moment. GA4_SENDER=off turns it off; the
	// tests drain it themselves, against a fake Google. Already a SKIP
	// LOCKED-style job-queue consumer (internal/conversions/sender.go); safe
	// as-is for more than one replica.
	conversions.StartSender(a, 5*time.Second)

	// Snoozed conversations wake when their time comes (MH-383), so every
	// open tab's list and bell move without anyone reloading. SNOOZE_WAKER=off
	// turns it off; the inbox list wakes due threads on read either way.
	if os.Getenv("SNOOZE_WAKER") != "off" {
		inbox.StartSnoozeWaker(a, 30*time.Second)
	}
}
