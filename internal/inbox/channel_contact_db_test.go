package inbox

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/fadymondy/moharrik/internal/contacts"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
MH-583: Telegram, Slack and Discord inbound senders must resolve or create a
CRM contact through contacts.Upsert, exactly as email and WhatsApp already do
(see crm_link.go's autoLink). Against a real Postgres database with RLS: a
second message from the same platform id lands on the SAME contact as the
first, and the contact_identities row FileChannelMessage wrote on the first
message is reused rather than duplicated.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/inbox/ -run ChannelContact -v
*/

func TestFileChannelMessageResolvesSameContactAcrossMessages(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)

	chatID := "chat-" + idSuffix()

	if err := FileChannelMessage(f.ctx, a, f.wsID, "telegram", "", chatID, "@fady", "Fady", "First message", "tg:"+idSuffix()); err != nil {
		t.Fatal(err)
	}

	var convID, contactID string
	var contactLink string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT id, coalesce(contact_id, ''), contact_link FROM conversations
		WHERE workspace_id = $1 AND channel = 'telegram' AND external_thread_id = $2`,
		f.wsID, chatID).Scan(&convID, &contactID, &contactLink); err != nil {
		t.Fatal(err)
	}
	if contactID == "" {
		t.Fatal("first Telegram message did not link a contact")
	}
	if contactLink != "auto" {
		t.Fatalf("contact_link = %q, want 'auto'", contactLink)
	}

	var identityCount int
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT count(*) FROM contact_identities WHERE workspace_id = $1 AND channel = 'telegram' AND external_id = $2`,
		f.wsID, chatID).Scan(&identityCount); err != nil {
		t.Fatal(err)
	}
	if identityCount != 1 {
		t.Fatalf("contact_identities rows for %q = %d, want 1", chatID, identityCount)
	}

	// A SECOND message from the same chat id: it must resolve to the SAME
	// contact (via the identity the first message wrote), not create a
	// second contact or a second identity row.
	if err := FileChannelMessage(f.ctx, a, f.wsID, "telegram", "", chatID, "@fady", "Fady", "Second message", "tg:"+idSuffix()); err != nil {
		t.Fatal(err)
	}

	var convID2, contactID2 string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT id, coalesce(contact_id, '') FROM conversations
		WHERE workspace_id = $1 AND channel = 'telegram' AND external_thread_id = $2`,
		f.wsID, chatID).Scan(&convID2, &contactID2); err != nil {
		t.Fatal(err)
	}
	if convID2 != convID {
		t.Fatalf("second message threaded into a different conversation: %q vs %q", convID2, convID)
	}
	if contactID2 != contactID {
		t.Fatalf("second message resolved to a different contact: %q vs %q, want the same contact reused", contactID2, contactID)
	}

	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT count(*) FROM contact_identities WHERE workspace_id = $1 AND channel = 'telegram' AND external_id = $2`,
		f.wsID, chatID).Scan(&identityCount); err != nil {
		t.Fatal(err)
	}
	if identityCount != 1 {
		t.Fatalf("contact_identities rows after a second message = %d, want 1 (the existing identity must be reused, not duplicated)", identityCount)
	}

	var messageCount int
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT count(*) FROM messages WHERE workspace_id = $1 AND conversation_id = $2`, f.wsID, convID).Scan(&messageCount); err != nil {
		t.Fatal(err)
	}
	if messageCount != 2 {
		t.Fatalf("messages in the thread = %d, want 2", messageCount)
	}
}

// TestFileChannelMessageReusesExistingIdentity covers the other half of the
// card's requirement directly: a contact that ALREADY carries a Slack
// identity (linked by hand, e.g. via the "link to existing contact" inbox
// action or the MCP link tool) must be the contact a later inbound Slack
// message from that same external id resolves to — never a duplicate.
func TestFileChannelMessageReusesExistingIdentity(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)

	externalID := "U" + idSuffix()

	// A placeholder handle satisfies the contacts_identity_guard trigger for
	// this standalone INSERT (a real caller would give it an email/phone
	// too); the point under test is the Slack identity linked next.
	var existingContactID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO contacts (workspace_id, email, handle, name, source) VALUES ($1, '', $2, 'Pre-linked Person', 'manual') RETURNING id`,
		f.wsID, "seed-"+idSuffix()).Scan(&existingContactID); err != nil {
		t.Fatal(err)
	}
	if _, err := contacts.LinkIdentity(f.ctx, a.SQLDB, f.wsID, existingContactID, "slack", externalID, "", true); err != nil {
		t.Fatal(err)
	}

	if err := FileChannelMessage(f.ctx, a, f.wsID, "slack", "", externalID, "fady", "Fady", "Hello from Slack", "sl:"+idSuffix()); err != nil {
		t.Fatal(err)
	}

	var contactID, contactLink string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT coalesce(contact_id, ''), contact_link FROM conversations
		WHERE workspace_id = $1 AND channel = 'slack' AND external_thread_id = $2`,
		f.wsID, externalID).Scan(&contactID, &contactLink); err != nil {
		t.Fatal(err)
	}
	if contactID != existingContactID {
		t.Fatalf("conversation resolved to contact %q, want the pre-linked contact %q", contactID, existingContactID)
	}
	if contactLink != "auto" {
		t.Fatalf("contact_link = %q, want 'auto'", contactLink)
	}

	var identityCount int
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT count(*) FROM contact_identities WHERE workspace_id = $1 AND channel = 'slack' AND external_id = $2`,
		f.wsID, externalID).Scan(&identityCount); err != nil {
		t.Fatal(err)
	}
	if identityCount != 1 {
		t.Fatalf("contact_identities rows for %q = %d, want 1 (the pre-existing identity must be reused, not duplicated)", externalID, identityCount)
	}
}

// TestUpdateConversationLinkAttachesSenderIdentity covers item 2 of MH-583:
// the inbox "link to existing contact" action (PATCH /conversations/{id}
// {contact_id}) must not only point the conversation at the contact, it must
// also attach the conversation's own sender identity (here, a Telegram chat
// id) to that contact via contacts.LinkIdentity — the same identity a LATER
// Telegram message from that chat id needs already present to resolve to the
// same contact through FileChannelMessage/contacts.Upsert.
func TestUpdateConversationLinkAttachesSenderIdentity(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)
	sctx := tenant.With(f.ctx, &tenant.Scope{
		WorkspaceID: f.wsID, UserID: f.userID, Email: "owner@example.test", Role: "owner", Via: "session",
	})

	chatID := "chat-" + idSuffix()
	var convID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, external_thread_id, customer_name, customer_handle, subject, last_message_at, unread)
		VALUES ($1, 'telegram', $2, 'Fady', '@fady', 'Hi', now(), true) RETURNING id`,
		f.wsID, chatID).Scan(&convID); err != nil {
		t.Fatal(err)
	}

	// A placeholder handle satisfies the contacts_identity_guard trigger for
	// this standalone INSERT; UpdateConversation's identity attach is what's
	// under test next.
	var contactID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO contacts (workspace_id, email, handle, name, source) VALUES ($1, '', $2, 'Existing Person', 'manual') RETURNING id`,
		f.wsID, "seed-"+idSuffix()).Scan(&contactID); err != nil {
		t.Fatal(err)
	}

	if _, err := UpdateConversation(sctx, a, convID, ConversationPatch{ContactID: &contactID}); err != nil {
		t.Fatal(err)
	}

	var linkedContactID, contactLink string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT coalesce(contact_id, ''), contact_link FROM conversations WHERE workspace_id = $1 AND id = $2`,
		f.wsID, convID).Scan(&linkedContactID, &contactLink); err != nil {
		t.Fatal(err)
	}
	if linkedContactID != contactID {
		t.Fatalf("conversation.contact_id = %q, want %q", linkedContactID, contactID)
	}
	if contactLink != "manual" {
		t.Fatalf("contact_link = %q, want 'manual' (a member's own choice)", contactLink)
	}

	var identChannel, identExternalID, identLabel string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT channel, external_id, label FROM contact_identities WHERE workspace_id = $1 AND contact_id = $2`,
		f.wsID, contactID).Scan(&identChannel, &identExternalID, &identLabel); err != nil {
		t.Fatalf("no identity was attached to the linked contact: %v", err)
	}
	if identChannel != "telegram" {
		t.Errorf("identity channel = %q, want telegram", identChannel)
	}
	if identExternalID != chatID {
		t.Errorf("identity external_id = %q, want the conversation's chat id %q", identExternalID, chatID)
	}
	if identLabel != "@fady" {
		t.Errorf("identity label = %q, want the customer_handle @fady", identLabel)
	}

	// A second message from the SAME chat id must now resolve to this same,
	// already-linked contact — the point of attaching the identity at all.
	if err := FileChannelMessage(f.ctx, a, f.wsID, "telegram", "", chatID, "@fady", "Fady", "Follow-up", "tg:"+idSuffix()); err != nil {
		t.Fatal(err)
	}
	var contactIDAfter string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT coalesce(contact_id, '') FROM conversations WHERE workspace_id = $1 AND id = $2`,
		f.wsID, convID).Scan(&contactIDAfter); err != nil {
		t.Fatal(err)
	}
	if contactIDAfter != contactID {
		t.Fatalf("after a second message, conversation.contact_id = %q, want it to stay %q (the manual link must not be overridden)", contactIDAfter, contactID)
	}
}

// The "a contact needs some identity" rule is a deferred trigger now, not a
// CHECK — but it must still fail as the check_violation named
// contacts_identity_chk, which crm.linkErr turns into a 422 naming the rule
// rather than a 500.
func TestContactWithNoIdentityIsACheckViolation(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)

	_, err := a.SQLDB.ExecContext(f.ctx, `INSERT INTO contacts (workspace_id, name) VALUES ($1, 'Nobody')`, f.wsID)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		t.Fatalf("a contact with no identity was stored (err %v)", err)
	}
	if pg.Code != "23514" || pg.ConstraintName != "contacts_identity_chk" {
		t.Fatalf("got %s on %q, want 23514 on contacts_identity_chk", pg.Code, pg.ConstraintName)
	}
}
