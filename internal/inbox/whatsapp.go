package inbox

import (
	"net/http"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/realtime"
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
func FileWhatsApp(ctx context.Context, a *app.App, workspaceID, phone, name, text, externalID string, media *whatsapp.Media, download whatsapp.MediaDownloader) error {
	phone = strings.TrimSpace(phone)
	if phone == "" || (strings.TrimSpace(text) == "" && media == nil) {
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
	// A blocked customer's message is dropped at the door (MH-657).
	if isBlocked(sctx, db, workspaceID, convID) {
		return nil
	}

	var msgID string
	err = db.QueryRowContext(sctx, `
		INSERT INTO messages (workspace_id, conversation_id, direction, body, external_id)
		VALUES ($1, $2, 'in', $3, $4)
		ON CONFLICT (workspace_id, external_id) WHERE external_id <> '' DO NOTHING
		RETURNING id`,
		workspaceID, convID, trimTo(text, maxBody), externalID).Scan(&msgID)
	if errors.Is(err, sql.ErrNoRows) {
		// A redelivery that raced the check above wrote nothing, and alerts nobody.
		return nil
	}
	if err != nil {
		return err
	}

	// MH-658: an image, video, voice note, document, sticker, location or
	// contact card rides alongside — or instead of — a caption. Filed on the
	// SAME message_attachments table and signed-URL path an email attachment
	// already uses (internal/inbox/attachment.go), so the client renders both
	// through one component regardless of channel.
	if media != nil {
		// sctx, not ctx: message_attachments carries the same tenant_isolation
		// RLS policy as every other table here, keyed off app.workspace_id —
		// set on sctx by tenant.WithWorkspace above, never on the bare ctx this
		// function was handed.
		fileWhatsAppMedia(sctx, a, db, workspaceID, msgID, media, download)
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

// FileWhatsAppOwn files a message the linked number itself sent (MH-665) as
// the team's outbound message.
//
// Most of them are typed on the phone: the owner answers from WhatsApp as
// often as from the inbox, and a thread that only showed the customer's side
// read as a customer talking to nobody. The rest are echoes of what this app
// sent, which it tells apart by WhatsApp's message id:
//
//   - already a message here (an inbox reply, an import) — nothing to add
//   - in whatsapp_sends (a campaign, a STOP confirmation, a workflow) — filed
//     on the customer's thread if they have one, but never starts one: a
//     broadcast to five hundred numbers is not five hundred conversations
//   - anything else was typed on the phone — filed, starting the thread if
//     the team wrote first
//
// It is the team's own message, so it never marks the chat unread or alerts
// anyone; like a reply from the inbox it marks the chat read, since answering
// on the phone means the owner saw it.
//
// It is stamped on arrival by the database clock, exactly like an inbound
// message: WhatsApp's whole-second timestamp, or this process's clock, would
// sort a quick answer before the question it answers.
func FileWhatsAppOwn(ctx context.Context, a *app.App, workspaceID, phone, text, externalID string, media *whatsapp.Media, download whatsapp.MediaDownloader) error {
	phone = strings.TrimSpace(phone)
	if phone == "" || (strings.TrimSpace(text) == "" && media == nil) || externalID == "" {
		return nil
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	db := a.SQLDB

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
	var sentHere bool
	if err := db.QueryRowContext(sctx, `
		SELECT EXISTS (SELECT 1 FROM whatsapp_sends WHERE workspace_id = $1 AND external_id = $2)`,
		workspaceID, externalID).Scan(&sentHere); err != nil {
		return err
	}

	var convID string
	err = db.QueryRowContext(sctx, `
		SELECT id FROM conversations
		WHERE workspace_id = $1 AND channel = 'whatsapp' AND customer_phone = $2`,
		workspaceID, phone).Scan(&convID)
	if errors.Is(err, sql.ErrNoRows) {
		if sentHere {
			return nil
		}
		if err := db.QueryRowContext(sctx, `
			INSERT INTO conversations (workspace_id, channel, customer_name, customer_phone, subject, last_message_at, unread)
			VALUES ($1, 'whatsapp', '', $2, $3, now(), false) RETURNING id`,
			workspaceID, phone, trimTo(text, 80),
		).Scan(&convID); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	var msgID string
	err = db.QueryRowContext(sctx, `
		INSERT INTO messages (workspace_id, conversation_id, direction, body, body_html, external_id, status)
		VALUES ($1, $2, 'out', $3, $4, $5, 'sent')
		ON CONFLICT (workspace_id, external_id) WHERE external_id <> '' DO NOTHING
		RETURNING id`,
		workspaceID, convID, trimTo(text, maxBody), RenderMarkdown(text), externalID).Scan(&msgID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// A photo, voice note or file sent from the phone rides along exactly as
	// a customer's does (MH-658).
	if media != nil {
		fileWhatsAppMedia(sctx, a, db, workspaceID, msgID, media, download)
	}
	if _, err := db.ExecContext(sctx, `
		UPDATE conversations SET last_message_at = now(), unread = false, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, convID); err != nil {
		return err
	}
	if !sentHere {
		// A contact the CRM already knows is linked, as for an inbound thread.
		if err := autoLink(sctx, db, workspaceID, convID); err != nil {
			a.Log.Warn("could not match a WhatsApp number to a contact", "workspace", workspaceID, "err", err)
		}
	}
	changed(ctx, a, workspaceID, realtime.Event{Kind: "message", Direction: "out", ConversationID: convID})
	return nil
}

// fileWhatsAppMedia stores one message's attachment (MH-658): a location or
// contact card renders from data it already carried in the webhook (no
// download needed), everything else is downloaded from Evolution with the
// bounded timeout and size cap GetMediaBase64 itself enforces. A failed
// download still files a placeholder row (status 'failed') rather than the
// message losing its attachment silently — the thread is what must never be
// dropped, and it isn't: the message row is already committed by the time
// this runs.
func fileWhatsAppMedia(ctx context.Context, a *app.App, db *sql.DB, workspaceID, messageID string, media *whatsapp.Media, download whatsapp.MediaDownloader) {
	switch media.Kind {
	case "location":
		meta, _ := json.Marshal(map[string]any{
			"lat": media.Lat, "lng": media.Lng, "name": media.LocationName, "address": media.Address,
		})
		insertAttachment(ctx, a, db, workspaceID, messageID, "location", "location", "application/geo+json", "ready", nil, meta)
		return
	case "contact":
		meta, _ := json.Marshal(map[string]any{"name": media.ContactName, "number": vcardNumber(media.ContactVC)})
		insertAttachment(ctx, a, db, workspaceID, messageID, "contact", "contact.vcf", "text/vcard", "ready", []byte(media.ContactVC), meta)
		return
	}
	filename := media.FileName
	mimeType := media.MimeType
	if download == nil {
		insertAttachment(ctx, a, db, workspaceID, messageID, media.Kind, filename, mimeType, "failed", nil, []byte(`{}`))
		return
	}
	mf, err := download(ctx)
	if err == nil && mf.FileName != "" {
		filename = mf.FileName
	}
	if err == nil && mf.MimeType != "" {
		mimeType = mf.MimeType
	}
	if err != nil || len(mf.Data) == 0 {
		if err != nil {
			a.Log.Warn("could not download a WhatsApp attachment", "workspace", workspaceID, "kind", media.Kind, "err", err)
		}
		insertAttachment(ctx, a, db, workspaceID, messageID, media.Kind, filename, mimeType, "failed", nil, []byte(`{}`))
		return
	}
	insertAttachment(ctx, a, db, workspaceID, messageID, media.Kind, filename, mimeType, "ready", mf.Data, []byte(`{}`))
}

// insertAttachment writes one message_attachments row. Best-effort: a failure
// here is logged, never returned — the message it belongs to is already
// filed, and an attachment write failing must not look like the whole
// message never arrived.
func insertAttachment(ctx context.Context, a *app.App, db *sql.DB, workspaceID, messageID, kind, filename, contentType, status string, data []byte, meta []byte) {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if filename == "" {
		filename = "attachment"
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO message_attachments (workspace_id, message_id, filename, content_type, size_bytes, data, is_inline, content_id, kind, status, meta)
		VALUES ($1, $2, $3, $4, $5, $6, false, '', $7, $8, $9)`,
		workspaceID, messageID, filename, contentType, len(data), data, kind, status, meta)
	if err != nil {
		a.Log.Warn("could not file a WhatsApp attachment", "workspace", workspaceID, "message", messageID, "err", err)
	}
}

// vcardNumber pulls the first phone number out of a vCard's TEL line(s),
// e.g. "TEL;TYPE=CELL:+20123456789" -> "+20123456789". "" if the card somehow
// has none — the contact card still renders, just with only a name.
func vcardNumber(vcard string) string {
	for _, line := range strings.Split(vcard, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(line), "TEL") {
			continue
		}
		if i := strings.LastIndex(line, ":"); i >= 0 {
			return strings.TrimSpace(line[i+1:])
		}
	}
	return ""
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
