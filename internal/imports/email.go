package imports

import (
	"sort"
	"strings"
	"time"

	"context"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/marketing"
)

/*
The email import source (MH-562): a workspace's IMAP mailbox (MH-555/556),
read through inbox.LoadMailboxForBrowsing and inbox.ListMessages — the only
two functions internal/inbox exposes for this — and turned into contacts and
inbox threads, one per sender address.

No IMAP code lives here: everything about dialing, authenticating and parsing
a mailbox stays inside internal/inbox, exactly as its own doc comment
describes. This file only groups what ListMessages already returned by
sender, and reuses upsertConversation/marketing.UpsertContactIdentity the same
way whatsapp.go does — the point of the Provider interface is that a second
source is new grouping logic over old primitives, not a new integration.

A mailbox has no groups, so every Item.Kind here is "direct". A workspace's
own outbound mail never appears (the default Folder is INBOX, so
ListMessages only ever sees what arrived), so Flags are the only signal that
an address is not a person a workspace member replies to: an
obviously-automated sender (noreply@, mailer-daemon@, and the like) is
flagged "no_reply" and a bulk-mail-shaped one is flagged "mailing_list" —
both filtered out of Preview by default (Filters.IncludeFlagged), matching
MH-562's "preview with filters".
*/

const KindEmail = "email"

// emailWindow is how far back Preview and Import look by default — generous
// enough to show a mailbox's real correspondents without pulling years of
// history just to build a list.
const emailWindow = 90 * 24 * time.Hour

// emailHistoryDepth is ImportRequest.HistoryDepth's default, matching
// WhatsApp's.
const emailHistoryDepth = 50

// EmailProvider implements Provider against internal/inbox's mailboxes.
type EmailProvider struct{}

func (EmailProvider) Kind() string { return KindEmail }

func (EmailProvider) Connections(ctx context.Context, a *app.App, wsID string) ([]ImportConnection, error) {
	boxes, err := inbox.ListMailboxesTool(ctx, a)
	if err != nil {
		return nil, err
	}
	out := make([]ImportConnection, 0, len(boxes))
	for _, mb := range boxes {
		ready := mb.Enabled && mb.HasPassword
		note := ""
		switch {
		case !mb.HasPassword:
			note = "this mailbox has no password set yet"
		case !mb.Enabled:
			note = "this mailbox is disabled"
		}
		out = append(out, ImportConnection{ID: mb.ID, Label: mb.Username, Ready: ready, Note: note})
	}
	return out, nil
}

// emailFlags guesses whether an address is a person or an automated/bulk
// sender, from the local-part alone — the same kind of heuristic
// whatsapp.go's isArabic uses for locale, not a claim about the mail's real
// nature.
func emailFlags(address string) []string {
	local, _, _ := strings.Cut(address, "@")
	local = strings.ToLower(local)
	var flags []string
	switch {
	case strings.Contains(local, "noreply"), strings.Contains(local, "no-reply"),
		strings.Contains(local, "donotreply"), strings.Contains(local, "do-not-reply"),
		local == "mailer-daemon", local == "postmaster":
		flags = append(flags, "no_reply")
	case strings.Contains(local, "newsletter"), strings.Contains(local, "notifications"),
		strings.Contains(local, "digest"), strings.Contains(local, "updates"),
		strings.Contains(local, "marketing"), strings.Contains(local, "bounce"):
		flags = append(flags, "mailing_list")
	}
	return flags
}

// groupedEmails is one sender's messages within the browsed window, oldest to
// newest — what both Preview and Import group ListMessages's flat list into.
type groupedEmail struct {
	address string
	name    string
	msgs    []*inbox.Email // newest first
}

func groupBySender(emails []*inbox.Email) []groupedEmail {
	order := []string{}
	byAddr := map[string]*groupedEmail{}
	for _, e := range emails {
		addr := strings.ToLower(strings.TrimSpace(e.From))
		if addr == "" {
			continue
		}
		g, ok := byAddr[addr]
		if !ok {
			g = &groupedEmail{address: addr}
			byAddr[addr] = g
			order = append(order, addr)
		}
		if g.name == "" && e.FromName != "" {
			g.name = e.FromName
		}
		g.msgs = append(g.msgs, e)
	}
	out := make([]groupedEmail, 0, len(order))
	for _, addr := range order {
		g := *byAddr[addr]
		sort.Slice(g.msgs, func(i, j int) bool { return g.msgs[i].Date.After(g.msgs[j].Date) })
		out = append(out, g)
	}
	return out
}

func windowFor(f Filters) (since, until time.Time) {
	since = time.Now().Add(-emailWindow)
	if f.Since != nil {
		since = *f.Since
	}
	return since, time.Time{} // until zero = "up to now" (ListMessages's own convention)
}

func (EmailProvider) Preview(ctx context.Context, a *app.App, wsID, connID string, f Filters) ([]Item, error) {
	if f.Kind == "group" {
		return []Item{}, nil // a mailbox has no groups
	}
	mb, err := inbox.LoadMailboxForBrowsing(ctx, a, wsID, connID)
	if err != nil {
		return nil, err
	}
	since, until := windowFor(f)
	emails, err := inbox.ListMessages(ctx, *mb, since, until)
	if err != nil {
		return nil, err
	}
	groups := groupBySender(emails)
	items := make([]Item, 0, len(groups))
	for _, g := range groups {
		it := Item{
			ID:           g.address,
			Kind:         "direct",
			Name:         g.name,
			Identity:     g.address,
			MessageCount: len(g.msgs),
			Flags:        emailFlags(g.address),
		}
		if len(g.msgs) > 0 {
			latest := g.msgs[0]
			it.LastMessage = latest.Subject
			if it.LastMessage == "" {
				it.LastMessage = latest.Body
			}
			t := latest.Date
			it.LastMessageAt = &t
		}
		items = append(items, it)
	}
	items = applyTextFilter(items, f.Q)
	if !f.IncludeFlagged {
		filtered := items[:0]
		for _, it := range items {
			if len(it.Flags) == 0 {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}
	sort.Slice(items, func(i, j int) bool {
		ti, tj := items[i].LastMessageAt, items[j].LastMessageAt
		if ti == nil || tj == nil {
			return false
		}
		return ti.After(*tj)
	})
	return items, nil
}

func (EmailProvider) Import(ctx context.Context, a *app.App, wsID string, job *Job, req ImportRequest, progress func(int)) error {
	mb, err := inbox.LoadMailboxForBrowsing(ctx, a, wsID, req.ConnectionID)
	if err != nil {
		return err
	}
	since := time.Now().Add(-emailWindow)
	emails, err := inbox.ListMessages(ctx, *mb, since, time.Time{})
	if err != nil {
		return err
	}
	groups := groupBySender(emails)
	byAddr := map[string]groupedEmail{}
	for _, g := range groups {
		byAddr[g.address] = g
	}

	depth := req.HistoryDepth
	if depth <= 0 {
		depth = emailHistoryDepth
	}

	done := 0
	for _, addr := range req.Items {
		g, ok := byAddr[addr]
		if !ok {
			a.Log.Warn("email import: address not in the browsed window", "workspace", wsID, "address", addr)
			job.Skipped++
			done++
			progress(done)
			continue
		}
		if err := importSender(ctx, a, wsID, job, g, depth); err != nil {
			a.Log.Warn("email import: sender failed", "workspace", wsID, "address", addr, "err", err)
			job.Skipped++
		}
		done++
		progress(done)
	}
	return nil
}

func importSender(ctx context.Context, a *app.App, wsID string, job *Job, g groupedEmail, depth int) error {
	name := g.name
	if name == "" {
		name = g.address
	}
	locale := localeOf(name)
	contactID, created, err := marketing.UpsertContactIdentity(ctx, a.SQLDB, wsID, g.address, "", "", name, locale, "email_import")
	if err != nil {
		return err
	}
	if created {
		job.ContactsCreated++
	} else {
		job.ContactsReused++
	}

	convID, convCreated, err := upsertConversation(ctx, a, wsID, KindEmail, g.address, false, &contactID, name, "")
	if err != nil {
		return err
	}
	if convCreated {
		job.ConversationsCreated++
	} else {
		job.ConversationsReused++
	}

	msgs := g.msgs
	if len(msgs) > depth {
		msgs = msgs[:depth]
	}
	n, err := insertEmailHistory(ctx, a, wsID, convID, msgs)
	if err != nil {
		return err
	}
	job.MessagesImported += n
	return nil
}

// insertEmailHistory files a batch of past emails, dedup'd on external_id
// (messages_external_uq, same index whatsapp.go's insertHistory uses) so a
// re-import adds nothing already there. MessageID is the natural external id
// — an IMAP server never issues the same one twice for a real message.
func insertEmailHistory(ctx context.Context, a *app.App, wsID, convID string, msgs []*inbox.Email) (int, error) {
	n := 0
	var lastAt time.Time
	for _, m := range msgs {
		text := m.Subject
		if m.Body != "" {
			if text != "" {
				text += "\n\n"
			}
			text += m.Body
		}
		extID := m.MessageID
		if text == "" || extID == "" {
			continue
		}
		res, err := a.SQLDB.ExecContext(ctx, `
			INSERT INTO messages (workspace_id, conversation_id, direction, body, external_id, created_at)
			VALUES ($1, $2, 'in', $3, $4, $5)
			ON CONFLICT (workspace_id, external_id) WHERE external_id <> '' DO NOTHING`,
			wsID, convID, text, extID, m.Date)
		if err != nil {
			return n, err
		}
		if rows, _ := res.RowsAffected(); rows > 0 {
			n++
		}
		if m.Date.After(lastAt) {
			lastAt = m.Date
		}
	}
	if !lastAt.IsZero() {
		if err := syncConversationTimes(ctx, a, wsID, convID); err != nil {
			return n, err
		}
	}
	return n, nil
}

func (EmailProvider) Summary(ctx context.Context, a *app.App, wsID, conversationID string) (*Summary, error) {
	return genericSummary(ctx, a, wsID, conversationID, KindEmail)
}

var _ Provider = EmailProvider{}
