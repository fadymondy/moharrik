package inbox

/*
MH-600: internal/db/post/178_email_one_conversation_per_sender.sql folds a
sender's already-split email conversations (from before findThread's rewrite
stopped new ones) into the one with the newest last_message_at, moving every
message and everything else that points at the losers, coalescing the
survivor's empty scalar columns from the most-recently-active loser,
recomputing last_message_at/unread from what actually survives, deleting the
emptied losers, and backing the (workspace, sender) invariant with a unique
index.

testdb.Setup already runs the full migrate.Run — including 178 itself — before
this test's body starts, so by the time it runs, the unique index already
exists and a fresh INSERT could never reach the split state 178 exists to
clean up (that is the whole point: the index makes a NEW split impossible).
To test the fold itself, this test recreates a pre-migration state on
purpose — drop the index, insert two conversations for one sender the way a
pre-MH-600 findThread could have — and then re-runs the same .sql file
directly against the OWNER connection (a.SysDB — SQLDB is the RLS-scoped app
role and cannot run a cross-workspace DO block), exactly as migrate.Run does,
and checks what it did. Proves both the fold and that a second run (rerun
before every test/migrate, per the post/ convention) is a no-op.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/inbox/ -run FoldsEmail -v
*/

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readMigration178(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "db", "post", "178_email_one_conversation_per_sender.sql"))
	if err != nil {
		t.Fatalf("could not read migration 178: %v", err)
	}
	return string(b)
}

func TestMigration178FoldsEmailConversationsBySender(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)

	// The index already exists (it shipped with the schema this test DB was
	// migrated to) — dropped here to recreate the PRE-migration state the
	// fold is meant to clean up.
	if _, err := a.SysDB.ExecContext(f.ctx, `DROP INDEX IF EXISTS conversations_email_sender_uq`); err != nil {
		t.Fatalf("could not drop the unique index to stage the test: %v", err)
	}

	sender := "buyer-" + idSuffix() + "@customer.test"
	now := time.Now()

	// The OLDER, loser conversation: has a contact_id and a customer_name the
	// survivor is missing, so the coalesce step has something to prove.
	contactID := f.contact(t, "Sender Contact")
	var convA string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_email, customer_name, contact_id, subject, last_message_at, unread)
		VALUES ($1, 'email', $2, 'Sender Contact', $3, 'A: opening question', $4, false) RETURNING id`,
		f.wsID, sender, contactID, now.Add(-2*time.Hour)).Scan(&convA); err != nil {
		t.Fatal(err)
	}
	// The NEWER conversation — the fold's survivor, since it has the newest
	// last_message_at. Its own customer_name/contact_id are blank/NULL on
	// purpose, so the coalesce from convA is what fills them.
	var convB string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_email, subject, last_message_at, unread)
		VALUES ($1, 'email', $2, 'B: a new subject', $3, true) RETURNING id`,
		f.wsID, sender, now.Add(-1*time.Hour)).Scan(&convB); err != nil {
		t.Fatal(err)
	}

	msgA := f.message(t, convA, "in", "the opening question", "A: opening question", now.Add(-3*time.Hour))
	msgB := f.message(t, convB, "in", "a follow-up on something else", "B: a new subject", now.Add(-1*time.Hour))

	if _, err := a.SQLDB.ExecContext(f.ctx, `
		INSERT INTO conversation_participants (workspace_id, conversation_id, identity, name)
		VALUES ($1, $2, $3, 'Sender Contact')`, f.wsID, convA, sender); err != nil {
		t.Fatal(err)
	}

	if _, err := a.SysDB.ExecContext(f.ctx, readMigration178(t)); err != nil {
		t.Fatalf("running migration 178 failed: %v", err)
	}

	// ---- exactly one conversation left for this sender, and it is convB. ----
	var survivorCount int
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT count(*) FROM conversations WHERE workspace_id = $1 AND channel = 'email' AND lower(customer_email) = lower($2)`,
		f.wsID, sender).Scan(&survivorCount); err != nil {
		t.Fatal(err)
	}
	if survivorCount != 1 {
		t.Fatalf("conversations left for %s = %d, want 1", sender, survivorCount)
	}

	var stillThere bool
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT EXISTS (SELECT 1 FROM conversations WHERE workspace_id = $1 AND id = $2)`,
		f.wsID, convA).Scan(&stillThere); err != nil {
		t.Fatal(err)
	}
	if stillThere {
		t.Fatal("the loser conversation (convA) was not deleted")
	}

	// ---- both messages now point at the survivor, each keeping its OWN subject. ----
	rows, err := a.SQLDB.QueryContext(f.ctx, `
		SELECT id, conversation_id, subject FROM messages WHERE workspace_id = $1 AND id IN ($2, $3) ORDER BY created_at`,
		f.wsID, msgA, msgB)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct{ id, convID, subject string }
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.convID, &r.subject); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2 (both must survive the fold): %+v", len(got), got)
	}
	for _, r := range got {
		if r.convID != convB {
			t.Fatalf("message %s.conversation_id = %q, want the survivor %q", r.id, r.convID, convB)
		}
	}
	if got[0].subject != "A: opening question" || got[1].subject != "B: a new subject" {
		t.Fatalf("messages kept the wrong subjects after the fold: %+v, want each its own pre-fold subject", got)
	}

	// ---- the participant row that pointed at the loser now points at the survivor. ----
	var participantConvID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT conversation_id FROM conversation_participants WHERE workspace_id = $1 AND identity = $2`,
		f.wsID, sender).Scan(&participantConvID); err != nil {
		t.Fatal(err)
	}
	if participantConvID != convB {
		t.Fatalf("conversation_participants.conversation_id = %q, want the survivor %q", participantConvID, convB)
	}

	// ---- the survivor coalesced convA's contact_id/customer_name, since its
	// own were blank, and never overwrote its own subject/unread. ----
	var gotContactID, gotName, gotSubject string
	var gotUnread bool
	var gotLastMessageAt time.Time
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT coalesce(contact_id, ''), customer_name, subject, unread, last_message_at
		FROM conversations WHERE workspace_id = $1 AND id = $2`,
		f.wsID, convB).Scan(&gotContactID, &gotName, &gotSubject, &gotUnread, &gotLastMessageAt); err != nil {
		t.Fatal(err)
	}
	if gotContactID != contactID {
		t.Fatalf("survivor.contact_id = %q, want it coalesced from the loser: %q", gotContactID, contactID)
	}
	if gotName != "Sender Contact" {
		t.Fatalf("survivor.customer_name = %q, want it coalesced from the loser", gotName)
	}
	if gotSubject != "B: a new subject" {
		t.Fatalf("survivor.subject = %q, want its own kept, not the loser's", gotSubject)
	}
	if !gotUnread {
		t.Fatal("survivor.unread = false, want true (recomputed: it was unread before the fold)")
	}
	// Recomputed from MAX(messages.created_at) over the survivor's own
	// messages post-fold — msgA is now the OLDEST of the two, msgB the
	// newest, so the survivor's last_message_at must equal msgB's time, not
	// its own pre-fold value (which happened to also be msgB's time here, so
	// this also exercises that the recompute did not silently no-op).
	if gotLastMessageAt.Unix() != now.Add(-1*time.Hour).Unix() {
		t.Fatalf("survivor.last_message_at = %v, want %v (recomputed from surviving messages)",
			gotLastMessageAt, now.Add(-1*time.Hour))
	}

	// ---- the unique index is back, enforcing the invariant going forward. ----
	var indexExists bool
	if err := a.SysDB.QueryRowContext(f.ctx, `
		SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'conversations_email_sender_uq')`,
	).Scan(&indexExists); err != nil {
		t.Fatal(err)
	}
	if !indexExists {
		t.Fatal("conversations_email_sender_uq was not recreated")
	}

	// ---- idempotent: nothing left to fold, a second run is a no-op. ----
	if _, err := a.SysDB.ExecContext(f.ctx, readMigration178(t)); err != nil {
		t.Fatalf("running migration 178 a second time failed: %v", err)
	}
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT count(*) FROM conversations WHERE workspace_id = $1 AND channel = 'email' AND lower(customer_email) = lower($2)`,
		f.wsID, sender).Scan(&survivorCount); err != nil {
		t.Fatal(err)
	}
	if survivorCount != 1 {
		t.Fatalf("conversations left for %s after a second run = %d, want still 1", sender, survivorCount)
	}
}

func (f mbFixture) contact(t *testing.T, name string) string {
	t.Helper()
	var id string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO contacts (workspace_id, name, email, source) VALUES ($1, $2, $3, 'inbox') RETURNING id`,
		f.wsID, name, "contact-"+idSuffix()+"@customer.test").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f mbFixture) message(t *testing.T, convID, direction, body, subject string, createdAt time.Time) string {
	t.Helper()
	var id string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO messages (workspace_id, conversation_id, direction, body, subject, created_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		f.wsID, convID, direction, body, subject, createdAt).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
