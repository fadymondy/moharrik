package whatsapp

// MH-602: RefreshContactPhoto against a real Postgres database with RLS on —
// caches a fetched photo, respects the refresh window (no re-fetch inside a
// week), and caches a FAILURE too so a workspace with no WhatsApp photo for a
// customer, or a flaky Evolution, is not hammered on every inbound message.
// Evolution itself is still fully faked (httptest), per the card's rule.
//
//	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/whatsapp/ -run RefreshContactPhoto -v

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
)

func testApp(t *testing.T) *app.App {
	t.Helper()
	owner, appURL := testdb.Setup(t, "whatsapp", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "wa-avatar-test-secret-wa-avatar-test-secret-01")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("SCHEDULER", "off")
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	k := togo.New()
	a := app.New(context.Background(), k)
	t.Cleanup(func() { k.Close() })
	return a
}

func idSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newAvatarWorkspaceAndContact(t *testing.T, a *app.App) (wsID, contactID string, ctx context.Context) {
	t.Helper()
	base := context.Background()
	suffix := idSuffix(t)
	if err := a.SysDB.QueryRowContext(base, `
		INSERT INTO workspaces (name, slug, created_by) VALUES ('WA Avatar Test', $1, 'test') RETURNING id`,
		"wa-avt-"+suffix).Scan(&wsID); err != nil {
		t.Fatal(err)
	}
	ctx = tenant.WithWorkspace(base, wsID)
	if err := a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO contacts (workspace_id, name, phone, source) VALUES ($1, 'Customer', $2, 'manual') RETURNING id`,
		wsID, "20100"+suffix).Scan(&contactID); err != nil {
		t.Fatal(err)
	}
	return wsID, contactID, ctx
}

func TestRefreshContactPhotoCachesAResult(t *testing.T) {
	a := testApp(t)
	wsID, contactID, ctx := newAvatarWorkspaceAndContact(t, a)

	srv := fakeEvolutionWithPhoto(t, true)
	cfg := Config{URL: srv.URL, Key: "test-key"}
	link := &Link{WorkspaceID: wsID, Instance: "moh-" + wsID}

	RefreshContactPhoto(ctx, a.SQLDB, cfg, link, wsID, contactID, "201234567890")

	var photoURL, source, status string
	var fetchedAt time.Time
	if err := a.SQLDB.QueryRowContext(ctx, `
		SELECT photo_url, photo_source, photo_fetch_status, photo_fetched_at FROM contacts WHERE workspace_id = $1 AND id = $2`,
		wsID, contactID).Scan(&photoURL, &source, &status, &fetchedAt); err != nil {
		t.Fatal(err)
	}
	if status != "ok" || source != "whatsapp" || photoURL == "" {
		t.Fatalf("expected a cached ok photo, got status=%q source=%q photo_url=%q", status, source, photoURL)
	}
	if time.Since(fetchedAt) > time.Minute {
		t.Errorf("photo_fetched_at should be ~now, got %v", fetchedAt)
	}
}

func TestRefreshContactPhotoCachesAFailureAndDoesNotRetryInsideTheWindow(t *testing.T) {
	a := testApp(t)
	wsID, contactID, ctx := newAvatarWorkspaceAndContact(t, a)

	// A server that is Evolution-shaped but always errors, the "flaky
	// Evolution" case the window is specifically there to protect against.
	down := Config{URL: "http://127.0.0.1:1", Key: "test-key"} // nothing listens here
	link := &Link{WorkspaceID: wsID, Instance: "moh-" + wsID}

	RefreshContactPhoto(ctx, a.SQLDB, down, link, wsID, contactID, "201234567890")

	var status string
	var firstFetchedAt time.Time
	if err := a.SQLDB.QueryRowContext(ctx, `
		SELECT photo_fetch_status, photo_fetched_at FROM contacts WHERE workspace_id = $1 AND id = $2`,
		wsID, contactID).Scan(&status, &firstFetchedAt); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("expected the failure to be cached as 'failed', got %q", status)
	}

	// A second call inside the week-long window must not touch Evolution
	// again — proven here by pointing it at a server that WOULD succeed, and
	// asserting the row (and its status) does not change.
	srv := fakeEvolutionWithPhoto(t, true)
	up := Config{URL: srv.URL, Key: "test-key"}
	RefreshContactPhoto(ctx, a.SQLDB, up, link, wsID, contactID, "201234567890")

	var status2 string
	var secondFetchedAt time.Time
	if err := a.SQLDB.QueryRowContext(ctx, `
		SELECT photo_fetch_status, photo_fetched_at FROM contacts WHERE workspace_id = $1 AND id = $2`,
		wsID, contactID).Scan(&status2, &secondFetchedAt); err != nil {
		t.Fatal(err)
	}
	if status2 != "failed" || !secondFetchedAt.Equal(firstFetchedAt) {
		t.Errorf("a call inside the refresh window must be a no-op, got status=%q fetched_at changed=%v",
			status2, !secondFetchedAt.Equal(firstFetchedAt))
	}
}
