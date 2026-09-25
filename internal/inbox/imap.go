package inbox

import (
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/recency"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The IMAP side of the email channel (MH-133).

The collector is the one part of the inbox with no request behind it, so it
scopes itself with tenant.WithWorkspace, one workspace at a time — the same
shape as the trends collector, and for the same reason: RLS is what keeps one
workspace's mail out of another's inbox, and RLS needs a workspace on the
context.

Only UNSEEN messages are fetched, and each one is marked \Seen after it is
filed. That makes the mailbox itself the queue — there is no cursor to get out
of step, and a message that failed to file is retried on the next poll because
it was never marked.
*/

// Mailbox is one of a workspace's incoming mail accounts (MH-555: a workspace
// may have several, the way it may have several SMTP services).
type Mailbox struct {
	ID            string
	WorkspaceID   string
	Name          string
	Host          string
	Port          int
	Username      string
	Password      string
	TLSMode       string
	Folder        string
	SMTPServiceID string // which of the workspace's SMTP services a reply on this mailbox goes out through; "" = the workspace default
	// ConnectedAt is when the mailbox was added. Poll files only mail sent
	// from (a day before) then on — see internal/recency.
	ConnectedAt time.Time
}

// fetchLimit caps one poll. A mailbox with ten thousand unread messages should
// drain over several polls rather than hold a connection (and a transaction)
// open for minutes.
const fetchLimit = 50

// imapTimeout bounds a whole poll: a mail server that accepts the connection
// and then stops answering must not pin the collector.
const imapTimeout = 90 * time.Second

// testTimeout bounds a "test connection" check (MH-555) — shorter than a poll
// because it does nothing but dial, log in and open the folder.
const testTimeout = 20 * time.Second

// TestConnection checks a mailbox's credentials without collecting anything:
// dial, log in, open the folder, log out. Nothing is fetched and nothing is
// marked \Seen, so running it never loses or duplicates mail.
func TestConnection(ctx context.Context, mb Mailbox) error {
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		c, err := dial(mb)
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = c.Logout() }()
		if err := c.Login(mb.Username, mb.Password); err != nil {
			done <- fmt.Errorf("could not sign in to the mailbox: %w", err)
			return
		}
		folder := mb.Folder
		if strings.TrimSpace(folder) == "" {
			folder = "INBOX"
		}
		if _, err := c.Select(folder, true); err != nil { // read-only: never risks marking anything
			done <- fmt.Errorf("could not open %s: %w", folder, err)
			return
		}
		done <- nil
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("the mail server did not answer in time")
	}
}

// listTimeout bounds ListMessages — generous next to a poll, since browsing a
// date range can touch far more messages than "what's new since last time".
const listTimeout = 5 * time.Minute

// listFetchLimit caps one ListMessages call, the same reasoning as fetchLimit
// for Poll: a mailbox with years of history should be browsed a page at a
// time by the caller (moving `until` back), not read in one huge fetch.
const listFetchLimit = 500

// ListMessages reads a mailbox's mail with a Date header in [since, until),
// read-only: nothing is filed into the inbox, nothing is marked \Seen, and
// none of it touches the database. It exists for other features built ON TOP
// of a mailbox — a contact/history importer, for one — that need to browse
// what a mailbox holds without it becoming part of the conversation list Poll
// feeds. Poll and ListMessages are the only two things in this package that
// open an IMAP connection, and they stay deliberately separate: Poll's
// contract is "the mailbox itself is the queue" (imap.go's own doc), and that
// contract breaks the moment something else reads the mailbox and forms
// opinions about what still needs collecting.
//
// until zero means "up to now". Messages come back newest first, capped at
// listFetchLimit — a caller wanting more pages back further by moving until
// to the oldest Date it got.
func ListMessages(ctx context.Context, mb Mailbox, since, until time.Time) ([]*Email, error) {
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()

	c, err := dial(mb)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Logout() }()

	if err := c.Login(mb.Username, mb.Password); err != nil {
		return nil, fmt.Errorf("could not sign in to the mailbox: %w", err)
	}

	folder := mb.Folder
	if strings.TrimSpace(folder) == "" {
		folder = "INBOX"
	}
	// Read-only (the second argument): this connection can never mark a
	// message \Seen or otherwise change the mailbox, whatever else in this
	// function might be wrong.
	if _, err := c.Select(folder, true); err != nil {
		return nil, fmt.Errorf("could not open %s: %w", folder, err)
	}

	criteria := imap.NewSearchCriteria()
	if !since.IsZero() {
		criteria.SentSince = since
	}
	if !until.IsZero() {
		criteria.SentBefore = until
	}
	ids, err := c.Search(criteria)
	if err != nil {
		return nil, fmt.Errorf("could not search the mailbox: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	// Newest first, capped: Search returns ascending UIDs/sequence numbers,
	// which on almost every server tracks arrival order.
	if len(ids) > listFetchLimit {
		ids = ids[len(ids)-listFetchLimit:]
	}
	for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
		ids[i], ids[j] = ids[j], ids[i]
	}

	seqset := new(imap.SeqSet)
	seqset.AddNum(ids...)
	section := &imap.BodySectionName{}
	messages := make(chan *imap.Message, 10)
	done := make(chan error, 1)
	go func() { done <- c.Fetch(seqset, []imap.FetchItem{section.FetchItem()}, messages) }()

	byUID := make(map[uint32]*Email, len(ids))
	for msg := range messages {
		body := msg.GetBody(section)
		if body == nil {
			continue
		}
		email, err := ParseEmail(body)
		if err != nil {
			continue // unparseable: skipped, not an error for the whole browse
		}
		byUID[msg.SeqNum] = email
	}
	if err := <-done; err != nil {
		return nil, fmt.Errorf("could not read the mailbox: %w", err)
	}

	out := make([]*Email, 0, len(byUID))
	for _, id := range ids {
		if e, ok := byUID[id]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// Poll fetches this mailbox's unseen mail and files it. It returns how many
// messages were filed.
func Poll(ctx context.Context, a *app.App, mb Mailbox) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, imapTimeout)
	defer cancel()

	c, err := dial(mb)
	if err != nil {
		return 0, err
	}
	defer func() { _ = c.Logout() }()

	if err := c.Login(mb.Username, mb.Password); err != nil {
		// The server's own words are the useful part ("authentication failed",
		// "application-specific password required"), so they are kept.
		return 0, fmt.Errorf("could not sign in to the mailbox: %w", err)
	}

	folder := mb.Folder
	if strings.TrimSpace(folder) == "" {
		folder = "INBOX"
	}
	if _, err := c.Select(folder, false); err != nil {
		return 0, fmt.Errorf("could not open %s: %w", folder, err)
	}

	// Unseen AND recent. Unseen alone reached back to 2019 on a mailbox whose
	// owner never read their newsletters — and marked all of it read in their
	// own client. IMAP's SINCE is date-granular, so the exact cutoff is checked
	// again per message below.
	cutoff := recency.Cutoff(mb.ConnectedAt)
	criteria := imap.NewSearchCriteria()
	criteria.WithoutFlags = []string{imap.SeenFlag}
	criteria.Since = cutoff
	ids, err := c.Search(criteria)
	if err != nil {
		return 0, fmt.Errorf("could not search the mailbox: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if len(ids) > fetchLimit {
		ids = ids[:fetchLimit]
	}

	seqset := new(imap.SeqSet)
	seqset.AddNum(ids...)
	section := &imap.BodySectionName{}
	messages := make(chan *imap.Message, 10)
	done := make(chan error, 1)
	go func() { done <- c.Fetch(seqset, []imap.FetchItem{section.FetchItem()}, messages) }()

	filed := 0
	var filedIDs []uint32
	for msg := range messages {
		body := msg.GetBody(section)
		if body == nil {
			continue
		}
		email, err := ParseEmail(body)
		if err != nil {
			// Unparseable: mark it seen anyway, or it is retried for ever.
			filedIDs = append(filedIDs, msg.SeqNum)
			continue
		}
		if recency.TooOld(email.Date, mb.ConnectedAt) {
			// Older than the channel: not ours to file, and not ours to mark
			// read either — it stays exactly as the owner left it.
			continue
		}
		if err := File(ctx, a, mb.WorkspaceID, mb.ID, email); err != nil {
			// Left UNSEEN on purpose: the next poll retries it. The mailbox is
			// the queue, so a failure here loses nothing.
			continue
		}
		filed++
		filedIDs = append(filedIDs, msg.SeqNum)
	}
	if err := <-done; err != nil {
		return filed, fmt.Errorf("could not read the mailbox: %w", err)
	}

	if len(filedIDs) > 0 {
		marked := new(imap.SeqSet)
		marked.AddNum(filedIDs...)
		item := imap.FormatFlagsOp(imap.AddFlags, true)
		if err := c.Store(marked, item, []any{imap.SeenFlag}, nil); err != nil {
			// Filed but not marked: the next poll would file them again. The
			// unique index on (workspace_id, external_id) is what stops that
			// becoming duplicates in the thread.
			return filed, fmt.Errorf("mail was filed but could not be marked read: %w", err)
		}
	}
	return filed, nil
}

func dial(mb Mailbox) (*client.Client, error) {
	addr := fmt.Sprintf("%s:%d", mb.Host, mb.Port)
	if mb.TLSMode == "none" {
		// Plaintext. The password crosses the wire in the clear, which is why
		// the UI labels this "no encryption" rather than something neutral —
		// it is for a mail server on the same host, and nothing else.
		c, err := client.Dial(addr)
		if err != nil {
			return nil, fmt.Errorf("could not reach %s: %w", addr, err)
		}
		return c, nil
	}
	if mb.TLSMode == "starttls" {
		c, err := client.Dial(addr)
		if err != nil {
			return nil, fmt.Errorf("could not reach %s: %w", addr, err)
		}
		if err := c.StartTLS(&tls.Config{ServerName: mb.Host}); err != nil {
			_ = c.Logout()
			return nil, fmt.Errorf("STARTTLS failed: %w", err)
		}
		return c, nil
	}
	c, err := client.DialTLS(addr, &tls.Config{ServerName: mb.Host})
	if err != nil {
		return nil, fmt.Errorf("could not reach %s over TLS: %w", addr, err)
	}
	return c, nil
}

// File puts one email on the right conversation, creating it if this is a new
// thread. It is exported so the tests and the "check this mailbox now" button
// share exactly one code path with the collector.
//
// mailboxID names which of the workspace's mailboxes (MH-555) this email
// arrived on; "" is accepted (a test, or a caller with no mailbox context) and
// stores NULL. findThread and the "same sender, same subject" fallback stay
// scoped to workspace + channel, NOT to mailboxID — deliberately: two
// mailboxes on the same workspace collecting mail from the same customer
// dedupe and thread together exactly as if they were one mailbox, because a
// customer does not know or care which of the workspace's addresses they
// wrote to.
func File(ctx context.Context, a *app.App, workspaceID, mailboxID string, e *Email) error {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	db := a.SQLDB
	mbID := nullable(mailboxID)

	// 1. Already filed? A redelivery is the same row, not a second message.
	if e.MessageID != "" {
		var exists string
		err := db.QueryRowContext(sctx, `
			SELECT id FROM messages WHERE workspace_id = $1 AND external_id = $2`,
			workspaceID, e.MessageID).Scan(&exists)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}

	convID, err := findThread(sctx, db, workspaceID, e)
	if err != nil {
		return err
	}

	if convID == "" {
		if err := db.QueryRowContext(sctx, `
			INSERT INTO conversations (workspace_id, channel, mailbox_id, customer_name, customer_email, subject, last_message_at, unread)
			VALUES ($1, 'email', $2, $3, $4, $5, $6, true) RETURNING id`,
			workspaceID, mbID, trimTo(e.FromName, 120), e.From, subjectOrDefault(e.Subject), e.Date,
		).Scan(&convID); err != nil {
			return err
		}
	}

	var msgID string
	err = db.QueryRowContext(sctx, `
		INSERT INTO messages (workspace_id, conversation_id, direction, body, body_html, mailbox_id, external_id, created_at)
		VALUES ($1, $2, 'in', $3, $4, $5, $6, $7)
		ON CONFLICT (workspace_id, external_id) WHERE external_id <> '' DO NOTHING
		RETURNING id`,
		workspaceID, convID, e.Body, e.BodyHTML, mbID, e.MessageID, e.Date).Scan(&msgID)
	inserted := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if inserted && len(e.Attachments) > 0 {
		if err := storeAttachments(sctx, db, workspaceID, msgID, e.Attachments); err != nil {
			// The message itself is already filed; a failed attachment insert
			// must not lose it or retry the whole email forever.
			a.Log.Warn("could not store an email's attachments", "workspace", workspaceID, "message", msgID, "err", err)
		}
	}

	// An inbound message makes the thread unread for the team again, and
	// reopens it rather than answering into a closed conversation.
	_, err = db.ExecContext(sctx, `
		UPDATE conversations SET last_message_at = GREATEST(last_message_at, $3), unread = true, updated_at = now(), `+reopenOnInbound+`
		WHERE workspace_id = $1 AND id = $2`, workspaceID, convID, e.Date)
	if err != nil {
		return err
	}
	// A sender the CRM already knows by this exact address is linked to the
	// thread (MH-379). Best effort: the message is filed either way.
	var linkedContactID string
	if err := autoLink(sctx, db, workspaceID, convID); err != nil {
		a.Log.Warn("could not match an email to a contact", "workspace", workspaceID, "err", err)
	} else {
		_ = db.QueryRowContext(sctx, `SELECT contact_id FROM conversations WHERE workspace_id = $1 AND id = $2`,
			workspaceID, convID).Scan(&linkedContactID)
	}
	// MH-588: Gravatar is the last-resort photo source (task spec: "only when
	// nothing else exists") — every other channel's fetcher runs on ITS OWN
	// inbound message, so an address that also messages on WhatsApp or has an
	// uploaded photo keeps that one; queueGravatarRefresh below only ever
	// writes when photo_source is still empty.
	if linkedContactID != "" {
		queueGravatarRefresh(a, workspaceID, "contact", linkedContactID, e.From)
	} else {
		queueGravatarRefresh(a, workspaceID, "conversation", convID, e.From)
	}
	// Email used to arrive in silence: nothing published, so an open inbox did
	// not even refresh. Now it is announced like every other channel — once,
	// for a message actually written (a redelivery wrote nothing).
	if inserted {
		announceInbound(ctx, a, workspaceID, convID, e.Body, e.Date)
	}
	return nil
}

// storeAttachments writes one message's attachments. Best-effort and
// all-or-nothing per message: a partial write would offer a thread that looks
// complete but is missing a file, which is worse than missing them all.
func storeAttachments(ctx context.Context, db *sql.DB, workspaceID, messageID string, atts []Attachment) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for _, at := range atts {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO message_attachments (workspace_id, message_id, filename, content_type, size_bytes, data, is_inline, content_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			workspaceID, messageID, at.Filename, at.ContentType, len(at.Data), at.Data, at.Inline, at.ContentID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// findThread answers "which conversation is this" in the order the rules are
// trustworthy: an explicit Message-ID reference first, then the sender's own
// open thread with the same subject, and otherwise nothing (a new thread).
func findThread(ctx context.Context, db *sql.DB, workspaceID string, e *Email) (string, error) {
	for _, key := range e.ThreadKeys() {
		var convID string
		err := db.QueryRowContext(ctx, `
			SELECT conversation_id FROM messages WHERE workspace_id = $1 AND external_id = $2`,
			workspaceID, key).Scan(&convID)
		if err == nil {
			return convID, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}

	// Fallback: the same person, the same subject, a thread that is not closed.
	// Paired with the address on purpose — subjects collide ("Hello"), and
	// matching on subject alone would drop one customer into another's thread.
	if e.From == "" {
		return "", nil
	}
	subject := NormalisedSubject(e.Subject)
	if subject == "" {
		return "", nil
	}
	var convID string
	err := db.QueryRowContext(ctx, `
		SELECT id FROM conversations
		WHERE workspace_id = $1 AND channel = 'email' AND lower(customer_email) = $2
		  AND status <> 'closed'
		  AND regexp_replace(lower(subject), '^((re|fw|fwd)\s*:\s*)+', '') = $3
		ORDER BY last_message_at DESC LIMIT 1`,
		workspaceID, e.From, subject).Scan(&convID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return convID, err
}
