package marketing_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/db/migrate"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/tenant"
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

	owner, appURL := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_APP_DATABASE_URL")
	if owner == "" || appURL == "" {
		t.Skip("set TEST_DATABASE_URL and TEST_APP_DATABASE_URL (a throwaway database) to run the subscribe tests")
	}

	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "subscribe-test-secret-subscribe-test-secret-01")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("SCHEDULER", "off")
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")

	ctx := context.Background()
	k0 := togo.New()
	sys, err := k0.SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Run(ctx, sys, appURL, "../..", t.Logf); err != nil {
		t.Fatal(err)
	}
	k0.Close()

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
