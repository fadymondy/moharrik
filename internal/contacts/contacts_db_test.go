package contacts_test

import (
	"context"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/contacts"
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
