package crm

/*
Contact avatar upload (MH-602), against a real Postgres database with RLS on:
type and size are validated server-side (a client can lie about content_type),
and a workspace cannot touch another workspace's avatar even by guessing its
contact id — the same isolation every other CRM write already has.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/crm/ -run Avatar -v
*/

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
)

func testApp(t *testing.T) *app.App {
	t.Helper()
	owner, appURL := testdb.Setup(t, "crm", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "crm-test-secret-crm-test-secret-crm-test-01")
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

func idSuffix(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type crmFixture struct {
	a      *app.App
	wsID   string
	userID string
	ctx    context.Context
}

func newCRMWorkspace(t *testing.T, a *app.App) crmFixture {
	t.Helper()
	ctx := context.Background()
	suffix := idSuffix(t)
	userID := "u-" + suffix
	if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', 'now')`,
		userID, "owner-"+suffix+"@example.test"); err != nil {
		t.Fatal(err)
	}
	f := crmFixture{a: a, userID: userID}
	if err := a.SysDB.QueryRowContext(ctx, `
		INSERT INTO workspaces (name, slug, created_by, locale, currency) VALUES ('Avatar Test', $1, $2, 'en', 'USD') RETURNING id`,
		"avt-"+suffix, userID).Scan(&f.wsID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')`,
		f.wsID, userID); err != nil {
		t.Fatal(err)
	}
	// scope() needs a real role, which tenant.WithWorkspace (a system-job
	// context, no role) deliberately does not carry — this mirrors what an
	// actual session-authenticated request puts on ctx.
	f.ctx = tenant.With(ctx, &tenant.Scope{WorkspaceID: f.wsID, UserID: userID, Role: "owner", Via: "session"})
	return f
}

func newContact(t *testing.T, a *app.App, wsID, name string) string {
	t.Helper()
	var id string
	if err := a.SysDB.QueryRowContext(context.Background(), `
		INSERT INTO contacts (workspace_id, name, email, source) VALUES ($1, $2, $3, 'manual') RETURNING id`,
		wsID, name, name+"-"+idSuffix(t)+"@example.test").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// tinyPNG is the smallest possible valid PNG (a 1x1 transparent pixel) — real
// bytes, so the content-type validation is exercised against something that
// actually decodes, not a placeholder string.
const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestUploadContactAvatarStoresAndServesIt(t *testing.T) {
	a := testApp(t)
	f := newCRMWorkspace(t, a)
	contactID := newContact(t, a, f.wsID, "Sara")

	c, err := UploadContactAvatar(f.ctx, a.SQLDB, contactID, tinyPNGBase64, "image/png", "avatar.png")
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if c.AvatarURL == "" {
		t.Fatal("avatar_url must be set once an avatar is uploaded")
	}

	// A re-upload REPLACES the row rather than accumulating a second one
	// (contact_avatars is UNIQUE on (workspace_id, contact_id)).
	c2, err := UploadContactAvatar(f.ctx, a.SQLDB, contactID, tinyPNGBase64, "image/jpeg", "again.jpg")
	if err != nil {
		t.Fatalf("re-upload: %v", err)
	}
	var count int
	if err := a.SysDB.QueryRowContext(context.Background(), `SELECT count(*) FROM contact_avatars WHERE workspace_id = $1 AND contact_id = $2`,
		f.wsID, contactID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one avatar row after a replace, got %d", count)
	}
	if c2.AvatarURL == "" {
		t.Error("avatar_url must still be set after a replace")
	}
	var contentType string
	if err := a.SysDB.QueryRowContext(context.Background(), `SELECT content_type FROM contact_avatars WHERE workspace_id = $1 AND contact_id = $2`,
		f.wsID, contactID).Scan(&contentType); err != nil {
		t.Fatal(err)
	}
	if contentType != "image/jpeg" {
		t.Errorf("the replace should have overwritten content_type, got %q", contentType)
	}

	// Remove falls back to no photo at all (no WhatsApp photo cached here).
	c3, err := RemoveContactAvatar(f.ctx, a.SQLDB, contactID)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if c3.AvatarURL != "" {
		t.Errorf("avatar_url should be empty after removal, got %q", c3.AvatarURL)
	}
	if err := a.SysDB.QueryRowContext(context.Background(), `SELECT count(*) FROM contact_avatars WHERE workspace_id = $1 AND contact_id = $2`,
		f.wsID, contactID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("the row must actually be deleted, not just unlinked")
	}
}

func TestUploadContactAvatarRejectsBadTypeAndSize(t *testing.T) {
	a := testApp(t)
	f := newCRMWorkspace(t, a)
	contactID := newContact(t, a, f.wsID, "Omar")

	if _, err := UploadContactAvatar(f.ctx, a.SQLDB, contactID, tinyPNGBase64, "image/gif", "x.gif"); err == nil {
		t.Error("a content type outside png/jpeg/webp must be refused")
	}
	if _, err := UploadContactAvatar(f.ctx, a.SQLDB, contactID, "not-base64!!", "image/png", "x.png"); err == nil {
		t.Error("invalid base64 must be refused")
	}
	oversized := base64.StdEncoding.EncodeToString(make([]byte, avatarMaxSize+1))
	if _, err := UploadContactAvatar(f.ctx, a.SQLDB, contactID, oversized, "image/png", "x.png"); err == nil {
		t.Error("an image over 2MB must be refused")
	}
}

func TestContactAvatarIsWorkspaceIsolated(t *testing.T) {
	a := testApp(t)
	f1 := newCRMWorkspace(t, a)
	f2 := newCRMWorkspace(t, a)
	contactID := newContact(t, a, f1.wsID, "Workspace One's Contact")

	if _, err := UploadContactAvatar(f1.ctx, a.SQLDB, contactID, tinyPNGBase64, "image/png", "a.png"); err != nil {
		t.Fatalf("upload in the owning workspace: %v", err)
	}

	// Workspace two, scoped via RLS, must not be able to reach workspace
	// one's contact at all — the composite (workspace_id, contact_id) FK plus
	// the tenant_isolation policy on contact_avatars is what MH-602's
	// migration added specifically to prevent this.
	if _, err := UploadContactAvatar(f2.ctx, a.SQLDB, contactID, tinyPNGBase64, "image/png", "b.png"); err == nil {
		t.Error("uploading against another workspace's contact id must fail, not silently attach")
	}
	if _, err := RemoveContactAvatar(f2.ctx, a.SQLDB, contactID); err == nil {
		t.Error("removing another workspace's contact avatar must fail too")
	}

	// The original upload must be untouched.
	var count int
	if err := a.SysDB.QueryRowContext(context.Background(), `SELECT count(*) FROM contact_avatars WHERE workspace_id = $1 AND contact_id = $2`,
		f1.wsID, contactID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("workspace one's avatar should be unaffected, found %d rows", count)
	}
}
