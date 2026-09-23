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

// Mailbox is one workspace's incoming mail account.
type Mailbox struct {
	WorkspaceID string
	Host        string
	Port        int
	Username    string
	Password    string
	TLSMode     string
	Folder      string
}

// fetchLimit caps one poll. A mailbox with ten thousand unread messages should
// drain over several polls rather than hold a connection (and a transaction)
// open for minutes.
const fetchLimit = 50

// imapTimeout bounds a whole poll: a mail server that accepts the connection
// and then stops answering must not pin the collector.
const imapTimeout = 90 * time.Second

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

	criteria := imap.NewSearchCriteria()
	criteria.WithoutFlags = []string{imap.SeenFlag}
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
		if err := File(ctx, a, mb.WorkspaceID, email); err != nil {
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
func File(ctx context.Context, a *app.App, workspaceID string, e *Email) error {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	db := a.SQLDB

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
			INSERT INTO conversations (workspace_id, channel, customer_name, customer_email, subject, last_message_at, unread)
			VALUES ($1, 'email', $2, $3, $4, $5, true) RETURNING id`,
			workspaceID, trimTo(e.FromName, 120), e.From, subjectOrDefault(e.Subject), e.Date,
		).Scan(&convID); err != nil {
			return err
		}
	}

	ins, err := db.ExecContext(sctx, `
		INSERT INTO messages (workspace_id, conversation_id, direction, body, external_id, created_at)
		VALUES ($1, $2, 'in', $3, $4, $5)
		ON CONFLICT (workspace_id, external_id) WHERE external_id <> '' DO NOTHING`,
		workspaceID, convID, e.Body, e.MessageID, e.Date)
	if err != nil {
		return err
	}
	inserted, _ := ins.RowsAffected()

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
	if err := autoLink(sctx, db, workspaceID, convID); err != nil {
		a.Log.Warn("could not match an email to a contact", "workspace", workspaceID, "err", err)
	}
	// Email used to arrive in silence: nothing published, so an open inbox did
	// not even refresh. Now it is announced like every other channel — once,
	// for a message actually written (a redelivery wrote nothing).
	if inserted > 0 {
		announceInbound(ctx, a, workspaceID, convID, e.Body, e.Date)
	}
	return nil
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
