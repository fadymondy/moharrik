package marketing_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
	"github.com/togo-framework/togo"
)

/*
The consent rules, against a real database (MH-318).

These are the rules it would be worst to get subtly wrong, because the symptom
is not a failing page — it is somebody receiving mail they did not ask for, or
a subscriber being asked to confirm again and quietly dropping off the list.

Run as the other DB-backed suites do:

	TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:55479/moharrik_test?sslmode=disable \
	TEST_APP_DATABASE_URL=postgres://moharrik_app_test:test@127.0.0.1:55479/moharrik_test?sslmode=disable \
	go test -count=1 ./internal/marketing/ -run Subscribe -v
*/

func testApp(t *testing.T) *app.App {
	t.Helper()

	// Its own database (internal/testdb): three packages migrating ONE database
	// interfere — an ALTER from another package invalidates the statement
	// descriptions pgx has cached, and this suite then fails with something
	// that looks unrelated to it.
	owner, appURL := testdb.Setup(t, "marketing", "../..")

	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "subscribe-test-secret-subscribe-test-secret-01")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("SCHEDULER", "off")
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")

	ctx := context.Background()
	// testdb.Setup already migrated this package's own database, under a lock
	// held on the shared base database — the app ROLE is cluster-global, so
	// two packages creating it at once collide on the same catalogue row.

	k := togo.New()
	a := app.New(ctx, k)
	t.Cleanup(func() { k.Close() })
	return a
}

// listIn makes a workspace with one mailing list and returns both ids.
func listIn(t *testing.T, a *app.App, doubleOptIn bool) (workspaceID, listID string) {
	t.Helper()
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	slug := "sub-test-" + time.Unix(0, suffix).Format("150405.000000000")
	if err := a.SysDB.QueryRowContext(ctx, `
		INSERT INTO workspaces (name, slug, created_by) VALUES ('Subscribe Test', $1, 'test') RETURNING id`,
		slug).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}

	sctx := tenant.WithWorkspace(ctx, workspaceID)
	if err := a.SQLDB.QueryRowContext(sctx, `
		INSERT INTO mail_lists (workspace_id, name, double_opt_in) VALUES ($1, 'Newsletter', $2) RETURNING id`,
		workspaceID, doubleOptIn).Scan(&listID); err != nil {
		t.Fatal(err)
	}
	return workspaceID, listID
}

func TestSubscribeDoubleOptInLandsPending(t *testing.T) {
	a := testApp(t)
	wsID, listID := listIn(t, a, true)
	sctx := tenant.WithWorkspace(context.Background(), wsID)

	res, err := marketing.Subscribe(sctx, a.SQLDB, wsID, listID, "  Someone@Example.COM ", "Someone", "en", "form")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != marketing.StatusPending {
		t.Fatalf("status = %q, want pending — a double opt-in list must not subscribe anyone directly", res.Status)
	}
	if !res.NeedsConfirmation || res.Token == "" {
		t.Fatal("no confirmation was asked for, so nobody would ever be asked to consent")
	}

	// The address is normalised, or the same person subscribes twice under two
	// spellings and unsubscribing one leaves the other.
	var email string
	if err := a.SQLDB.QueryRowContext(sctx,
		`SELECT email FROM contacts WHERE workspace_id = $1 AND id = $2`, wsID, res.ContactID).Scan(&email); err != nil {
		t.Fatal(err)
	}
	if email != "someone@example.com" {
		t.Fatalf("email stored as %q; it must be trimmed and lower-cased", email)
	}
}

func TestSubscribeWithoutDoubleOptInSubscribesImmediately(t *testing.T) {
	a := testApp(t)
	wsID, listID := listIn(t, a, false)
	sctx := tenant.WithWorkspace(context.Background(), wsID)

	res, err := marketing.Subscribe(sctx, a.SQLDB, wsID, listID, "direct@example.com", "", "en", "form")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != marketing.StatusSubscribed {
		t.Fatalf("status = %q, want subscribed", res.Status)
	}
	if res.NeedsConfirmation {
		t.Fatal("a list with opt-in off asked for a confirmation email anyway")
	}
}

func TestSubscribeAnExistingSubscriberChangesNothingAndSendsNothing(t *testing.T) {
	// The case that matters for a PUBLIC form: anyone can post a stranger's
	// address. If that reset them to pending, a subscriber would be asked to
	// confirm again — and quietly fall off the list when they ignored it.
	a := testApp(t)
	wsID, listID := listIn(t, a, false)
	sctx := tenant.WithWorkspace(context.Background(), wsID)

	first, err := marketing.Subscribe(sctx, a.SQLDB, wsID, listID, "member@example.com", "", "en", "form")
	if err != nil {
		t.Fatal(err)
	}

	second, err := marketing.Subscribe(sctx, a.SQLDB, wsID, listID, "member@example.com", "", "en", "form")
	if err != nil {
		t.Fatal(err)
	}
	if !second.AlreadySubscribed {
		t.Fatal("re-subscribing an existing subscriber was not recognised as a no-op")
	}
	if second.NeedsConfirmation {
		t.Fatal("an existing subscriber would have been emailed again")
	}
	if second.Status != marketing.StatusSubscribed {
		t.Fatalf("an existing subscriber ended up %q", second.Status)
	}
	if second.ContactID != first.ContactID {
		t.Fatal("a second contact row was created for the same address")
	}
}

func TestSubscribeAfterUnsubscribingAsksForConsentAgain(t *testing.T) {
	// Coming back must go through consent again, with a FRESH token — the old
	// link must stop working, or an unsubscribe can be undone by replaying an
	// email somebody still has.
	a := testApp(t)
	wsID, listID := listIn(t, a, true)
	sctx := tenant.WithWorkspace(context.Background(), wsID)

	first, err := marketing.Subscribe(sctx, a.SQLDB, wsID, listID, "returning@example.com", "", "en", "form")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SQLDB.ExecContext(sctx, `
		UPDATE list_subscriptions SET status = 'unsubscribed', unsubscribed_at = now()
		WHERE workspace_id = $1 AND contact_id = $2`, wsID, first.ContactID); err != nil {
		t.Fatal(err)
	}

	again, err := marketing.Subscribe(sctx, a.SQLDB, wsID, listID, "returning@example.com", "", "en", "form")
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != marketing.StatusPending || !again.NeedsConfirmation {
		t.Fatalf("a returning address was let straight back in: status=%q confirm=%v", again.Status, again.NeedsConfirmation)
	}
	if again.Token == first.Token {
		t.Fatal("the confirmation token was reused; the old link still works")
	}

	var unsubAt *time.Time
	if err := a.SQLDB.QueryRowContext(sctx,
		`SELECT unsubscribed_at FROM list_subscriptions WHERE workspace_id = $1 AND contact_id = $2`,
		wsID, first.ContactID).Scan(&unsubAt); err != nil {
		t.Fatal(err)
	}
	if unsubAt != nil {
		t.Fatal("the old unsubscribe timestamp was left behind on a re-subscription")
	}
}

func TestSubscribeWillNotMailTheSamePendingAddressRepeatedly(t *testing.T) {
	// Without this, replaying one form submission is a way to mail-bomb a
	// stranger from a domain they trust.
	a := testApp(t)
	wsID, listID := listIn(t, a, true)
	sctx := tenant.WithWorkspace(context.Background(), wsID)

	first, err := marketing.Subscribe(sctx, a.SQLDB, wsID, listID, "spammed@example.com", "", "en", "form")
	if err != nil {
		t.Fatal(err)
	}
	if !first.NeedsConfirmation {
		t.Fatal("the first submission did not ask for a confirmation")
	}

	second, err := marketing.Subscribe(sctx, a.SQLDB, wsID, listID, "spammed@example.com", "", "en", "form")
	if err != nil {
		t.Fatal(err)
	}
	if second.NeedsConfirmation {
		t.Fatal("an immediate re-submission would have sent a second confirmation email")
	}
	// Still pending, and still confirmable — holding the EMAIL back must not
	// break the subscription itself.
	if second.Status != marketing.StatusPending || second.Token == "" {
		t.Fatalf("the held-back submission left the row unusable: %+v", second)
	}

	// Past the cooldown it sends again, or somebody who lost the first email
	// could never subscribe.
	if _, err := a.SQLDB.ExecContext(sctx, `
		UPDATE list_subscriptions SET updated_at = now() - $3::interval
		WHERE workspace_id = $1 AND contact_id = $2`,
		wsID, first.ContactID, (marketing.ConfirmationCooldown + time.Minute).String()); err != nil {
		t.Fatal(err)
	}
	third, err := marketing.Subscribe(sctx, a.SQLDB, wsID, listID, "spammed@example.com", "", "en", "form")
	if err != nil {
		t.Fatal(err)
	}
	if !third.NeedsConfirmation {
		t.Fatal("after the cooldown the confirmation was still held back, so a lost email could never be replaced")
	}
}

func TestSubscribeRefusesAListFromAnotherWorkspace(t *testing.T) {
	a := testApp(t)
	wsA, _ := listIn(t, a, true)
	_, listB := listIn(t, a, true)

	sctx := tenant.WithWorkspace(context.Background(), wsA)
	if _, err := marketing.Subscribe(sctx, a.SQLDB, wsA, listB, "x@example.com", "", "en", "form"); err == nil {
		t.Fatal("subscribed someone to another workspace's list")
	}
}

// ── WhatsApp broadcasts (MH-320) ─────────────────────────────────────────

// contactOn subscribes an address and returns its contact id.
func contactOn(t *testing.T, a *app.App, wsID, listID, email string) string {
	t.Helper()
	sctx := tenant.WithWorkspace(context.Background(), wsID)
	res, err := marketing.Subscribe(sctx, a.SQLDB, wsID, listID, email, "", "en", "test")
	if err != nil {
		t.Fatal(err)
	}
	return res.ContactID
}

func setPhone(t *testing.T, a *app.App, wsID, contactID, phone string) {
	t.Helper()
	sctx := tenant.WithWorkspace(context.Background(), wsID)
	if _, err := a.SQLDB.ExecContext(sctx,
		`UPDATE contacts SET phone = $3 WHERE workspace_id = $1 AND id = $2`, wsID, contactID, phone); err != nil {
		t.Fatal(err)
	}
}

func audiencePhones(t *testing.T, a *app.App, wsID, listID string) map[string]bool {
	t.Helper()
	sctx := tenant.WithWorkspace(context.Background(), wsID)
	got, err := marketing.WhatsAppAudience(sctx, a.SQLDB, wsID, listID)
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]bool{}
	for _, r := range got {
		set[r.Phone] = true
	}
	return set
}

func TestSubscribeWhatsAppAudienceRequiresItsOwnConsent(t *testing.T) {
	// The rule the whole feature rests on: being on a mailing list is NOT
	// agreeing to a message on your phone. Every contact below is on the list;
	// only one of them may be broadcast to.
	a := testApp(t)
	wsID, listID := listIn(t, a, false)
	sctx := tenant.WithWorkspace(context.Background(), wsID)

	// Opted in, with a phone — the only one who should be reached.
	yes := contactOn(t, a, wsID, listID, "yes@example.com")
	setPhone(t, a, wsID, yes, "+20 100 000 0001")
	if err := marketing.SetWhatsAppOptIn(sctx, a.SQLDB, wsID, yes); err != nil {
		t.Fatal(err)
	}

	// On the list, has a phone, never agreed to WhatsApp.
	noConsent := contactOn(t, a, wsID, listID, "noconsent@example.com")
	setPhone(t, a, wsID, noConsent, "+20 100 000 0002")

	// Agreed, but there is nothing to send to.
	noPhone := contactOn(t, a, wsID, listID, "nophone@example.com")
	if err := marketing.SetWhatsAppOptIn(sctx, a.SQLDB, wsID, noPhone); err != nil {
		t.Fatal(err)
	}

	// Agreed once, then said STOP.
	stopped := contactOn(t, a, wsID, listID, "stopped@example.com")
	setPhone(t, a, wsID, stopped, "+20 100 000 0004")
	if err := marketing.SetWhatsAppOptIn(sctx, a.SQLDB, wsID, stopped); err != nil {
		t.Fatal(err)
	}
	if err := marketing.SetWhatsAppOptOut(sctx, a.SQLDB, wsID, stopped); err != nil {
		t.Fatal(err)
	}

	// Agreed to WhatsApp, but unsubscribed from the list. An unsubscribe is
	// "stop sending me this", and it is honoured across channels.
	left := contactOn(t, a, wsID, listID, "left@example.com")
	setPhone(t, a, wsID, left, "+20 100 000 0005")
	if err := marketing.SetWhatsAppOptIn(sctx, a.SQLDB, wsID, left); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SQLDB.ExecContext(sctx, `
		UPDATE list_subscriptions SET status = 'unsubscribed'
		WHERE workspace_id = $1 AND contact_id = $2`, wsID, left); err != nil {
		t.Fatal(err)
	}

	got := audiencePhones(t, a, wsID, listID)
	if len(got) != 1 || !got["+20 100 000 0001"] {
		t.Fatalf("audience = %v; only the opted-in contact with a phone may be broadcast to", got)
	}
}

func TestSubscribeWhatsAppOptInCannotUndoASTOP(t *testing.T) {
	// A form re-ticking a box must not silently put somebody back on a list
	// they asked to leave. Only an explicit action reverses a STOP.
	a := testApp(t)
	wsID, listID := listIn(t, a, false)
	sctx := tenant.WithWorkspace(context.Background(), wsID)

	id := contactOn(t, a, wsID, listID, "stopper@example.com")
	setPhone(t, a, wsID, id, "+20 111 111 1111")
	if err := marketing.SetWhatsAppOptIn(sctx, a.SQLDB, wsID, id); err != nil {
		t.Fatal(err)
	}
	if err := marketing.SetWhatsAppOptOut(sctx, a.SQLDB, wsID, id); err != nil {
		t.Fatal(err)
	}

	// The form tries again.
	if err := marketing.SetWhatsAppOptIn(sctx, a.SQLDB, wsID, id); err != nil {
		t.Fatal(err)
	}
	if len(audiencePhones(t, a, wsID, listID)) != 0 {
		t.Fatal("an opt-in put a contact who had said STOP back into the audience")
	}

	// The deliberate reversal does work, or somebody could never come back.
	if err := marketing.ClearWhatsAppOptOut(sctx, a.SQLDB, wsID, id); err != nil {
		t.Fatal(err)
	}
	if len(audiencePhones(t, a, wsID, listID)) != 1 {
		t.Fatal("clearing the opt-out did not let the contact back in")
	}
}

func TestSubscribeWhatsAppStopWordMatchesAPhoneWrittenDifferently(t *testing.T) {
	// The number WhatsApp delivers is not the one typed into the CRM.
	a := testApp(t)
	wsID, listID := listIn(t, a, false)
	sctx := tenant.WithWorkspace(context.Background(), wsID)

	id := contactOn(t, a, wsID, listID, "reply@example.com")
	setPhone(t, a, wsID, id, "+20 100 222 3333")
	if err := marketing.SetWhatsAppOptIn(sctx, a.SQLDB, wsID, id); err != nil {
		t.Fatal(err)
	}

	stopped, err := marketing.HandleStopWord(context.Background(), a, wsID, "201002223333@s.whatsapp.net", "STOP")
	if err != nil {
		t.Fatal(err)
	}
	if !stopped {
		t.Fatal("a STOP from a differently formatted number was not matched to its contact")
	}
	if len(audiencePhones(t, a, wsID, listID)) != 0 {
		t.Fatal("the contact is still in the audience after saying STOP")
	}

	// An ordinary message is not a STOP.
	again, err := marketing.HandleStopWord(context.Background(), a, wsID, "201002223333", "hello, is this still available?")
	if err != nil {
		t.Fatal(err)
	}
	if again {
		t.Fatal("an ordinary message was treated as a STOP")
	}
}

func TestSubscribeWhatsAppAudienceIsScopedToItsWorkspace(t *testing.T) {
	a := testApp(t)
	wsA, listA := listIn(t, a, false)
	wsB, listB := listIn(t, a, false)

	for _, c := range []struct{ ws, list, email, phone string }{
		{wsA, listA, "a@example.com", "+20 100 000 1111"},
		{wsB, listB, "b@example.com", "+20 100 000 2222"},
	} {
		sctx := tenant.WithWorkspace(context.Background(), c.ws)
		id := contactOn(t, a, c.ws, c.list, c.email)
		setPhone(t, a, c.ws, id, c.phone)
		if err := marketing.SetWhatsAppOptIn(sctx, a.SQLDB, c.ws, id); err != nil {
			t.Fatal(err)
		}
	}

	got := audiencePhones(t, a, wsA, listA)
	if len(got) != 1 || !got["+20 100 000 1111"] {
		t.Fatalf("audience = %v; the other workspace's contact leaked in", got)
	}
}

func TestSubscribeWhatsAppCountIsTheAudience(t *testing.T) {
	// The number shown before sending must be the number sent to. Both read the
	// same predicate; this pins that they cannot drift.
	a := testApp(t)
	wsID, listID := listIn(t, a, false)
	sctx := tenant.WithWorkspace(context.Background(), wsID)

	count := func() int64 {
		n, err := marketing.CountWhatsAppAudience(sctx, a.SQLDB, wsID, listID)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	id := contactOn(t, a, wsID, listID, "counted@example.com")
	setPhone(t, a, wsID, id, "+20 100 999 0001")
	if n := count(); n != 0 {
		t.Fatalf("count = %d before any opt-in; list membership is not WhatsApp consent", n)
	}

	if err := marketing.RecordWhatsAppOptIn(sctx, a.SQLDB, wsID, id); err != nil {
		t.Fatal(err)
	}
	if n, want := count(), int64(len(audiencePhones(t, a, wsID, listID))); n != 1 || n != want {
		t.Fatalf("count = %d, audience = %d; want both 1", n, want)
	}
}

func TestSubscribeWhatsAppRecordOptInRefusesAnOptedOutContact(t *testing.T) {
	// The workspace path must SAY it did nothing, so the page offers the
	// explicit reversal instead of showing a tick that did not take.
	a := testApp(t)
	wsID, listID := listIn(t, a, false)
	sctx := tenant.WithWorkspace(context.Background(), wsID)

	id := contactOn(t, a, wsID, listID, "refused@example.com")
	setPhone(t, a, wsID, id, "+20 100 999 0002")
	if err := marketing.SetWhatsAppOptOut(sctx, a.SQLDB, wsID, id); err != nil {
		t.Fatal(err)
	}
	if err := marketing.RecordWhatsAppOptIn(sctx, a.SQLDB, wsID, id); !errors.Is(err, marketing.ErrWhatsAppOptedOut) {
		t.Fatalf("err = %v; want ErrWhatsAppOptedOut", err)
	}
	if len(audiencePhones(t, a, wsID, listID)) != 0 {
		t.Fatal("a refused opt-in still reached the audience")
	}

	// A contact from another workspace is not found, not silently opted in.
	wsB, _ := listIn(t, a, false)
	if err := marketing.RecordWhatsAppOptIn(tenant.WithWorkspace(context.Background(), wsB), a.SQLDB, wsB, id); !errors.Is(err, marketing.ErrNoSuchContact) {
		t.Fatalf("cross-workspace opt-in err = %v; want ErrNoSuchContact", err)
	}
}
