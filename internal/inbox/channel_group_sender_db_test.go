package inbox

/*
MH-604: a Telegram/Slack/Discord GROUP thread (never WhatsApp — WhatsApp
groups are deliberately excluded from the inbox, see internal/whatsapp/hook.go)
shares ONE conversation across many senders, unlike a 1:1 DM where the
conversation's own contact_id already says who it's with. Before this card
nothing in the data model recorded who wrote which message in a group thread.

FileChannelMessage(..., isGroup=true) must:
  - mark the conversation is_group=true, and never link its contact_id (a
    group thread has no single "the contact" the way a DM does);
  - resolve each message's OWN sender via contacts.Upsert, keyed on the
    sender's platform handle rather than the conversation's thread id, and
    persist it on the message row (sender_contact_id/sender_name/
    sender_external_id);
  - reuse the SAME sender contact across that sender's later messages in the
    thread, exactly as linkChannelContact already does for a 1:1 DM;
  - queue a per-sender (not per-conversation) profile-photo fetch.

Against a real Postgres database with RLS:

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/inbox/ -run GroupSender -v
*/

import (
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/channels"
)

func TestFileChannelMessageResolvesPerSenderInGroupThread(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)

	groupID := "grp-" + idSuffix()

	if err := FileChannelMessage(f.ctx, a, f.wsID, "slack", "", groupID, "U-DANA", "Dana", "hi everyone", "sl:"+idSuffix(), true); err != nil {
		t.Fatal(err)
	}
	if err := FileChannelMessage(f.ctx, a, f.wsID, "slack", "", groupID, "U-SAMI", "Sami", "hey", "sl:"+idSuffix(), true); err != nil {
		t.Fatal(err)
	}

	var convID string
	var isGroup bool
	var convContactID *string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT id, is_group, contact_id FROM conversations
		WHERE workspace_id = $1 AND channel = 'slack' AND external_thread_id = $2`,
		f.wsID, groupID).Scan(&convID, &isGroup, &convContactID); err != nil {
		t.Fatal(err)
	}
	if !isGroup {
		t.Fatal("conversation.is_group = false, want true")
	}
	if convContactID != nil {
		t.Fatalf("conversation.contact_id = %q, want NULL — a group thread has no single conversation-level contact", *convContactID)
	}

	rows, err := a.SQLDB.QueryContext(f.ctx, `
		SELECT body, coalesce(sender_name, ''), coalesce(sender_external_id, ''), sender_contact_id
		FROM messages WHERE workspace_id = $1 AND conversation_id = $2 ORDER BY created_at`, f.wsID, convID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct {
		body, name, externalID string
		contactID              *string
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.body, &r.name, &r.externalID, &r.contactID); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(got), got)
	}
	if got[0].name != "Dana" || got[0].externalID != "U-DANA" || got[0].contactID == nil || *got[0].contactID == "" {
		t.Fatalf("message 1 sender = %+v, want Dana/U-DANA with a resolved contact", got[0])
	}
	if got[1].name != "Sami" || got[1].externalID != "U-SAMI" || got[1].contactID == nil || *got[1].contactID == "" {
		t.Fatalf("message 2 sender = %+v, want Sami/U-SAMI with a resolved contact", got[1])
	}
	if *got[0].contactID == *got[1].contactID {
		t.Fatalf("both messages resolved to the SAME contact %q, want two different senders to resolve to two different contacts", *got[0].contactID)
	}
}

func TestFileChannelMessageGroupSenderReusesSameContactAcrossMessages(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)

	groupID := "grp-" + idSuffix()
	senderID := "U-" + idSuffix()

	if err := FileChannelMessage(f.ctx, a, f.wsID, "discord", "", groupID, senderID, "Mona", "first", "dc:"+idSuffix(), true); err != nil {
		t.Fatal(err)
	}
	if err := FileChannelMessage(f.ctx, a, f.wsID, "discord", "", groupID, senderID, "Mona", "second", "dc:"+idSuffix(), true); err != nil {
		t.Fatal(err)
	}

	rows, err := a.SQLDB.QueryContext(f.ctx, `
		SELECT sender_contact_id FROM messages m
		JOIN conversations c ON c.workspace_id = m.workspace_id AND c.id = m.conversation_id
		WHERE m.workspace_id = $1 AND c.channel = 'discord' AND c.external_thread_id = $2 ORDER BY m.created_at`,
		f.wsID, groupID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id *string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		if id == nil || *id == "" {
			t.Fatal("a message in the group thread resolved no sender contact")
		}
		ids = append(ids, *id)
	}
	if len(ids) != 2 {
		t.Fatalf("got %d messages, want 2", len(ids))
	}
	if ids[0] != ids[1] {
		t.Fatalf("the same sender's two messages resolved to different contacts: %q vs %q", ids[0], ids[1])
	}

	var identityCount int
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT count(*) FROM contact_identities WHERE workspace_id = $1 AND channel = 'discord' AND external_id = $2`,
		f.wsID, senderID).Scan(&identityCount); err != nil {
		t.Fatal(err)
	}
	if identityCount != 1 {
		t.Fatalf("contact_identities rows for the sender = %d, want 1 (reused, not duplicated)", identityCount)
	}
}

// TestFileChannelMessageOneToOneThreadLeavesSenderColumnsEmpty proves the
// other half: a 1:1 DM (isGroup=false) must NOT populate the new per-message
// sender columns — that identity already lives on the conversation itself,
// exactly as it did before this card.
func TestFileChannelMessageOneToOneThreadLeavesSenderColumnsEmpty(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)

	chatID := "dm-" + idSuffix()
	if err := FileChannelMessage(f.ctx, a, f.wsID, "telegram", "", chatID, "@fady", "Fady", "hello", "tg:"+idSuffix(), false); err != nil {
		t.Fatal(err)
	}

	var isGroup bool
	var senderContactID, senderExternalID *string
	var senderName string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT c.is_group, m.sender_contact_id, m.sender_external_id, coalesce(m.sender_name, '')
		FROM messages m JOIN conversations c ON c.workspace_id = m.workspace_id AND c.id = m.conversation_id
		WHERE m.workspace_id = $1 AND c.channel = 'telegram' AND c.external_thread_id = $2`,
		f.wsID, chatID).Scan(&isGroup, &senderContactID, &senderExternalID, &senderName); err != nil {
		t.Fatal(err)
	}
	if isGroup {
		t.Fatal("a 1:1 delivery must not mark the conversation is_group")
	}
	if senderContactID != nil || (senderExternalID != nil && *senderExternalID != "") || senderName != "" {
		t.Fatalf("a 1:1 message must leave the sender columns empty, got contact=%v external_id=%v name=%q", senderContactID, senderExternalID, senderName)
	}
}

// TestFileChannelMessageQueuesPerSenderPhotoInGroupThread proves the photo
// refresh MH-604 wires for a group thread targets the resolved SENDER's own
// contact, not the conversation (which has none) — against a fake Telegram
// server, never a real one.
func TestFileChannelMessageQueuesPerSenderPhotoInGroupThread(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)

	fake := fakeTelegramForFiling(t)
	defer channels.SetTelegramAPIBase(fake.URL)()

	created, err := channels.CreateTelegram(f.ctx, a, f.wsID, f.userID, channels.TelegramInput{Name: "Bot", BotToken: "T:1"})
	if err != nil {
		t.Fatal(err)
	}

	groupID := "grp-" + numericIDSuffix()
	senderID := "800" + numericIDSuffix()
	if err := FileChannelMessage(f.ctx, a, f.wsID, "telegram", created.ID, groupID, senderID, "Dana", "hello group", "tg:"+idSuffix(), true); err != nil {
		t.Fatal(err)
	}

	var contactID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT sender_contact_id FROM messages m
		JOIN conversations c ON c.workspace_id = m.workspace_id AND c.id = m.conversation_id
		WHERE m.workspace_id = $1 AND c.channel = 'telegram' AND c.external_thread_id = $2`,
		f.wsID, groupID).Scan(&contactID); err != nil {
		t.Fatal(err)
	}
	if contactID == "" {
		t.Fatal("the group message did not resolve a sender contact")
	}

	waitUntil(t, 5*time.Second, func() (bool, error) {
		var fetched bool
		err := a.SQLDB.QueryRowContext(f.ctx, `SELECT photo_fetched_at IS NOT NULL FROM contacts WHERE workspace_id = $1 AND id = $2`,
			f.wsID, contactID).Scan(&fetched)
		return fetched, err
	})

	var url, source string
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT coalesce(photo_url,''), coalesce(photo_source,'') FROM contacts WHERE workspace_id = $1 AND id = $2`,
		f.wsID, contactID).Scan(&url, &source); err != nil {
		t.Fatal(err)
	}
	if source != "telegram" || !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("sender contact photo_source=%q photo_url=%q, want a cached telegram photo on the SENDER, not the conversation", source, url)
	}
}

// TestDeletingGroupSenderContactKeepsTheMessage: messages_sender_contact_fk is
// a composite (workspace_id, sender_contact_id) key, so its SET NULL must name
// sender_contact_id alone — a bare SET NULL would null workspace_id too and the
// contact delete would fail on its NOT NULL.
func TestDeletingGroupSenderContactKeepsTheMessage(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)

	groupID := "grp-" + idSuffix()
	if err := FileChannelMessage(f.ctx, a, f.wsID, "discord", "", groupID, "U-"+idSuffix(), "Nour", "hello", "dc:"+idSuffix(), true); err != nil {
		t.Fatal(err)
	}
	var msgID, contactID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT m.id, m.sender_contact_id FROM messages m
		JOIN conversations c ON c.workspace_id = m.workspace_id AND c.id = m.conversation_id
		WHERE m.workspace_id = $1 AND c.external_thread_id = $2`, f.wsID, groupID).Scan(&msgID, &contactID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SQLDB.ExecContext(f.ctx, `DELETE FROM contacts WHERE workspace_id = $1 AND id = $2`, f.wsID, contactID); err != nil {
		t.Fatalf("deleting the sender contact failed: %v", err)
	}
	var sender *string
	var name string
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT sender_contact_id, sender_name FROM messages WHERE workspace_id = $1 AND id = $2`,
		f.wsID, msgID).Scan(&sender, &name); err != nil {
		t.Fatalf("the message went with the contact: %v", err)
	}
	if sender != nil || name != "Nour" {
		t.Fatalf("after delete: sender_contact_id=%v sender_name=%q, want nil and the name kept", sender, name)
	}
}
