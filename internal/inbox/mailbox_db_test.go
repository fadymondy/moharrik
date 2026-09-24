package inbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
)

/*
Several mailboxes on one workspace, against a real Postgres database with RLS
on (MH-555): the same customer writing to two different mailboxes threads and
dedupes into ONE conversation, and a reply picks the SMTP service the
CONVERSATION's own mailbox names rather than the workspace default.

This is deliberately not the Docker/GreenMail test the card also asks for
(that one is IMAP wire-protocol coverage — dialing, SEARCH, FETCH, marking
\Seen — see the batch report for its status). File() and resolveSMTP() take no
network at all, so what they get right or wrong is entirely a database
question, and this is what actually answers it.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/inbox/ -run Mailbox -v
*/

func testApp(t *testing.T) *app.App {
	t.Helper()
	owner, appURL := testdb.Setup(t, "inbox", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "inbox-test-secret-inbox-test-secret-inbox-01")
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

type mbFixture struct {
	a      *app.App
	wsID   string
	userID string
	ctx    context.Context
}

func newMailboxWorkspace(t *testing.T, a *app.App) mbFixture {
	t.Helper()
	ctx := context.Background()
	suffix := idSuffix()
	f := mbFixture{a: a, userID: "u-" + suffix}
	if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', 'now')`,
		f.userID, "owner-"+suffix+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRowContext(ctx, `
		INSERT INTO workspaces (name, slug, created_by, locale, currency) VALUES ('Mailbox Test', $1, $2, 'en', 'USD') RETURNING id`,
		"mbx-"+suffix, f.userID).Scan(&f.wsID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')`,
		f.wsID, f.userID); err != nil {
		t.Fatal(err)
	}
	f.ctx = tenant.WithWorkspace(ctx, f.wsID)
	return f
}

// mailbox creates one mailbox row directly (bypassing the HTTP layer, which
// mailbox_routes_test-shaped coverage would exercise separately) and returns
// its id.
func (f mbFixture) mailbox(t *testing.T, name string, primary bool, smtpServiceID string) string {
	t.Helper()
	var id string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO mailboxes (workspace_id, name, is_primary, imap_host, imap_port, username, password_sealed, tls_mode, folder, smtp_service_id, enabled, created_by)
		VALUES ($1, $2, $3, 'imap.example.test', 993, $4, '', 'tls', 'INBOX', $5, true, $6) RETURNING id`,
		f.wsID, name, primary, strings.ToLower(name)+"@example.test", nullable(smtpServiceID), f.userID,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f mbFixture) smtpService(t *testing.T, name string, isDefault bool) string {
	t.Helper()
	var id string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO smtp_services (workspace_id, name, host, port, username, password_sealed, from_email, tls_mode, is_default, is_campaign_default, created_by)
		VALUES ($1, $2, '127.0.0.1', 2525, 'u', '', $3, 'none', $4, $4, $5) RETURNING id`,
		f.wsID, name, strings.ToLower(name)+"@example.test", isDefault, f.userID,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestFileDedupesAndThreadsAcrossMailboxes(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)
	support := f.mailbox(t, "Support", true, "")
	sales := f.mailbox(t, "Sales", false, "")

	from := "customer-" + idSuffix() + "@buyer.test"
	e1 := &Email{MessageID: "m1-" + idSuffix() + "@buyer.test", From: from, FromName: "Customer", Subject: "Order #42", Body: "First message", Date: time.Now()}
	if err := File(f.ctx, a, f.wsID, support, e1); err != nil {
		t.Fatal(err)
	}
	// A SECOND, unrelated message (no Message-ID reference) from the SAME
	// sender with the SAME normalised subject, but to a DIFFERENT mailbox: this
	// is the "same customer, wrote to a different one of our addresses" case
	// MH-555 asks to thread and dedupe, and it must land in the SAME
	// conversation as e1 — findThread is workspace+channel scoped, not
	// mailbox-scoped (imap.go's own doc explains why).
	e2 := &Email{MessageID: "m2-" + idSuffix() + "@buyer.test", From: from, FromName: "Customer", Subject: "Re: Order #42", Body: "Second message", Date: time.Now()}
	if err := File(f.ctx, a, f.wsID, sales, e2); err != nil {
		t.Fatal(err)
	}

	var convCount int
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT count(*) FROM conversations WHERE workspace_id = $1 AND customer_email = $2`, f.wsID, from).Scan(&convCount); err != nil {
		t.Fatal(err)
	}
	if convCount != 1 {
		t.Fatalf("two mailboxes filing mail from the same customer made %d conversations, want 1 (no dedupe/thread across mailboxes)", convCount)
	}

	var convMailboxID string
	var msgCount int
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT mailbox_id, (SELECT count(*) FROM messages m WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id)
		FROM conversations c WHERE workspace_id = $1 AND customer_email = $2`, f.wsID, from).Scan(&convMailboxID, &msgCount); err != nil {
		t.Fatal(err)
	}
	if msgCount != 2 {
		t.Fatalf("messages in the merged thread = %d, want 2", msgCount)
	}
	if convMailboxID != support {
		t.Errorf("the conversation's mailbox_id = %q, want the FIRST mailbox that created it (%q) — it must not move when a later mailbox adds to the same thread", convMailboxID, support)
	}

	// Each individual MESSAGE still remembers which mailbox it actually
	// arrived on, even though the conversation itself stays with the first.
	var msg1Mailbox, msg2Mailbox string
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT mailbox_id FROM messages WHERE workspace_id = $1 AND external_id = $2`, f.wsID, e1.MessageID).Scan(&msg1Mailbox); err != nil {
		t.Fatal(err)
	}
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT mailbox_id FROM messages WHERE workspace_id = $1 AND external_id = $2`, f.wsID, e2.MessageID).Scan(&msg2Mailbox); err != nil {
		t.Fatal(err)
	}
	if msg1Mailbox != support || msg2Mailbox != sales {
		t.Errorf("messages should remember their OWN mailbox: got %q and %q, want %q and %q", msg1Mailbox, msg2Mailbox, support, sales)
	}

	// A redelivery of e1 (same Message-ID, arriving again — some servers
	// redeliver on a flaky STORE) must not duplicate it, on either mailbox.
	if err := File(f.ctx, a, f.wsID, support, e1); err != nil {
		t.Fatal(err)
	}
	if err := File(f.ctx, a, f.wsID, sales, e1); err != nil {
		t.Fatal(err)
	}
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT (SELECT count(*) FROM messages m WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id)
		FROM conversations c WHERE workspace_id = $1 AND customer_email = $2`, f.wsID, from).Scan(&msgCount); err != nil {
		t.Fatal(err)
	}
	if msgCount != 2 {
		t.Fatalf("a redelivered message (even filed against a different mailbox) duplicated: %d messages, want 2", msgCount)
	}
}

func TestResolveSMTPPicksTheMailboxsOwnService(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)
	def := f.smtpService(t, "Default", true)
	other := f.smtpService(t, "Marketing", false)
	withOwn := f.mailbox(t, "Support", true, other)
	withoutOwn := f.mailbox(t, "Sales", false, "")

	svc, err := resolveSMTP(f.ctx, a.SQLDB, f.wsID, withOwn)
	if err != nil {
		t.Fatal(err)
	}
	if svc.ServiceID != other {
		t.Errorf("mailbox with its own SMTP service resolved to %q (%s), want %q", svc.ServiceID, svc.ServiceName, other)
	}

	svc, err = resolveSMTP(f.ctx, a.SQLDB, f.wsID, withoutOwn)
	if err != nil {
		t.Fatal(err)
	}
	if svc.ServiceID != def {
		t.Errorf("mailbox with no SMTP service of its own resolved to %q, want the workspace default %q", svc.ServiceID, def)
	}

	// No mailbox at all (a pre-MH-555 conversation, or one filed with mailboxID=""):
	// falls back to the workspace default too.
	svc, err = resolveSMTP(f.ctx, a.SQLDB, f.wsID, "")
	if err != nil {
		t.Fatal(err)
	}
	if svc.ServiceID != def {
		t.Errorf("no mailboxID resolved to %q, want the workspace default %q", svc.ServiceID, def)
	}
}
