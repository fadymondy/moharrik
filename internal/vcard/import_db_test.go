package vcard_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/testdb"
	"github.com/fadymondy/moharrik/internal/vcard"

	_ "github.com/fadymondy/moharrik/internal/plugins" // registers the pgx driver
)

/*
MH-584: import/commit/merge/idempotent-re-import against a real database,
mirroring internal/contacts' own DB test style (contacts_db_test.go) — the
APP pool directly, RLS enforced via app.workspace_id, a throwaway per-package
database from internal/testdb.
*/

func appDB(t *testing.T) (appPool, ownerPool *sql.DB) {
	t.Helper()
	ownerURL, appURL := testdb.Setup(t, "vcard", "../..")
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
	slug := "vcard-test-" + time.Now().Format("150405.000000000")
	if err := owner.QueryRowContext(context.Background(), `
		INSERT INTO workspaces (name, slug, created_by, default_country) VALUES ('Vcard Test', $1, 'test', 'EG') RETURNING id`,
		slug).Scan(&wsID); err != nil {
		t.Fatal(err)
	}
	return wsID
}

func withWS(ctx context.Context, db *sql.DB, wsID string) context.Context {
	_, _ = db.ExecContext(ctx, `SELECT set_config('app.workspace_id', $1, false)`, wsID)
	return ctx
}

const vcardOne = "BEGIN:VCARD\r\n" +
	"VERSION:3.0\r\n" +
	"FN:Sara Kamal\r\n" +
	"N:Kamal;Sara;;;\r\n" +
	"TEL;TYPE=CELL:01001112222\r\n" +
	"EMAIL:sara.kamal@example.test\r\n" +
	"UID:vcard-sara-0001\r\n" +
	"END:VCARD\r\n"

// vcardOneAgain is the same person as vcardOne, exported again with a phone
// added and the UID kept — the shape a "re-export from the same phone, six
// months later" file has.
const vcardOneAgain = "BEGIN:VCARD\r\n" +
	"VERSION:3.0\r\n" +
	"FN:Sara Kamal\r\n" +
	"N:Kamal;Sara;;;\r\n" +
	"TEL;TYPE=CELL:01001112222\r\n" +
	"TEL;TYPE=HOME:0223334444\r\n" +
	"EMAIL:sara.kamal@example.test\r\n" +
	"UID:vcard-sara-0001\r\n" +
	"END:VCARD\r\n"

const vcardInvalid = "BEGIN:VCARD\r\nVERSION:3.0\r\nNOTE:no name, no phone, no email, no UID\r\nEND:VCARD\r\n"

func TestPreviewClassifiesNewMergeInvalid(t *testing.T) {
	db, owner := appDB(t)
	wsID := workspaceIn(t, owner)
	ctx := withWS(context.Background(), db, wsID)

	pv, err := vcard.PreviewImport(ctx, db, wsID, []byte(vcardOne+vcardInvalid), "EG")
	if err != nil {
		t.Fatalf("PreviewImport: %v", err)
	}
	if pv.CardCount != 2 || pv.NewCount != 1 || pv.InvalidCount != 1 || pv.MergeCount != 0 {
		t.Fatalf("Preview = %+v", pv)
	}

	if _, err := vcard.CommitImport(ctx, db, wsID, "tester", []byte(vcardOne), "EG"); err != nil {
		t.Fatalf("CommitImport: %v", err)
	}

	pv2, err := vcard.PreviewImport(ctx, db, wsID, []byte(vcardOne), "")
	if err != nil {
		t.Fatalf("PreviewImport after commit: %v", err)
	}
	if pv2.MergeCount != 1 || pv2.NewCount != 0 {
		t.Fatalf("re-preview after commit should classify as merge: %+v", pv2)
	}
}

func TestCommitCreatesMergesAndIsIdempotent(t *testing.T) {
	db, owner := appDB(t)
	wsID := workspaceIn(t, owner)
	ctx := withWS(context.Background(), db, wsID)

	res1, err := vcard.CommitImport(ctx, db, wsID, "tester", []byte(vcardOne), "EG")
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}
	if res1.CreatedCount != 1 || res1.MergedCount != 0 {
		t.Fatalf("first commit = %+v, want 1 created", res1)
	}
	contactID := res1.ContactIDs[0]

	// Re-importing the exact same card: matches on the vcarduid identity,
	// fills nothing (every field already set), creates nothing new.
	res2, err := vcard.CommitImport(ctx, db, wsID, "tester", []byte(vcardOne), "EG")
	if err != nil {
		t.Fatalf("second commit (same file): %v", err)
	}
	if res2.CreatedCount != 0 || res2.MergedCount != 1 || res2.ContactIDs[0] != contactID {
		t.Fatalf("re-import should be idempotent: %+v", res2)
	}

	// A second export of the same person with a new phone number: merges
	// into the same contact, appends the new number, never duplicates it.
	res3, err := vcard.CommitImport(ctx, db, wsID, "tester", []byte(vcardOneAgain), "EG")
	if err != nil {
		t.Fatalf("third commit (extra phone): %v", err)
	}
	if res3.CreatedCount != 0 || res3.MergedCount != 1 || res3.ContactIDs[0] != contactID {
		t.Fatalf("adding a phone should merge into the same contact: %+v", res3)
	}

	var email, phone, name string
	if err := db.QueryRowContext(ctx, `SELECT name, email, phone FROM contacts WHERE id = $1`, contactID).Scan(&name, &email, &phone); err != nil {
		t.Fatalf("read back contact: %v", err)
	}
	if name != "Sara Kamal" || email != "sara.kamal@example.test" || phone == "" {
		t.Fatalf("contact fields = name=%q email=%q phone=%q", name, email, phone)
	}

	var identityCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM contact_identities WHERE workspace_id=$1 AND contact_id=$2 AND channel='phone'`, wsID, contactID).Scan(&identityCount); err != nil {
		t.Fatalf("count phone identities: %v", err)
	}
	if identityCount < 1 {
		t.Fatalf("want at least one extra phone identity recorded, got %d", identityCount)
	}
}

func TestCommitRecordsImportBookkeeping(t *testing.T) {
	db, owner := appDB(t)
	wsID := workspaceIn(t, owner)
	ctx := withWS(context.Background(), db, wsID)

	if _, err := vcard.CommitImport(ctx, db, wsID, "tester", []byte(vcardOne), "EG"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM vcard_imports WHERE workspace_id=$1`, wsID).Scan(&count); err != nil {
		t.Fatalf("count vcard_imports: %v", err)
	}
	if count != 1 {
		t.Fatalf("want one vcard_imports row, got %d", count)
	}
}

func TestExportRoundTripsThroughDatabase(t *testing.T) {
	db, owner := appDB(t)
	wsID := workspaceIn(t, owner)
	ctx := withWS(context.Background(), db, wsID)

	res, err := vcard.CommitImport(ctx, db, wsID, "tester", []byte(vcardOne), "EG")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}

	text, err := vcard.ExportVCard(ctx, db, wsID, []string{res.ContactIDs[0]})
	if err != nil {
		t.Fatalf("ExportVCard: %v", err)
	}
	if !strings.Contains(text, "BEGIN:VCARD") || !strings.Contains(text, "Sara Kamal") || !strings.Contains(text, "sara.kamal@example.test") {
		t.Fatalf("exported text missing expected fields: %s", text)
	}

	cards, errs := vcard.ParseCards([]byte(text))
	if len(errs) != 0 {
		t.Fatalf("re-parsing the export failed: %v", errs)
	}
	if len(cards) != 1 || cards[0].FN != "Sara Kamal" {
		t.Fatalf("round-tripped card = %+v", cards)
	}
}

func TestLocalPhoneWithNoDefaultCountryIsAWarningNotAFailure(t *testing.T) {
	db, owner := appDB(t)
	wsID := workspaceIn(t, owner)
	ctx := withWS(context.Background(), db, wsID)
	// Overwrite the workspace's default_country to empty, so the local
	// number in vcardOne has nowhere to resolve from.
	if _, err := owner.ExecContext(ctx, `UPDATE workspaces SET default_country = '' WHERE id = $1`, wsID); err != nil {
		t.Fatalf("clear default_country: %v", err)
	}

	pv, err := vcard.PreviewImport(ctx, db, wsID, []byte(vcardOne), "")
	if err != nil {
		t.Fatalf("PreviewImport: %v", err)
	}
	if pv.NewCount != 1 {
		t.Fatalf("a card with a UID and email should still preview as new even if its phone cannot be resolved: %+v", pv)
	}
}
