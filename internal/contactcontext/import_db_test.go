package contactcontext

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
)

/*
MH-586, against a real Postgres database with RLS on: filing a WhatsApp export
into a contact's conversation, re-importing the same file (must add zero new
messages), removing one import (must delete exactly its own rows and nothing
else), and proving an import never raises an automation event.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/contactcontext/ -run DB -v
*/

func testApp(t *testing.T) *app.App {
	t.Helper()
	owner, appURL := testdb.Setup(t, "contactcontext", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "cc-test-secret-cc-test-secret-cc-test-01")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("SCHEDULER", "off")
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	k := togo.New()
	a := app.New(context.Background(), k)
	t.Cleanup(func() { k.Close() })
	if a.SQLDB == nil || a.SysDB == nil {
		t.Fatal("both pools are required")
	}
	return a
}

func idSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type ccFixture struct {
	a         *app.App
	ctx       context.Context
	wsID      string
	userID    string
	contactID string
}

// newWorkspaceWithContact seeds a workspace, its owner user, and one contact
// named "Sara Ali" (matching the sender name in testdata/android_export.txt),
// the way internal/inbox's DB tests seed a workspace: direct SQL on the owner
// pool (a.SysDB) so RLS never blocks the fixture itself, then a
// tenant-scoped ctx for everything that goes through the RLS-bound app pool.
func newWorkspaceWithContact(t *testing.T, a *app.App) ccFixture {
	t.Helper()
	ctx := context.Background()
	suffix := idSuffix()
	f := ccFixture{a: a, userID: "u-" + suffix}

	if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', now())`,
		f.userID, "owner-"+suffix+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRowContext(ctx, `
		INSERT INTO workspaces (name, slug, created_by, locale, currency) VALUES ('Contact Context Test', $1, $2, 'en', 'USD') RETURNING id`,
		"cctx-"+suffix, f.userID).Scan(&f.wsID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')`,
		f.wsID, f.userID); err != nil {
		t.Fatal(err)
	}
	f.ctx = tenant.WithWorkspace(ctx, f.wsID)

	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO contacts (workspace_id, name, email, source) VALUES ($1, 'Sara Ali', $2, 'manual') RETURNING id`,
		f.wsID, "sara-"+suffix+"@example.test").Scan(&f.contactID); err != nil {
		t.Fatal(err)
	}
	return f
}

func androidFixtureBytes(t *testing.T) []byte {
	t.Helper()
	return readFixture(t, "android_export.txt")
}

// TestDBCommitFilesMessages checks a fresh commit creates the conversation
// and files every non-system line, tagged source=whatsapp_export.
func TestDBCommitFilesMessages(t *testing.T) {
	a := testApp(t)
	f := newWorkspaceWithContact(t, a)

	res, err := Commit(f.ctx, a, f.wsID, f.contactID, f.userID, "android_export.txt", androidFixtureBytes(t), "Sara Ali")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	// 8 parsed lines in the fixture, 1 is a system line (dropped by Commit),
	// so 7 messages should land.
	if res.MessagesAdded != 7 {
		t.Fatalf("MessagesAdded = %d, want 7", res.MessagesAdded)
	}
	if res.MessagesSkipped != 0 {
		t.Fatalf("MessagesSkipped = %d, want 0 on a first import", res.MessagesSkipped)
	}

	var count int
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT count(*) FROM messages WHERE workspace_id=$1 AND conversation_id=$2 AND source=$3`,
		f.wsID, res.ConversationID, SourceWhatsAppExport).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 7 {
		t.Fatalf("stored message count = %d, want 7", count)
	}

	var channel string
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT channel FROM conversations WHERE workspace_id=$1 AND id=$2`,
		f.wsID, res.ConversationID).Scan(&channel); err != nil {
		t.Fatal(err)
	}
	if channel != "whatsapp" {
		t.Fatalf("conversation channel = %q, want whatsapp", channel)
	}
}

// TestDBReimportAddsZero is the dedupe requirement: committing the same file
// twice into the same contact adds nothing the second time.
func TestDBReimportAddsZero(t *testing.T) {
	a := testApp(t)
	f := newWorkspaceWithContact(t, a)
	data := androidFixtureBytes(t)

	first, err := Commit(f.ctx, a, f.wsID, f.contactID, f.userID, "android_export.txt", data, "Sara Ali")
	if err != nil {
		t.Fatalf("first Commit: %v", err)
	}
	if first.MessagesAdded != 7 {
		t.Fatalf("first import added %d, want 7", first.MessagesAdded)
	}

	second, err := Commit(f.ctx, a, f.wsID, f.contactID, f.userID, "android_export.txt", data, "Sara Ali")
	if err != nil {
		t.Fatalf("second Commit: %v", err)
	}
	if second.MessagesAdded != 0 {
		t.Fatalf("re-import added %d new messages, want 0", second.MessagesAdded)
	}
	if second.MessagesSkipped != 7 {
		t.Fatalf("re-import skipped %d, want 7 (all duplicates)", second.MessagesSkipped)
	}

	var total int
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT count(*) FROM messages WHERE workspace_id=$1 AND conversation_id=$2`,
		f.wsID, first.ConversationID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 7 {
		t.Fatalf("total stored messages after re-import = %d, want 7 (still just the first import)", total)
	}
}

// TestDBRemoveImportDeletesOnlyItsOwn imports two separate files (android then
// iOS fixtures) into the same contact, removes the first import, and checks
// exactly its messages are gone while the second import's messages remain.
func TestDBRemoveImportDeletesOnlyItsOwn(t *testing.T) {
	a := testApp(t)
	f := newWorkspaceWithContact(t, a)

	first, err := Commit(f.ctx, a, f.wsID, f.contactID, f.userID, "android_export.txt", androidFixtureBytes(t), "Sara Ali")
	if err != nil {
		t.Fatalf("first Commit: %v", err)
	}
	iosData := readFixture(t, "ios_export.txt")
	second, err := Commit(f.ctx, a, f.wsID, f.contactID, f.userID, "ios_export.txt", iosData, "Sara Ali")
	if err != nil {
		t.Fatalf("second Commit: %v", err)
	}
	if second.ConversationID != first.ConversationID {
		t.Fatalf("expected both imports to file into the same WhatsApp conversation for this contact")
	}

	if err := RemoveImport(f.ctx, a, f.wsID, f.contactID, first.ImportID); err != nil {
		t.Fatalf("RemoveImport: %v", err)
	}

	var remainingFromFirst, remainingFromSecond int
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT count(*) FROM messages WHERE workspace_id=$1 AND import_id=$2`,
		f.wsID, first.ImportID).Scan(&remainingFromFirst); err != nil {
		t.Fatal(err)
	}
	if remainingFromFirst != 0 {
		t.Fatalf("removed import still has %d messages", remainingFromFirst)
	}
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT count(*) FROM messages WHERE workspace_id=$1 AND import_id=$2`,
		f.wsID, second.ImportID).Scan(&remainingFromSecond); err != nil {
		t.Fatal(err)
	}
	// 5 non-system iOS lines, but 2 of them (the "Hi! Are we still on for
	// tomorrow?" / "Yes absolutely, see you at 10am" exchange) land in the
	// same minute with the same text and direction as their android-fixture
	// counterparts already filed by the first import, so the shared dedupe
	// index correctly treats them as the same message across imports: 3 new.
	if remainingFromSecond != 3 {
		t.Fatalf("untouched import's message count = %d, want 3 (5 non-system iOS lines minus 2 that dedupe against the first import)", remainingFromSecond)
	}

	var importRow int
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT count(*) FROM contact_context_imports WHERE workspace_id=$1 AND id=$2`,
		f.wsID, first.ImportID).Scan(&importRow); err != nil {
		t.Fatal(err)
	}
	if importRow != 0 {
		t.Fatalf("contact_context_imports row for the removed import was not deleted")
	}
}

// TestDBImportRaisesNoAutomation is the "must not trigger automations" gate:
// an enabled automation_rules row listening for message.received exists on
// the workspace, yet committing a WhatsApp export raises zero
// automation_rule_events — the trigger's explicit
// source=whatsapp_export/import_id bypass (174_contact_context.sql) holds
// even though every message is direction='in', which would otherwise fire it.
func TestDBImportRaisesNoAutomation(t *testing.T) {
	a := testApp(t)
	f := newWorkspaceWithContact(t, a)

	if _, err := a.SQLDB.ExecContext(f.ctx, `
		INSERT INTO automation_rules (workspace_id, name, trigger, enabled, conditions, actions, created_by, updated_by)
		VALUES ($1, 'notify on inbound', 'message.received', true, '[]'::jsonb, '[]'::jsonb, $2, $2)`,
		f.wsID, f.userID); err != nil {
		t.Fatalf("seeding an enabled automation rule: %v", err)
	}

	res, err := Commit(f.ctx, a, f.wsID, f.contactID, f.userID, "android_export.txt", androidFixtureBytes(t), "Sara Ali")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if res.MessagesAdded == 0 {
		t.Fatal("import filed nothing, the automation check below would be vacuous")
	}

	var events int
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT count(*) FROM automation_rule_events WHERE workspace_id=$1`,
		f.wsID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("import raised %d automation_rule_events, want 0 (imported messages must never trigger automations)", events)
	}
}
