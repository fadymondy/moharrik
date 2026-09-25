package contactcontext

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// SourceWhatsAppExport tags every message/attachment written by this
// package's Commit, and is what the automation-rule trigger
// (internal/db/post/174_contact_context.sql) checks to refuse to fire
// message.received/conversation.created for them.
const SourceWhatsAppExport = "whatsapp_export"

// maxBody mirrors internal/inbox's cap on one message's stored text.
const maxBody = 16 << 10

// maxAttachmentBytes caps one imported media file, the same order of
// magnitude as internal/inbox's message attachments (MH-556) — this is
// correspondence, not bulk file storage.
const maxAttachmentBytes = 15 << 20

// errContactNotFound and errImportNotFound are generic on purpose: never
// echo a path id back into an error body a caller can see, so a 404 for an
// id from a workspace this caller can't see looks identical to a 404 for
// one that never existed at all (see internal/server's cross-workspace sweep).
var (
	errContactNotFound = errors.New("contact not found")
	errImportNotFound  = errors.New("import not found")
)

// WhatsAppExportPreview is what the upload step returns before anything is written.
type WhatsAppExportPreview struct {
	Filename        string     `json:"filename"`
	MessageCount    int        `json:"message_count"`
	SystemCount     int        `json:"system_count"`
	MediaCount      int        `json:"media_count"`
	Senders         []string   `json:"senders"`
	DateFrom        *time.Time `json:"date_from,omitempty"`
	DateTo          *time.Time `json:"date_to,omitempty"`
	Sample          []Sample   `json:"sample"`
	NeedsSenderPick bool       `json:"needs_sender_pick" doc:"true when there are not exactly two senders, or no confident guess which one is the contact"`
	GuessedContact  string     `json:"guessed_contact,omitempty"`
	AlreadyImported int        `json:"already_imported_estimate" doc:"How many of the sampled messages already exist for this contact, by dedupe key — an early signal before a full re-import commits to 0 new."`
}

// Sample is one message shown in the preview.
type Sample struct {
	Time   time.Time `json:"time"`
	Sender string    `json:"sender,omitempty"`
	Text   string    `json:"text"`
	System bool      `json:"system"`
}

const sampleSize = 20

// BuildPreview parses an upload and describes it without writing anything.
func BuildPreview(ctx context.Context, a *app.App, workspaceID, contactID, filename string, data []byte, contactName string) (*WhatsAppExportPreview, error) {
	parsed, err := ParseUpload(filename, data)
	if err != nil {
		return nil, err
	}
	from, to := parsed.DateRange()
	p := &WhatsAppExportPreview{Filename: filename, Senders: parsed.Senders()}
	for _, m := range parsed.Messages {
		p.MessageCount++
		if m.System {
			p.SystemCount++
		}
		if m.MediaOnly {
			p.MediaCount++
		}
	}
	if !from.IsZero() {
		p.DateFrom = &from
	}
	if !to.IsZero() {
		p.DateTo = &to
	}
	guess, ok := parsed.GuessContactSender(contactName)
	p.GuessedContact = guess
	p.NeedsSenderPick = parsed.IsGroupOrAmbiguous() || !ok

	for i, m := range parsed.Messages {
		if i >= sampleSize {
			break
		}
		p.Sample = append(p.Sample, Sample{Time: m.Time, Sender: m.Sender, Text: trimTo(m.Text, 300), System: m.System})
	}

	// Best-effort "how many of these already exist" signal, so a repeated
	// upload of the same file previews as "0 new" before the user commits.
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var convID string
	_ = a.SQLDB.QueryRowContext(sctx, `SELECT id FROM conversations WHERE workspace_id=$1 AND channel='whatsapp' AND contact_id=$2`,
		workspaceID, contactID).Scan(&convID)
	if convID != "" {
		for _, m := range parsed.Messages {
			if m.System {
				continue
			}
			key := dedupeKey(m, guess)
			var exists string
			err := a.SQLDB.QueryRowContext(sctx, `SELECT id FROM messages WHERE workspace_id=$1 AND conversation_id=$2 AND dedupe_key=$3`,
				workspaceID, convID, key).Scan(&exists)
			if err == nil {
				p.AlreadyImported++
			}
		}
	}
	return p, nil
}

// WhatsAppImportResult is what Commit reports back.
type WhatsAppImportResult struct {
	ImportID      string `json:"import_id"`
	ConversationID string `json:"conversation_id"`
	MessagesAdded int    `json:"messages_added"`
	MessagesSkipped int  `json:"messages_skipped_duplicate"`
	MediaStored   int    `json:"media_stored"`
}

// dedupeKey combines the minute-truncated timestamp, the sender's role
// (in/out, from the chosen contact-sender mapping) and a hash of the
// normalized text — exports carry no seconds and no stable message id, so
// this is the strongest key available. Re-importing the same file recomputes
// the same keys and inserts nothing new (messages_import_dedupe_idx).
func dedupeKey(m Message, contactSender string) string {
	direction := "out"
	if strings.EqualFold(m.Sender, contactSender) {
		direction = "in"
	}
	minute := m.Time.Truncate(time.Minute).Unix()
	h := sha256.Sum256([]byte(normalizeForDedupe(m.Text)))
	return fmt.Sprintf("wex:%d:%s:%s", minute, direction, hex.EncodeToString(h[:8]))
}

// Commit parses the upload again (preview is stateless) and files it into the
// contact's WhatsApp conversation, creating one if none exists. contactSender
// must be one of the export's senders (WhatsAppExportPreview.Senders / a group-chat choice);
// everything else in the export is filed as the workspace side ('out').
//
// Every row this writes carries source=SourceWhatsAppExport and this run's
// import_id, which is (a) how the automation trigger refuses to fire for it
// (MH-586) and (b) what RemoveImport deletes by.
func Commit(ctx context.Context, a *app.App, workspaceID, contactID, createdBy, filename string, data []byte, contactSender string) (*WhatsAppImportResult, error) {
	contactSender = strings.TrimSpace(contactSender)
	if contactSender == "" {
		return nil, errors.New("contact_sender is required: pick which sender in the export is this contact")
	}
	parsed, err := ParseUpload(filename, data)
	if err != nil {
		return nil, err
	}
	found := false
	for _, s := range parsed.Senders() {
		if s == contactSender {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("contact_sender %q is not among the export's senders (%v)", contactSender, parsed.Senders())
	}

	sctx := tenant.WithWorkspace(ctx, workspaceID)
	db := a.SQLDB

	var contactName string
	if err := db.QueryRowContext(sctx, `SELECT name FROM contacts WHERE workspace_id=$1 AND id=$2`, workspaceID, contactID).Scan(&contactName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Never echo the id back into an error a caller can see — a 404 for
			// an id from a workspace this caller can't see must look identical
			// to a 404 for one that never existed at all.
			return nil, errContactNotFound
		}
		return nil, err
	}

	var convID string
	err = db.QueryRowContext(sctx, `SELECT id FROM conversations WHERE workspace_id=$1 AND channel='whatsapp' AND contact_id=$2`,
		workspaceID, contactID).Scan(&convID)
	if errors.Is(err, sql.ErrNoRows) {
		if err := db.QueryRowContext(sctx, `
			INSERT INTO conversations (workspace_id, channel, contact_id, customer_name, subject, last_message_at, unread, status)
			VALUES ($1, 'whatsapp', $2, $3, 'Imported WhatsApp history', now(), false, 'closed')
			RETURNING id`, workspaceID, contactID, trimTo(contactName, 120)).Scan(&convID); err != nil {
			return nil, fmt.Errorf("creating the WhatsApp conversation: %w", err)
		}
	} else if err != nil {
		return nil, err
	}

	importID := newID()
	res := &WhatsAppImportResult{ImportID: importID, ConversationID: convID}
	var lastAt time.Time

	for _, m := range parsed.Messages {
		if m.System {
			continue // system lines ("encrypted", "missed call") are not conversation content
		}
		direction := "out"
		if m.Sender == contactSender {
			direction = "in"
		}
		body := m.Text
		if m.MediaOnly {
			if body == "" || strings.Contains(strings.ToLower(body), "omitted") {
				if m.MediaFile != "" {
					body = "[attachment: " + m.MediaFile + "]"
				} else {
					body = "[media omitted]"
				}
			}
		}
		key := dedupeKey(m, contactSender)
		var msgID string
		err := db.QueryRowContext(sctx, `
			INSERT INTO messages (workspace_id, conversation_id, direction, body, source, import_id, dedupe_key, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (workspace_id, conversation_id, dedupe_key) WHERE dedupe_key <> '' DO NOTHING
			RETURNING id`,
			workspaceID, convID, direction, trimTo(body, maxBody), SourceWhatsAppExport, importID, key, m.Time,
		).Scan(&msgID)
		if errors.Is(err, sql.ErrNoRows) {
			res.MessagesSkipped++
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("filing message: %w", err)
		}
		res.MessagesAdded++
		if m.Time.After(lastAt) {
			lastAt = m.Time
		}

		if m.MediaFile != "" && parsed.Media != nil {
			if raw, ok := parsed.Media[m.MediaFile]; ok && len(raw) > 0 && len(raw) <= maxAttachmentBytes {
				if _, err := db.ExecContext(sctx, `
					INSERT INTO message_attachments (workspace_id, message_id, filename, content_type, size_bytes, data)
					VALUES ($1, $2, $3, $4, $5, $6)`,
					workspaceID, msgID, m.MediaFile, contentTypeFor(m.MediaFile), len(raw), raw); err != nil {
					return nil, fmt.Errorf("storing attachment %s: %w", m.MediaFile, err)
				}
				res.MediaStored++
			}
		}
	}

	from, to := parsed.DateRange()
	var fromP, toP *time.Time
	if !from.IsZero() {
		fromP = &from
	}
	if !to.IsZero() {
		toP = &to
	}
	if _, err := db.ExecContext(sctx, `
		INSERT INTO contact_context_imports
			(id, workspace_id, contact_id, conversation_id, kind, filename, contact_sender, message_count, media_count, skipped_count, date_from, date_to, created_by)
		VALUES ($1, $2, $3, $4, 'whatsapp_export', $5, $6, $7, $8, $9, $10, $11, $12)`,
		importID, workspaceID, contactID, convID, filename, contactSender,
		res.MessagesAdded, res.MediaStored, res.MessagesSkipped, fromP, toP, createdBy); err != nil {
		return nil, fmt.Errorf("recording the import: %w", err)
	}

	// Update the conversation's bookkeeping without going through
	// inbox.FileWhatsApp (which would announce, auto-link and refresh the
	// avatar) — an import is silent by design.
	if !lastAt.IsZero() {
		if _, err := db.ExecContext(sctx, `
			UPDATE conversations SET last_message_at = GREATEST(last_message_at, $3), updated_at = now()
			WHERE workspace_id=$1 AND id=$2`, workspaceID, convID, lastAt); err != nil {
			a.Log.Warn("contactcontext: could not bump conversation timestamp after import", "err", err)
		}
	}

	return res, nil
}

// RemoveImport deletes exactly one import's messages, their attachments (via
// the message FK cascade) and the import row itself. Nothing else on the
// conversation is touched.
func RemoveImport(ctx context.Context, a *app.App, workspaceID, contactID, importID string) error {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var ownerContact string
	if err := a.SQLDB.QueryRowContext(sctx, `SELECT contact_id FROM contact_context_imports WHERE workspace_id=$1 AND id=$2`,
		workspaceID, importID).Scan(&ownerContact); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errImportNotFound
		}
		return err
	}
	if ownerContact != contactID {
		return errImportNotFound
	}
	tx, err := a.SQLDB.BeginTx(sctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(sctx, `DELETE FROM message_attachments WHERE workspace_id=$1 AND message_id IN (
		SELECT id FROM messages WHERE workspace_id=$1 AND import_id=$2)`, workspaceID, importID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(sctx, `DELETE FROM messages WHERE workspace_id=$1 AND import_id=$2`, workspaceID, importID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(sctx, `DELETE FROM contact_context_notes WHERE workspace_id=$1 AND import_id=$2`, workspaceID, importID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(sctx, `DELETE FROM contact_context_imports WHERE workspace_id=$1 AND id=$2`, workspaceID, importID); err != nil {
		return err
	}
	return tx.Commit()
}

// ListImports lists a contact's committed imports, newest first.
func ListImports(ctx context.Context, a *app.App, workspaceID, contactID string) ([]ImportRow, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	rows, err := a.SQLDB.QueryContext(sctx, `
		SELECT id, filename, contact_sender, message_count, media_count, skipped_count, date_from, date_to, created_at
		FROM contact_context_imports WHERE workspace_id=$1 AND contact_id=$2 ORDER BY created_at DESC`,
		workspaceID, contactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImportRow{}
	for rows.Next() {
		var r ImportRow
		if err := rows.Scan(&r.ID, &r.Filename, &r.ContactSender, &r.MessageCount, &r.MediaCount, &r.SkippedCount, &r.DateFrom, &r.DateTo, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ImportRow is one row of ListImports.
type ImportRow struct {
	ID            string     `json:"id"`
	Filename      string     `json:"filename"`
	ContactSender string     `json:"contact_sender"`
	MessageCount  int        `json:"message_count"`
	MediaCount    int        `json:"media_count"`
	SkippedCount  int        `json:"skipped_count"`
	DateFrom      *time.Time `json:"date_from,omitempty"`
	DateTo        *time.Time `json:"date_to,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

func trimTo(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func contentTypeFor(filename string) string {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".gif"):
		return "image/gif"
	case strings.HasSuffix(lower, ".mp4"):
		return "video/mp4"
	case strings.HasSuffix(lower, ".opus"), strings.HasSuffix(lower, ".ogg"):
		return "audio/ogg"
	case strings.HasSuffix(lower, ".pdf"):
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
