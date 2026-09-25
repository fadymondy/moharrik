package inbox

import (
	"net/http"
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/whatsapp"

	"github.com/danielgtaylor/huma/v2"
)

// FileWhatsApp puts one WhatsApp message on the right conversation.
//
// One conversation per number per workspace, so a customer who writes again
// next week continues the thread they already had — there is no Message-ID to
// thread on, and a phone number is a stable identity in a way an email subject
// is not.
//
// It is wired into internal/whatsapp at boot rather than imported there, so
// neither package depends on the other.
func FileWhatsApp(ctx context.Context, a *app.App, workspaceID, phone, name, text, externalID string) error {
	phone = strings.TrimSpace(phone)
	if phone == "" || strings.TrimSpace(text) == "" {
		return nil
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	db := a.SQLDB

	// A redelivery is the same row. Evolution retries on any non-2xx, so this
	// is the ordinary case rather than an edge one.
	if externalID != "" {
		var exists string
		err := db.QueryRowContext(sctx, `
			SELECT id FROM messages WHERE workspace_id = $1 AND external_id = $2`,
			workspaceID, externalID).Scan(&exists)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}

	var convID string
	err := db.QueryRowContext(sctx, `
		SELECT id FROM conversations
		WHERE workspace_id = $1 AND channel = 'whatsapp' AND customer_phone = $2`,
		workspaceID, phone).Scan(&convID)
	if errors.Is(err, sql.ErrNoRows) {
		if err := db.QueryRowContext(sctx, `
			INSERT INTO conversations (workspace_id, channel, customer_name, customer_phone, subject, last_message_at, unread)
			VALUES ($1, 'whatsapp', $2, $3, $4, now(), true) RETURNING id`,
			workspaceID, trimTo(name, 120), phone, trimTo(text, 80),
		).Scan(&convID); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	ins, err := db.ExecContext(sctx, `
		INSERT INTO messages (workspace_id, conversation_id, direction, body, external_id)
		VALUES ($1, $2, 'in', $3, $4)
		ON CONFLICT (workspace_id, external_id) WHERE external_id <> '' DO NOTHING`,
		workspaceID, convID, trimTo(text, maxBody), externalID)
	if err != nil {
		return err
	}
	// A redelivery that raced the check above wrote nothing, and alerts nobody.
	if n, _ := ins.RowsAffected(); n == 0 {
		return nil
	}

	if _, err := db.ExecContext(sctx, `
		UPDATE conversations SET last_message_at = now(), unread = true, updated_at = now(),
		  customer_name = CASE WHEN customer_name = '' THEN $3 ELSE customer_name END, `+reopenOnInbound+`
		WHERE workspace_id = $1 AND id = $2`, workspaceID, convID, trimTo(name, 120)); err != nil {
		return err
	}
	// A number the CRM already knows (the same digits, MH-320's normalisation)
	// is linked to the thread (MH-379). Best effort: the message is filed.
	var linkedContactID string
	if err := autoLink(sctx, db, workspaceID, convID); err != nil {
		a.Log.Warn("could not match a WhatsApp number to a contact", "workspace", workspaceID, "err", err)
	} else {
		_ = db.QueryRowContext(sctx, `SELECT contact_id FROM conversations WHERE workspace_id = $1 AND id = $2`,
			workspaceID, convID).Scan(&linkedContactID)
	}

	// The team is told at once, the same as a chat message — a customer on
	// WhatsApp expects an answer in minutes, not on the next refresh.
	announceInbound(ctx, a, workspaceID, convID, text, time.Time{})

	// MH-602: cache the customer's WhatsApp photo, in the background — never
	// block this webhook on Evolution, which is exactly what filing the
	// message above must not wait on either.
	refreshWhatsAppPhoto(a, workspaceID, convID, linkedContactID, phone)
	return nil
}

// refreshWhatsAppPhoto fires the provider-photo fetch for one conversation
// (or, once linked, its contact instead) through the same queue every
// list/get trigger below uses, so an inbound message and a list refresh for
// the same row can never both be in flight.
func refreshWhatsAppPhoto(a *app.App, workspaceID, conversationID, contactID, phone string) {
	if contactID != "" {
		queuePhotoRefresh(a, workspaceID, "contact", contactID, phone)
	} else {
		queuePhotoRefresh(a, workspaceID, "conversation", conversationID, phone)
	}
}

// ---- MH-602 defect 1: fetch on list/first-show, not only on inbound -------
//
// Production shipped with all 248 WhatsApp conversations at photo_fetched_at
// NULL, because the only trigger was refreshWhatsAppPhoto above, on a NEW
// inbound message — a conversation that already existed before this feature
// landed, or one nobody has messaged again since, never gets a photo. The
// spec calls for a fetch "when a conversation/contact is created or first
// shown" too, so ListConversations and GetConversation (service.go) queue
// one here for every WhatsApp row scanConversation (routes.go) flagged as
// stale, through the exact same path as the inbound trigger.

// photoRefreshDedupeWindow bounds how often the same row is queued for a
// background fetch from these triggers. A slow-scrolling inbox re-lists the
// same page repeatedly, and infinite scroll re-lists earlier pages too — of
// itself photoWindow (internal/whatsapp/avatar.go) doesn't stop that, since
// it is only checked AFTER a fetch lands, not while one is in flight.
const photoRefreshDedupeWindow = 10 * time.Minute

// photoRefreshMaxPerList bounds how many rows one list response queues a
// fetch for, so a big unfetched backlog (production's 248) drains gradually
// across pages instead of opening hundreds of goroutines from one request.
const photoRefreshMaxPerList = 10

// photoRefreshConcurrency caps how many photo fetches run against Evolution
// at once, across every trigger (inbound, list, get) combined — Evolution is
// one shared service per workspace link, not a CDN built for a stampede.
const photoRefreshConcurrency = 4

// photoRefreshCandidate is what scanConversation found: a WhatsApp row whose
// relevant photo (the linked contact's, else the conversation's own) needs a
// background fetch.
type photoRefreshCandidate struct {
	kind  string // "contact" or "conversation" — queuePhotoRefresh's kind
	id    string
	phone string
}

var (
	// photoRefreshQueued dedupes queuePhotoRefresh across goroutines: key is
	// workspaceID+kind+id, value the time it was last queued.
	photoRefreshQueued sync.Map
	// photoRefreshSem is the global concurrency cap every queued fetch
	// acquires before calling Evolution.
	photoRefreshSem = make(chan struct{}, photoRefreshConcurrency)
)

// photoIsStale answers scanConversation's question from the fetched_at
// column it already read: never fetched, or past the refresh window.
func photoIsStale(fetchedAt sql.NullTime) bool {
	return !fetchedAt.Valid || time.Since(fetchedAt.Time) >= whatsapp.PhotoRefreshWindow
}

// queuePhotoRefresh fires a provider-photo fetch in the background, deduped
// (photoRefreshDedupeWindow) and bounded (photoRefreshSem) so repeated list
// loads, infinite scroll, and the inbound-message trigger can never pile up
// goroutines or hammer Evolution for the same row. Never blocks the caller:
// if WhatsApp isn't configured or the workspace has no link, this is a no-op.
func queuePhotoRefresh(a *app.App, workspaceID, kind, id, phone string) {
	cfg := whatsapp.LoadConfig()
	if !cfg.Configured() || id == "" {
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
		// A fresh background context (still tenant-scoped, for RLS) is used
		// deliberately: the request's own ctx is cancelled the moment the
		// handler returns, before a round trip to Evolution could ever finish.
		bctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		bctx = tenant.WithWorkspace(bctx, workspaceID)
		link, err := whatsapp.Load(bctx, a, workspaceID)
		if err != nil || link == nil {
			return
		}
		if kind == "contact" {
			whatsapp.RefreshContactPhoto(bctx, a.SQLDB, cfg, link, workspaceID, id, phone)
		} else {
			whatsapp.RefreshConversationPhoto(bctx, a.SQLDB, cfg, link, workspaceID, id, phone)
		}
	}()
}

// triggerPhotoRefreshes queues a fetch for at most photoRefreshMaxPerList of
// list's flagged WhatsApp rows — ListConversations' half of defect 1.
func triggerPhotoRefreshes(a *app.App, workspaceID string, list []ConversationOut) {
	n := 0
	for i := range list {
		cand := list[i].refreshPhoto
		if cand == nil {
			continue
		}
		if n >= photoRefreshMaxPerList {
			return
		}
		queuePhotoRefresh(a, workspaceID, cand.kind, cand.id, cand.phone)
		n++
	}
}

// whatsappError turns a send failure into a status the thread can explain.
func whatsappError(err error) error {
	switch {
	case errors.Is(err, whatsapp.ErrNotConfigured):
		return huma.Error503ServiceUnavailable(err.Error())
	case errors.Is(err, whatsapp.ErrNoLink):
		return huma.Error409Conflict("this workspace has no WhatsApp number linked, so it cannot answer on WhatsApp yet")
	}
	var we *whatsapp.Error
	if errors.As(err, &we) {
		return huma.NewError(http.StatusFailedDependency, we.Error())
	}
	return huma.Error500InternalServerError("the reply could not be sent")
}
