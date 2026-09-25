package inbox

// MH-602 against a real Postgres database with RLS on: a conversation JSON
// carries its linked contact's name (so the UI can prefer it over
// customer_name/phone — web/app/[locale]/w/[slug]/inbox/page.tsx does that
// part), and avatar_url follows the documented priority: an uploaded/provider
// contact photo when linked, else the conversation's OWN cached WhatsApp
// photo, else empty (client renders initials).

import (
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/crm"
	"github.com/fadymondy/moharrik/internal/tenant"
)

func TestConversationJSONCarriesTheLinkedContactName(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)

	var contactID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO contacts (workspace_id, name, phone, source) VALUES ($1, 'Real Name', $2, 'manual') RETURNING id`,
		f.wsID, "20155501"+idSuffix()).Scan(&contactID); err != nil {
		t.Fatal(err)
	}
	// customer_name intentionally holds the phone, the exact case the task
	// brief calls out: "customer_name is the phone" until a contact is linked.
	var convID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_name, customer_phone, contact_id, subject, last_message_at)
		VALUES ($1, 'whatsapp', '201555019999', '201555019999', $2, 'hi', now()) RETURNING id`,
		f.wsID, contactID).Scan(&convID); err != nil {
		t.Fatal(err)
	}

	out, err := readConversation(f.ctx, a.SQLDB, f.wsID, convID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Contact == nil || out.Contact.Name != "Real Name" {
		t.Fatalf("expected the linked contact's name on the JSON, got %+v", out.Contact)
	}
	if out.CustomerName != "201555019999" {
		t.Fatalf("customer_name should be untouched (still the phone) — the UI, not the API, prefers contact.name; got %q", out.CustomerName)
	}
}

func TestConversationAvatarURLPriority(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)

	// Case 1: linked contact with an uploaded avatar wins over everything.
	var contactID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO contacts (workspace_id, name, phone, source) VALUES ($1, 'Has Avatar', $2, 'manual') RETURNING id`,
		f.wsID, "20166601"+idSuffix()).Scan(&contactID); err != nil {
		t.Fatal(err)
	}
	// UploadContactAvatar calls scope(), which needs a real role — f.ctx is a
	// system-job scope (tenant.WithWorkspace, no role), so build a
	// session-shaped one here just for this call, same as an authenticated
	// HTTP request would carry.
	sessCtx := tenant.With(f.ctx, &tenant.Scope{WorkspaceID: f.wsID, UserID: f.userID, Role: "owner", Via: "session"})
	if _, err := crm.UploadContactAvatar(sessCtx, a.SQLDB, contactID, tinyPNGB64, "image/png", "a.png"); err != nil {
		t.Fatal(err)
	}
	var convID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_phone, contact_id, subject, last_message_at)
		VALUES ($1, 'whatsapp', $2, $3, 'hi', now()) RETURNING id`,
		f.wsID, "20166601", contactID).Scan(&convID); err != nil {
		t.Fatal(err)
	}
	out, err := readConversation(f.ctx, a.SQLDB, f.wsID, convID)
	if err != nil {
		t.Fatal(err)
	}
	if out.AvatarURL == "" {
		t.Error("a linked contact's uploaded avatar must show on the conversation")
	}

	// Case 2: unlinked conversation with its OWN cached WhatsApp photo.
	var convID2 string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_phone, subject, last_message_at, photo_url, photo_source, photo_fetched_at, photo_fetch_status)
		VALUES ($1, 'whatsapp', $2, 'hi', now(), 'data:image/png;base64,xx', 'whatsapp', now(), 'ok') RETURNING id`,
		f.wsID, "20177701"+idSuffix()).Scan(&convID2); err != nil {
		t.Fatal(err)
	}
	out2, err := readConversation(f.ctx, a.SQLDB, f.wsID, convID2)
	if err != nil {
		t.Fatal(err)
	}
	// MH-602 defect 2: the JSON must never carry the data: URI itself — the
	// conversation's own cached photo comes back as a signed provider-photo
	// link the client can use as a bare <img src>.
	wantPrefix := "/api/provider-photos/conversation/" + convID2 + "?"
	if !strings.HasPrefix(out2.AvatarURL, wantPrefix) {
		t.Errorf("an unlinked conversation's photo should be a signed provider-photo URL starting with %q, got %q", wantPrefix, out2.AvatarURL)
	}
	if strings.Contains(out2.AvatarURL, "data:") {
		t.Errorf("avatar_url must never contain a data: URI, got %q", out2.AvatarURL)
	}

	// Case 3: nothing at all — empty, so the client renders initials.
	var convID3 string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_phone, subject, last_message_at)
		VALUES ($1, 'whatsapp', $2, 'hi', now()) RETURNING id`,
		f.wsID, "20188801"+idSuffix()).Scan(&convID3); err != nil {
		t.Fatal(err)
	}
	out3, err := readConversation(f.ctx, a.SQLDB, f.wsID, convID3)
	if err != nil {
		t.Fatal(err)
	}
	if out3.AvatarURL != "" {
		t.Errorf("with no photo of any kind, avatar_url must be empty, got %q", out3.AvatarURL)
	}
}

// tinyPNGB64 is the smallest possible valid PNG (1x1 transparent pixel).
const tinyPNGB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
