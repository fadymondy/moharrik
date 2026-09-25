package contacts_test

import (
	"context"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/contacts"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/testdb"
	"github.com/togo-framework/togo"

	"database/sql"

	_ "github.com/fadymondy/moharrik/internal/plugins" // registers the pgx driver
)

/*
MH-583: internal/contacts is the one resolver every contact-creator goes
through (imports/whatsapp.go, the MCP create_contact, conversation→contact,
inquiry→contact), so imports from anywhere never conflict — they merge on
phone/email instead of colliding on a unique index.

These tests open the APP pool directly (no HTTP, no app.App) and drive
contacts.Upsert exactly like those callers do.
*/

// appDB opens the APP pool (RLS-bound — the pool internal/contacts is meant
// to run on) and, separately, the OWNER pool needed only to create the test
// workspace row (workspaces is RLS-protected too, and app.workspace_id is not
// set yet when the workspace itself is being created).
func appDB(t *testing.T) (appPool, ownerPool *sql.DB) {
	t.Helper()
	ownerURL, appURL := testdb.Setup(t, "contacts", "../..")
	k := togo.New()
	t.Cleanup(func() { k.Close() })

	ownerPool, err := sql.Open("pgx", ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ownerPool.Close() })

	appPool, err = sql.Open("pgx", appURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { appPool.Close() })
	return appPool, ownerPool
}

func workspaceIn(t *testing.T, owner *sql.DB) string {
	t.Helper()
	var wsID string
	slug := "contacts-test-" + time.Now().Format("150405.000000000")
	if err := owner.QueryRowContext(context.Background(), `
		INSERT INTO workspaces (name, slug, created_by) VALUES ('Contacts Test', $1, 'test') RETURNING id`,
		slug).Scan(&wsID); err != nil {
		t.Fatal(err)
	}
	return wsID
}

func withWS(ctx context.Context, db *sql.DB, wsID string) context.Context {
	// internal/contacts reads/writes on the app pool, which enforces RLS via
	// app.workspace_id. Set it for the session the same way internal/tenant does.
	_, _ = db.ExecContext(ctx, `SELECT set_config('app.workspace_id', $1, false)`, wsID)
	return ctx
}

// TestUpsertMergesTwoImportPaths: the same person arrives once by email
// (an inquiry-style import) and once by phone (a WhatsApp-style import), with
// no identity shared between the two calls up front. A later call carrying
// BOTH must resolve to the one contact, and neither call may ever fail on a
// unique-index conflict.
func TestUpsertMergesTwoImportPaths(t *testing.T) {
	db, owner := appDB(t)
	wsID := workspaceIn(t, owner)
	ctx := withWS(context.Background(), db, wsID)

	r1, err := contacts.Upsert(ctx, db, wsID, contacts.Candidate{
		Email: "sara.k@example.test", Name: "Sara K", Source: "inquiry",
	})
	if err != nil {
		t.Fatalf("first import (email only): %v", err)
	}
	if !r1.Created {
		t.Fatalf("first import should have created a contact")
	}

	r2, err := contacts.Upsert(ctx, db, wsID, contacts.Candidate{
		Phone: "+201012345678", Name: "Sara K", Source: "whatsapp_import",
	})
	if err != nil {
		t.Fatalf("second import (phone only): %v", err)
	}
	if r2.ContactID == r1.ContactID {
		t.Fatalf("a phone with no shared identity should not have matched the email-only contact by accident")
	}

	// A third path now carries BOTH the email and the phone — e.g. a CSV row,
	// or the WhatsApp contact fills in their email in chat. This ties the two
	// existing contacts together and must merge them into one, not error.
	r3, err := contacts.Upsert(ctx, db, wsID, contacts.Candidate{
		Email: "sara.k@example.test", Phone: "+201012345678", Name: "Sara K", Source: "csv_import",
	})
	if err != nil {
		t.Fatalf("third import (email + phone, ties the two together) must not fail on a unique-index conflict: %v", err)
	}
	if len(r3.Merged) == 0 {
		t.Errorf("expected the two prior contacts to be reported as merged, got none")
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM contacts WHERE workspace_id = $1`, wsID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one contact after the merge, found %d", count)
	}

	// The survivor must carry both identities now.
	ids, err := contacts.ListIdentities(ctx, db, wsID, r3.ContactID)
	if err != nil {
		t.Fatal(err)
	}
	var hasEmail, hasPhone bool
	for _, id := range ids {
		if id.Channel == contacts.ChannelEmail && id.ExternalID == "sara.k@example.test" {
			hasEmail = true
		}
		if id.Channel == contacts.ChannelPhone && id.ExternalID == "+201012345678" {
			hasPhone = true
		}
	}
	if !hasEmail || !hasPhone {
		t.Errorf("survivor should carry both identities after the merge: %+v", ids)
	}
}

// TestUpsertNeverOverwritesANonEmptyField: importing a second time with a
// different name for the same email must not clobber the name already on
// file — only blanks are filled.
func TestUpsertNeverOverwritesANonEmptyField(t *testing.T) {
	db, owner := appDB(t)
	wsID := workspaceIn(t, owner)
	ctx := withWS(context.Background(), db, wsID)

	r1, err := contacts.Upsert(ctx, db, wsID, contacts.Candidate{Email: "omar@example.test", Name: "Omar Adel", Source: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := contacts.Upsert(ctx, db, wsID, contacts.Candidate{Email: "omar@example.test", Name: "Some Reimport Typo", Source: "csv_import"})
	if err != nil {
		t.Fatal(err)
	}
	if r2.ContactID != r1.ContactID || r2.Created {
		t.Fatalf("the second import should have matched and reused the first contact, got %+v", r2)
	}
	var name string
	if err := db.QueryRowContext(ctx, `SELECT name FROM contacts WHERE workspace_id = $1 AND id = $2`, wsID, r1.ContactID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Omar Adel" {
		t.Errorf("name should not have been overwritten by the reimport: got %q", name)
	}
}

// TestLinkIdentityAddsAChannel covers the "link an account on every
// platform" half of the card: linking a Telegram handle by hand, then
// resolving through it like any other identity.
func TestLinkIdentityAddsAChannel(t *testing.T) {
	db, owner := appDB(t)
	wsID := workspaceIn(t, owner)
	ctx := withWS(context.Background(), db, wsID)

	r1, err := contacts.Upsert(ctx, db, wsID, contacts.Candidate{Email: "nour@example.test", Name: "Nour", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contacts.LinkIdentity(ctx, db, wsID, r1.ContactID, contacts.ChannelHandle, "@nour_tg", "telegram", false); err != nil {
		t.Fatalf("linking a Telegram handle: %v", err)
	}
	// Linking the same external id to a second contact must be refused.
	r2, err := contacts.Upsert(ctx, db, wsID, contacts.Candidate{Email: "other@example.test", Name: "Other", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contacts.LinkIdentity(ctx, db, wsID, r2.ContactID, contacts.ChannelHandle, "@nour_tg", "telegram", false); err == nil {
		t.Errorf("linking an identity already on another contact should have been refused")
	}
}

// TestMergeContactsMovesEverything is MH-647's DB coverage: every child row a
// contact can own — a linked identity, a conversation, a group message's
// sender identity, a context note, a lifecycle event and an uploaded
// avatar — must land on the survivor, and the survivor's tags must gain the
// loser's.
func TestMergeContactsMovesEverything(t *testing.T) {
	db, owner := appDB(t)
	wsID := workspaceIn(t, owner)
	ctx := withWS(context.Background(), db, wsID)

	dst, err := contacts.Upsert(ctx, db, wsID, contacts.Candidate{Email: "dst@example.test", Name: "Destination", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	src, err := contacts.Upsert(ctx, db, wsID, contacts.Candidate{Phone: "+201099999999", Name: "Source", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if src.ContactID == dst.ContactID {
		t.Fatalf("email and phone should not have matched the same contact up front")
	}

	// A Telegram handle only on src.
	if _, err := contacts.LinkIdentity(ctx, db, wsID, src.ContactID, contacts.ChannelHandle, "@src_tg", "telegram", false); err != nil {
		t.Fatalf("linking a Telegram handle to src: %v", err)
	}

	// Tags on src that dst does not have.
	if _, err := db.ExecContext(ctx, `UPDATE contacts SET tags = ARRAY['vip','beta'] WHERE workspace_id = $1 AND id = $2`, wsID, src.ContactID); err != nil {
		t.Fatal(err)
	}

	// A conversation and a group message whose sender is src.
	var convID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO conversations (workspace_id, channel, contact_id, is_group) VALUES ($1, 'chat', $2, true) RETURNING id`,
		wsID, src.ContactID).Scan(&convID); err != nil {
		t.Fatal(err)
	}
	var msgID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO messages (workspace_id, conversation_id, direction, body, sender_contact_id, sender_name)
		VALUES ($1, $2, 'in', 'hello from the group', $3, 'Source') RETURNING id`,
		wsID, convID, src.ContactID).Scan(&msgID); err != nil {
		t.Fatal(err)
	}

	// A context note on src.
	var noteID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO contact_context_notes (workspace_id, contact_id, content) VALUES ($1, $2, 'met at a conference') RETURNING id`,
		wsID, src.ContactID).Scan(&noteID); err != nil {
		t.Fatal(err)
	}

	// A lifecycle event on src.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO contact_lifecycle_events (workspace_id, contact_id, from_stage, to_stage, source) VALUES ($1, $2, '', 'lead', 'manual')`,
		wsID, src.ContactID); err != nil {
		t.Fatal(err)
	}

	// An uploaded avatar on src (dst has none, so it should move).
	if _, err := db.ExecContext(ctx, `
		INSERT INTO contact_avatars (workspace_id, contact_id, content_type, size_bytes, data) VALUES ($1, $2, 'image/png', 3, '\x010203')`,
		wsID, src.ContactID); err != nil {
		t.Fatal(err)
	}

	if err := contacts.MergeContacts(ctx, db, wsID, dst.ContactID, src.ContactID); err != nil {
		t.Fatalf("MergeContacts: %v", err)
	}

	// src is gone.
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM contacts WHERE workspace_id = $1 AND id = $2`, wsID, src.ContactID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("src should have been deleted after the merge")
	}

	// Identities: dst carries both.
	ids, err := contacts.ListIdentities(ctx, db, wsID, dst.ContactID)
	if err != nil {
		t.Fatal(err)
	}
	var hasEmail, hasPhone, hasTelegram bool
	for _, id := range ids {
		switch {
		case id.Channel == contacts.ChannelEmail && id.ExternalID == "dst@example.test":
			hasEmail = true
		case id.Channel == contacts.ChannelPhone && id.ExternalID == "+201099999999":
			hasPhone = true
		case id.Channel == contacts.ChannelHandle && id.ExternalID == "@src_tg":
			hasTelegram = true
		}
	}
	if !hasEmail || !hasPhone || !hasTelegram {
		t.Errorf("survivor should carry all three identities after the merge: %+v", ids)
	}

	// Tags: unioned.
	var tags pgtext.Array
	if err := db.QueryRowContext(ctx, `SELECT tags FROM contacts WHERE workspace_id = $1 AND id = $2`, wsID, dst.ContactID).Scan(&tags); err != nil {
		t.Fatal(err)
	}
	tagSet := map[string]bool{}
	for _, tg := range tags {
		tagSet[tg] = true
	}
	if !tagSet["vip"] || !tagSet["beta"] {
		t.Errorf("survivor should have gained src's tags, got %v", tags)
	}

	// Conversation moved.
	var convContact string
	if err := db.QueryRowContext(ctx, `SELECT contact_id FROM conversations WHERE workspace_id = $1 AND id = $2`, wsID, convID).Scan(&convContact); err != nil {
		t.Fatal(err)
	}
	if convContact != dst.ContactID {
		t.Errorf("conversation should have moved to dst, got contact_id=%q", convContact)
	}

	// Message's group-sender identity moved.
	var senderContact string
	if err := db.QueryRowContext(ctx, `SELECT sender_contact_id FROM messages WHERE workspace_id = $1 AND id = $2`, wsID, msgID).Scan(&senderContact); err != nil {
		t.Fatal(err)
	}
	if senderContact != dst.ContactID {
		t.Errorf("message.sender_contact_id should have moved to dst, got %q", senderContact)
	}

	// Context note moved.
	var noteContact string
	if err := db.QueryRowContext(ctx, `SELECT contact_id FROM contact_context_notes WHERE workspace_id = $1 AND id = $2`, wsID, noteID).Scan(&noteContact); err != nil {
		t.Fatal(err)
	}
	if noteContact != dst.ContactID {
		t.Errorf("context note should have moved to dst, got %q", noteContact)
	}

	// Lifecycle event moved.
	var lifecycleCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM contact_lifecycle_events WHERE workspace_id = $1 AND contact_id = $2 AND source = 'manual'`,
		wsID, dst.ContactID).Scan(&lifecycleCount); err != nil {
		t.Fatal(err)
	}
	if lifecycleCount != 1 {
		t.Errorf("lifecycle event should have moved to dst, found %d matching rows", lifecycleCount)
	}

	// Avatar moved (dst had none).
	var avatarContact string
	if err := db.QueryRowContext(ctx, `SELECT contact_id FROM contact_avatars WHERE workspace_id = $1`, wsID).Scan(&avatarContact); err != nil {
		t.Fatal(err)
	}
	if avatarContact != dst.ContactID {
		t.Errorf("avatar should have moved to dst, got contact_id=%q", avatarContact)
	}
}

// TestMergeContactsKeepsSurvivorsAvatar: when BOTH contacts already have an
// uploaded avatar, the survivor's own upload wins (contact_avatars is unique
// per contact) and the loser's is simply dropped, not left orphaned.
func TestMergeContactsKeepsSurvivorsAvatar(t *testing.T) {
	db, owner := appDB(t)
	wsID := workspaceIn(t, owner)
	ctx := withWS(context.Background(), db, wsID)

	dst, err := contacts.Upsert(ctx, db, wsID, contacts.Candidate{Email: "keepavatar@example.test", Name: "Dst", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	src, err := contacts.Upsert(ctx, db, wsID, contacts.Candidate{Phone: "+201088888888", Name: "Src", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO contact_avatars (workspace_id, contact_id, content_type, size_bytes, data) VALUES ($1, $2, 'image/png', 3, '\x0a0b0c')`,
		wsID, dst.ContactID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO contact_avatars (workspace_id, contact_id, content_type, size_bytes, data) VALUES ($1, $2, 'image/png', 3, '\x0d0e0f')`,
		wsID, src.ContactID); err != nil {
		t.Fatal(err)
	}

	if err := contacts.MergeContacts(ctx, db, wsID, dst.ContactID, src.ContactID); err != nil {
		t.Fatalf("MergeContacts: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM contact_avatars WHERE workspace_id = $1`, wsID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("exactly one avatar should remain, found %d", count)
	}
	var data []byte
	if err := db.QueryRowContext(ctx, `SELECT data FROM contact_avatars WHERE workspace_id = $1 AND contact_id = $2`, wsID, dst.ContactID).Scan(&data); err != nil {
		t.Fatalf("dst's own avatar should have survived: %v", err)
	}
	if len(data) != 3 || data[0] != 0x0a {
		t.Errorf("dst's avatar bytes should be its own upload, got %v", data)
	}
}

// TestMergeContactsRefusesSelfMerge: MergeContacts must never delete the only
// copy of a contact.
func TestMergeContactsRefusesSelfMerge(t *testing.T) {
	db, owner := appDB(t)
	wsID := workspaceIn(t, owner)
	ctx := withWS(context.Background(), db, wsID)

	r, err := contacts.Upsert(ctx, db, wsID, contacts.Candidate{Email: "solo@example.test", Name: "Solo", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if err := contacts.MergeContacts(ctx, db, wsID, r.ContactID, r.ContactID); err == nil {
		t.Errorf("merging a contact into itself should have been refused")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM contacts WHERE workspace_id = $1 AND id = $2`, wsID, r.ContactID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("the contact should still exist after a refused self-merge")
	}
}
