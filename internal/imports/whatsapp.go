package imports

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

/*
The WhatsApp import source (MH-562): a workspace's linked Evolution API
instance, read (never written to beyond what SendText already does elsewhere)
for its chats and groups, turned into contacts and inbox threads.

Groups become group conversations (channel=whatsapp, is_group=true), keyed on
the group JID as external_thread_id so a second import finds the same thread.
Participants are recorded in conversation_participants always, and turned
into contacts only when ImportRequest.ImportParticipants asks for it — a
group has people in it who never wrote to this workspace and never agreed to
anything, and MH-410's rule is that nothing here implies an opt-in either way.
*/

const KindWhatsApp = "whatsapp"

// WhatsAppProvider implements Provider against internal/whatsapp's Evolution
// client. One connection per workspace today (one linked number); Connections
// returns a single entry so the shape is already right for a workspace that
// links more than one later.
type WhatsAppProvider struct {
	// Config, when set, is used instead of whatsapp.LoadConfig() — the fake
	// Evolution server tests point this at itself. Nil means "read env the
	// normal way", so production wiring needs nothing extra.
	Config *whatsapp.Config
}

func (WhatsAppProvider) Kind() string { return KindWhatsApp }

func (p WhatsAppProvider) cfg() whatsapp.Config {
	if p.Config != nil {
		return *p.Config
	}
	return whatsapp.LoadConfig()
}

const connectionID = "default"

func (p WhatsAppProvider) Connections(ctx context.Context, a *app.App, wsID string) ([]ImportConnection, error) {
	link, err := whatsapp.Load(ctx, a, wsID)
	if errors.Is(err, whatsapp.ErrNoLink) {
		return []ImportConnection{{ID: connectionID, Label: "", Ready: false, Note: "no WhatsApp number is linked yet"}}, nil
	}
	if err != nil {
		return nil, err
	}
	ready := link.State == "open"
	note := ""
	switch {
	case link.State != "open":
		note = "WhatsApp is not connected (state: " + link.State + ")"
	case link.Phone == "":
		// Connected, but Evolution has not reported a number yet (pairing not
		// finished) — still not ready to preview/import from.
		ready = false
		note = "WhatsApp is connected but has not reported a number yet"
	}
	return []ImportConnection{{ID: connectionID, Label: link.Phone, Ready: ready, Note: note}}, nil
}

// client resolves the workspace's Evolution client, or an error naming why
// there isn't one — the same message a preview/import should surface.
func (p WhatsAppProvider) client(ctx context.Context, a *app.App, wsID string) (*whatsapp.Client, error) {
	link, err := whatsapp.Load(ctx, a, wsID)
	if errors.Is(err, whatsapp.ErrNoLink) {
		return nil, errors.New("no WhatsApp number is linked for this workspace")
	}
	if err != nil {
		return nil, err
	}
	if link.State != "open" {
		return nil, errors.New("WhatsApp is not connected (state: " + link.State + ")")
	}
	return whatsapp.ClientFor(p.cfg(), link), nil
}

func (p WhatsAppProvider) Preview(ctx context.Context, a *app.App, wsID, connID string, f Filters) ([]Item, error) {
	cl, err := p.client(ctx, a, wsID)
	if err != nil {
		return nil, err
	}
	var items []Item
	if f.Kind == "" || f.Kind == "direct" {
		chats, err := cl.ListChats(ctx)
		if err != nil {
			return nil, err
		}
		for _, c := range chats {
			phone := c.Phone()
			if phone == "" {
				continue
			}
			it := Item{ID: c.RemoteJID, Kind: "direct", Name: c.DisplayName(), Identity: "+" + phone}
			if c.LastMessage != nil {
				it.LastMessage = c.LastMessage.Text()
				t := time.Unix(int64(c.LastMessage.Timestamp), 0).UTC()
				it.LastMessageAt = &t
			}
			items = append(items, it)
		}
	}
	if f.Kind == "" || f.Kind == "group" {
		groups, err := cl.ListGroups(ctx)
		if err != nil {
			return nil, err
		}
		for _, g := range groups {
			it := Item{ID: g.ID, Kind: "group", Name: g.Subject, Identity: g.ID, MessageCount: g.Size}
			for _, part := range g.Participants {
				if ph := part.Phone(); ph != "" {
					it.Participants = append(it.Participants, Participant{Identity: "+" + ph, Name: part.Name})
				}
			}
			if g.LastMessage != nil {
				it.LastMessage = g.LastMessage.Text()
				t := time.Unix(int64(g.LastMessage.Timestamp), 0).UTC()
				it.LastMessageAt = &t
			}
			items = append(items, it)
		}
	}
	items = applyTextFilter(items, f.Q)
	if f.Since != nil {
		filtered := items[:0]
		for _, it := range items {
			if it.LastMessageAt == nil || !it.LastMessageAt.Before(*f.Since) {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}
	if items == nil {
		items = []Item{}
	}
	return items, nil
}

func applyTextFilter(items []Item, q string) []Item {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return items
	}
	out := items[:0]
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.Name), q) || strings.Contains(strings.ToLower(it.Identity), q) {
			out = append(out, it)
		}
	}
	return out
}

// isArabic guesses a contact's locale from a snippet of what they wrote —
// there is no locale field on a WhatsApp chat.
func isArabic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Arabic, r) {
			return true
		}
	}
	return false
}

func localeOf(s string) string {
	if isArabic(s) {
		return "ar"
	}
	return "en"
}

func (p WhatsAppProvider) Import(ctx context.Context, a *app.App, wsID string, job *Job, req ImportRequest, progress func(int)) error {
	cl, err := p.client(ctx, a, wsID)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, id := range req.Items {
		want[id] = true
	}

	chats, err := cl.ListChats(ctx)
	if err != nil {
		return err
	}
	groups, err := cl.ListGroups(ctx)
	if err != nil {
		return err
	}

	depth := req.HistoryDepth
	if depth <= 0 {
		depth = 50
	}

	done := 0
	for _, c := range chats {
		if !want[c.RemoteJID] {
			continue
		}
		if err := p.importDirect(ctx, a, wsID, job, cl, c, depth); err != nil {
			a.Log.Warn("whatsapp import: direct chat failed", "workspace", wsID, "jid", c.RemoteJID, "err", err)
			job.Skipped++
		}
		done++
		progress(done)
	}
	for _, g := range groups {
		if !want[g.ID] {
			continue
		}
		if err := p.importGroup(ctx, a, wsID, job, cl, g, depth, req.ImportParticipants); err != nil {
			a.Log.Warn("whatsapp import: group failed", "workspace", wsID, "jid", g.ID, "err", err)
			job.Skipped++
		}
		done++
		progress(done)
	}
	return nil
}

// upsertConversation finds or creates the conversation for one external
// thread, idempotently (conversations_external_thread_uniq).
func upsertConversation(ctx context.Context, a *app.App, wsID, channel, threadID string, isGroup bool, contactID *string, customerName, customerPhone string) (id string, created bool, err error) {
	err = a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO conversations (workspace_id, channel, external_thread_id, is_group, contact_id, customer_name, customer_phone, subject)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $6)
		ON CONFLICT (workspace_id, channel, external_thread_id) WHERE external_thread_id <> '' DO UPDATE SET
		  contact_id = coalesce(conversations.contact_id, EXCLUDED.contact_id),
		  updated_at = conversations.updated_at
		RETURNING id, (xmax = 0)`,
		wsID, channel, threadID, isGroup, contactID, customerName, customerPhone,
	).Scan(&id, &created)
	return id, created, err
}

func (p WhatsAppProvider) importDirect(ctx context.Context, a *app.App, wsID string, job *Job, cl *whatsapp.Client, c whatsapp.Chat, depth int) error {
	phone := "+" + c.Phone()
	if c.Phone() == "" {
		return errors.New("no phone on this chat")
	}
	name := c.DisplayName()
	locale := localeOf(name)
	contactID, created, err := marketing.UpsertContactIdentity(ctx, a.SQLDB, wsID, "", phone, "", name, locale, "whatsapp_import")
	if err != nil {
		return err
	}
	if created {
		job.ContactsCreated++
	} else {
		job.ContactsReused++
	}

	convID, convCreated, err := upsertConversation(ctx, a, wsID, KindWhatsApp, c.RemoteJID, false, &contactID, name, phone)
	if err != nil {
		return err
	}
	if convCreated {
		job.ConversationsCreated++
	} else {
		job.ConversationsReused++
	}

	msgs, err := cl.FindMessages(ctx, c.RemoteJID, depth)
	if err != nil {
		return err
	}
	n, err := insertHistory(ctx, a, wsID, convID, msgs)
	if err != nil {
		return err
	}
	job.MessagesImported += n
	return nil
}

func (p WhatsAppProvider) importGroup(ctx context.Context, a *app.App, wsID string, job *Job, cl *whatsapp.Client, g whatsapp.Group, depth int, importParticipants bool) error {
	locale := localeOf(g.Subject)
	convID, convCreated, err := upsertConversation(ctx, a, wsID, KindWhatsApp, g.ID, true, nil, g.Subject, "")
	if err != nil {
		return err
	}
	if convCreated {
		job.ConversationsCreated++
	} else {
		job.ConversationsReused++
	}

	for _, part := range g.Participants {
		phone := part.Phone()
		if phone == "" {
			continue
		}
		identity := "+" + phone
		var contactID *string
		if importParticipants {
			id, created, err := marketing.UpsertContactIdentity(ctx, a.SQLDB, wsID, "", identity, "", part.Name, locale, "whatsapp_import")
			if err != nil {
				a.Log.Warn("whatsapp import: participant contact failed", "workspace", wsID, "err", err)
			} else {
				contactID = &id
				if created {
					job.ContactsCreated++
				} else {
					job.ContactsReused++
				}
			}
		}
		if _, err := a.SQLDB.ExecContext(ctx, `
			INSERT INTO conversation_participants (workspace_id, conversation_id, contact_id, identity, name)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (workspace_id, conversation_id, identity) DO UPDATE SET
			  contact_id = coalesce(conversation_participants.contact_id, EXCLUDED.contact_id),
			  name = CASE WHEN conversation_participants.name = '' THEN EXCLUDED.name ELSE conversation_participants.name END`,
			wsID, convID, contactID, identity, part.Name); err != nil {
			return err
		}
	}

	msgs, err := cl.FindMessages(ctx, g.ID, depth)
	if err != nil {
		return err
	}
	n, err := insertHistory(ctx, a, wsID, convID, msgs)
	if err != nil {
		return err
	}
	job.MessagesImported += n
	return nil
}

// insertHistory files a batch of past messages, dedup'd on external_id
// (messages_external_uq) so a re-import adds nothing already there.
func insertHistory(ctx context.Context, a *app.App, wsID, convID string, msgs []whatsapp.HistoryMessage) (int, error) {
	n := 0
	var lastAt time.Time
	for _, m := range msgs {
		text := m.Text()
		if text == "" || m.Key.ID == "" {
			continue
		}
		direction := "in"
		if m.Key.FromMe {
			direction = "out"
		}
		at := time.Unix(int64(m.Timestamp), 0).UTC()
		res, err := a.SQLDB.ExecContext(ctx, `
			INSERT INTO messages (workspace_id, conversation_id, direction, body, external_id, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (workspace_id, external_id) WHERE external_id <> '' DO NOTHING`,
			wsID, convID, direction, text, m.Key.ID, at)
		if err != nil {
			return n, err
		}
		if rows, _ := res.RowsAffected(); rows > 0 {
			n++
		}
		if at.After(lastAt) {
			lastAt = at
		}
	}
	if !lastAt.IsZero() {
		_, err := a.SQLDB.ExecContext(ctx, `
			UPDATE conversations SET last_message_at = GREATEST(last_message_at, $3), updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, wsID, convID, lastAt)
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func (p WhatsAppProvider) Summary(ctx context.Context, a *app.App, wsID, conversationID string) (*Summary, error) {
	return genericSummary(ctx, a, wsID, conversationID, KindWhatsApp)
}

var _ Provider = WhatsAppProvider{}
