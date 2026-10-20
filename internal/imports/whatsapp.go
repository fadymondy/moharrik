package imports

import (
	"context"
	"database/sql"
	"errors"
	"sort"
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

// connectionID is the connection an import names when it does not pick a
// number: the workspace's default number. Each linked number is also its own
// connection, named by its link id.
const connectionID = "default"

func (p WhatsAppProvider) Connections(ctx context.Context, a *app.App, wsID string) ([]ImportConnection, error) {
	links, err := whatsapp.List(ctx, a, wsID)
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return []ImportConnection{{ID: connectionID, Label: "", Ready: false, Note: "no WhatsApp number is linked yet"}}, nil
	}
	out := make([]ImportConnection, 0, len(links))
	for i, link := range links {
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
		label := link.Phone
		if link.Label != "" && link.Phone != "" {
			label = link.Label + " (" + link.Phone + ")"
		} else if link.Label != "" {
			label = link.Label
		}
		id := link.ID
		if i == 0 {
			// The default number keeps the id imports have always used.
			id = connectionID
		}
		out = append(out, ImportConnection{ID: id, Label: label, Ready: ready, Note: note})
	}
	return out, nil
}

// client resolves the Evolution client of the number connID names (the
// default number for "default" or ""), or an error naming why there isn't
// one — the same message a preview/import should surface. The returned ctx
// carries that number, so whatever the import files lands on its threads.
func (p WhatsAppProvider) client(ctx context.Context, a *app.App, wsID, connID string) (context.Context, *whatsapp.Client, error) {
	if connID != "" && connID != connectionID {
		ctx = whatsapp.WithLink(ctx, connID)
	}
	link, err := whatsapp.Load(ctx, a, wsID)
	if errors.Is(err, whatsapp.ErrNoLink) {
		return ctx, nil, errors.New("no WhatsApp number is linked for this workspace")
	}
	if err != nil {
		return ctx, nil, err
	}
	if link.State != "open" {
		return ctx, nil, errors.New("WhatsApp is not connected (state: " + link.State + ")")
	}
	return whatsapp.WithLink(ctx, link.ID), whatsapp.ClientFor(p.cfg(), link), nil
}

func (p WhatsAppProvider) Preview(ctx context.Context, a *app.App, wsID, connID string, f Filters) ([]Item, error) {
	_, cl, err := p.client(ctx, a, wsID, connID)
	if err != nil {
		return nil, err
	}
	var items []Item
	if f.Kind == "" || f.Kind == "direct" {
		chats, err := cl.ListChats(ctx)
		if err != nil {
			return nil, err
		}
		for _, pc := range byPhone(chats) {
			it := Item{ID: pc.id(), Kind: "direct", Name: pc.name, Identity: "+" + pc.phone}
			if pc.last != nil {
				it.LastMessage = pc.last.Text()
				t := time.Unix(int64(pc.last.Timestamp), 0).UTC()
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
	ctx, cl, err := p.client(ctx, a, wsID, req.ConnectionID)
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
	for _, pc := range byPhone(chats) {
		// Accept the chat's own JID too: an item id from before direct chats
		// were keyed by phone still names the same person.
		picked := want[pc.id()]
		for _, c := range pc.chats {
			picked = picked || want[c.RemoteJID]
		}
		if !picked {
			continue
		}
		if err := p.importDirect(ctx, a, wsID, job, cl, pc, depth); err != nil {
			a.Log.Warn("whatsapp import: direct chat failed", "workspace", wsID, "phone_tail", tail(pc.phone), "err", err)
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

// phoneChat is one person's 1:1 chats. WhatsApp can hold the same person
// under their phone JID and a privacy "…@lid" id at once; both are one thread
// to the inbox, which files live messages by phone (inbox.FileWhatsApp).
type phoneChat struct {
	phone string // digits only, as live filing stores customer_phone
	name  string
	last  *whatsapp.ChatMessagePreview
	chats []whatsapp.Chat
}

// id is the item id: the person's phone JID, whichever JIDs they have.
func (pc phoneChat) id() string { return pc.phone + "@s.whatsapp.net" }

// byPhone groups chats by the phone behind them, newest first, and drops chats
// with no phone at all (a privacy id Evolution cannot resolve).
func byPhone(chats []whatsapp.Chat) []phoneChat {
	idx := map[string]int{}
	var out []phoneChat
	for _, c := range chats {
		phone := c.Phone()
		if phone == "" {
			continue
		}
		i, ok := idx[phone]
		if !ok {
			i = len(out)
			idx[phone] = i
			out = append(out, phoneChat{phone: phone})
		}
		pc := &out[i]
		pc.chats = append(pc.chats, c)
		if pc.name == "" {
			pc.name = c.DisplayName()
		}
		if c.LastMessage != nil && (pc.last == nil || c.LastMessage.Timestamp > pc.last.Timestamp) {
			pc.last = c.LastMessage
			if n := c.DisplayName(); n != "" {
				pc.name = n
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		var a, b whatsapp.UnixTime
		if out[i].last != nil {
			a = out[i].last.Timestamp
		}
		if out[j].last != nil {
			b = out[j].last.Timestamp
		}
		return a > b
	})
	return out
}

// tail is enough of a phone number to tell rows apart in a log.
func tail(phone string) string {
	if len(phone) <= 4 {
		return phone
	}
	return "…" + phone[len(phone)-4:]
}

// directConversation is the one thread a person's chats import into: the
// thread live messages already file into when there is one (matched on the
// phone's digits, as FileWhatsApp matches), else a new one keyed by their
// phone JID and stored the way FileWhatsApp would store it.
func directConversation(ctx context.Context, a *app.App, wsID string, pc phoneChat, contactID, name string) (id string, created bool, err error) {
	err = a.SQLDB.QueryRowContext(ctx, `
		SELECT id FROM conversations
		WHERE workspace_id = $1 AND channel = 'whatsapp' AND NOT is_group
		  AND (external_thread_id = $2 OR regexp_replace(customer_phone, '[^0-9]', '', 'g') = $3)
		ORDER BY (external_thread_id = $2) DESC, created_at
		LIMIT 1`, wsID, pc.id(), pc.phone).Scan(&id)
	if err == nil {
		// Claim the live thread for this import, so the preview says it is
		// imported — unless another thread already carries the key.
		_, err = a.SQLDB.ExecContext(ctx, `
			UPDATE conversations SET
			  external_thread_id = CASE WHEN external_thread_id = '' AND NOT EXISTS (
			      SELECT 1 FROM conversations o WHERE o.workspace_id = $1 AND o.channel = 'whatsapp' AND o.external_thread_id = $3)
			    THEN $3 ELSE external_thread_id END,
			  contact_id = coalesce(contact_id, $4)
			WHERE workspace_id = $1 AND id = $2`, wsID, id, pc.id(), contactID)
		return id, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	return upsertConversation(ctx, a, wsID, KindWhatsApp, pc.id(), false, &contactID, name, pc.phone)
}

func (p WhatsAppProvider) importDirect(ctx context.Context, a *app.App, wsID string, job *Job, cl *whatsapp.Client, pc phoneChat, depth int) error {
	name := pc.name
	locale := localeOf(name)
	contactID, created, err := marketing.UpsertContactIdentity(ctx, a.SQLDB, wsID, "", "+"+pc.phone, "", name, locale, "whatsapp_import")
	if err != nil {
		return err
	}
	if created {
		job.ContactsCreated++
	} else {
		job.ContactsReused++
	}

	// Every JID the person has, merged newest first; the same WhatsApp message
	// id never lands twice (insertHistory is idempotent on it).
	var msgs []whatsapp.HistoryMessage
	for _, c := range pc.chats {
		m, err := cl.FindMessages(ctx, c.RemoteJID, depth)
		if err != nil {
			return err
		}
		msgs = append(msgs, m...)
	}
	// A chat the phone lists with no history is a contact, not a thread
	// (MH-599): filing one made an empty conversation that read as deleted.
	if len(msgs) == 0 {
		job.Skipped++
		return nil
	}
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].Timestamp > msgs[j].Timestamp })
	if len(msgs) > depth {
		msgs = msgs[:depth]
	}

	convID, convCreated, err := directConversation(ctx, a, wsID, pc, contactID, name)
	if err != nil {
		return err
	}
	if convCreated {
		job.ConversationsCreated++
	} else {
		job.ConversationsReused++
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
	msgs, err := cl.FindMessages(ctx, g.ID, depth)
	if err != nil {
		return err
	}
	// No history, no thread (MH-599) — the same rule as a direct chat.
	if len(msgs) == 0 {
		job.Skipped++
		return nil
	}
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
		if err := syncConversationTimes(ctx, a, wsID, convID); err != nil {
			return n, err
		}
	}
	return n, nil
}

func (p WhatsAppProvider) Summary(ctx context.Context, a *app.App, wsID, conversationID string) (*Summary, error) {
	return genericSummary(ctx, a, wsID, conversationID, KindWhatsApp)
}

var _ Provider = WhatsAppProvider{}
