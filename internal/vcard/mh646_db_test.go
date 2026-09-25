package vcard_test

/*
MH-646: Google-imported contacts kept a name but lost a local-format phone
number, because ToE164 dropped it outright when the workspace had no
default_country. These tests cover the two halves of the fix against a real
database: InferDefaultCountry/ResolveCountry's deterministic guess, and
classify/CommitCards keeping the raw number in meta (phone_raw/phones_raw)
so ReparseRawPhones can recover it once a country is known — plus that the
recovery is idempotent and never overwrites a phone a member already set.

	TEST_DATABASE_URL=postgres://postgres:…@localhost:55432/moharrik_tak?sslmode=disable \
	TEST_APP_DATABASE_URL=postgres://moharrik_app:…@localhost:55432/moharrik_tak?sslmode=disable \
	go test -count=1 ./internal/vcard/... -run MH646 -v
*/

import (
	"context"
	"testing"

	"github.com/fadymondy/moharrik/internal/vcard"
)

// vcardLocalOnly has no UID/email so it keys purely on the phone once
// resolved — exercising the "kept raw, not dropped" path end to end.
const vcardLocalOnly = "BEGIN:VCARD\r\n" +
	"VERSION:3.0\r\n" +
	"FN:Nabil Hassan\r\n" +
	"N:Hassan;Nabil;;;\r\n" +
	"TEL;TYPE=CELL:01005556666\r\n" +
	"EMAIL:nabil.hassan@example.test\r\n" +
	"END:VCARD\r\n"

func TestMH646CommitKeepsRawPhoneAndReparseFillsItInOnceCountrySet(t *testing.T) {
	db, owner := appDB(t)
	wsID := workspaceIn(t, owner)
	ctx := withWS(context.Background(), db, wsID)
	// No default_country and no override: the local TEL cannot resolve.
	if _, err := owner.ExecContext(ctx, `UPDATE workspaces SET default_country = '' WHERE id = $1`, wsID); err != nil {
		t.Fatalf("clear default_country: %v", err)
	}

	res, err := vcard.CommitImport(ctx, db, wsID, "tester", []byte(vcardLocalOnly), "")
	if err != nil {
		t.Fatalf("CommitImport: %v", err)
	}
	if res.CreatedCount != 1 || res.NeedsCountryCount != 1 {
		t.Fatalf("commit = %+v, want 1 created and 1 needing a country", res)
	}
	contactID := res.ContactIDs[0]

	var phone, rawPhone string
	if err := db.QueryRowContext(ctx, `SELECT phone, coalesce(meta->>'phone_raw', '') FROM contacts WHERE id = $1`, contactID).
		Scan(&phone, &rawPhone); err != nil {
		t.Fatalf("read back contact: %v", err)
	}
	if phone != "" {
		t.Fatalf("phone must stay blank while the country is unknown, got %q", phone)
	}
	if rawPhone != "01005556666" {
		t.Fatalf("meta.phone_raw = %q, want the raw TEL value kept", rawPhone)
	}

	// The workspace gets a default_country; re-parsing must now fill the
	// phone in without a second import.
	if _, err := owner.ExecContext(ctx, `UPDATE workspaces SET default_country = 'EG' WHERE id = $1`, wsID); err != nil {
		t.Fatalf("set default_country: %v", err)
	}
	resolved, err := vcard.ReparseRawPhones(ctx, db, wsID, "EG")
	if err != nil {
		t.Fatalf("ReparseRawPhones: %v", err)
	}
	if resolved != 1 {
		t.Fatalf("resolved = %d, want 1", resolved)
	}
	if err := db.QueryRowContext(ctx, `SELECT phone, coalesce(meta->>'phone_raw', '') FROM contacts WHERE id = $1`, contactID).
		Scan(&phone, &rawPhone); err != nil {
		t.Fatalf("read back contact after reparse: %v", err)
	}
	if phone == "" || rawPhone != "" {
		t.Fatalf("after reparse: phone=%q meta.phone_raw=%q, want phone filled and phone_raw cleared", phone, rawPhone)
	}

	// Cheap to run again: nothing left to resolve, so it is a no-op.
	resolvedAgain, err := vcard.ReparseRawPhones(ctx, db, wsID, "EG")
	if err != nil {
		t.Fatalf("ReparseRawPhones (second run): %v", err)
	}
	if resolvedAgain != 0 {
		t.Fatalf("second run resolved = %d, want 0 (nothing left raw)", resolvedAgain)
	}
}

// TestMH646ReparseNeverOverwritesAHandSetPhone: a member fills the phone in
// by hand before the country is set — the raw meta key is stale bookkeeping
// at that point and must never clobber what the member actually entered.
func TestMH646ReparseNeverOverwritesAHandSetPhone(t *testing.T) {
	db, owner := appDB(t)
	wsID := workspaceIn(t, owner)
	ctx := withWS(context.Background(), db, wsID)
	if _, err := owner.ExecContext(ctx, `UPDATE workspaces SET default_country = '' WHERE id = $1`, wsID); err != nil {
		t.Fatalf("clear default_country: %v", err)
	}

	res, err := vcard.CommitImport(ctx, db, wsID, "tester", []byte(vcardLocalOnly), "")
	if err != nil {
		t.Fatalf("CommitImport: %v", err)
	}
	contactID := res.ContactIDs[0]

	handSet := "+201099998888"
	if _, err := db.ExecContext(ctx, `UPDATE contacts SET phone = $2 WHERE id = $1`, contactID, handSet); err != nil {
		t.Fatalf("simulate a hand-set phone: %v", err)
	}

	if _, err := owner.ExecContext(ctx, `UPDATE workspaces SET default_country = 'EG' WHERE id = $1`, wsID); err != nil {
		t.Fatalf("set default_country: %v", err)
	}
	resolved, err := vcard.ReparseRawPhones(ctx, db, wsID, "EG")
	if err != nil {
		t.Fatalf("ReparseRawPhones: %v", err)
	}
	if resolved != 0 {
		t.Fatalf("resolved = %d, want 0 — the contact's phone is no longer blank", resolved)
	}
	var phone string
	if err := db.QueryRowContext(ctx, `SELECT phone FROM contacts WHERE id = $1`, contactID).Scan(&phone); err != nil {
		t.Fatal(err)
	}
	if phone != handSet {
		t.Fatalf("phone = %q, want the hand-set value %q left untouched", phone, handSet)
	}
}

func TestMH646InferDefaultCountryPicksMostCommonCallingCode(t *testing.T) {
	db, owner := appDB(t)
	wsID := workspaceIn(t, owner)
	ctx := withWS(context.Background(), db, wsID)
	if _, err := owner.ExecContext(ctx, `UPDATE workspaces SET default_country = '' WHERE id = $1`, wsID); err != nil {
		t.Fatalf("clear default_country: %v", err)
	}

	// No phones yet: nothing to infer from.
	got, err := vcard.InferDefaultCountry(ctx, db, wsID)
	if err != nil {
		t.Fatalf("InferDefaultCountry (empty): %v", err)
	}
	if got != "" {
		t.Fatalf("InferDefaultCountry with no phones = %q, want empty", got)
	}

	// Two EG numbers, one SA number: EG wins.
	seed := []string{"+201001112222", "+201003334444", "+966501112222"}
	for i, p := range seed {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO contacts (workspace_id, name, phone) VALUES ($1, $2, $3)`,
			wsID, "Seed", p); err != nil {
			t.Fatalf("seed contact %d: %v", i, err)
		}
	}
	got, err = vcard.InferDefaultCountry(ctx, db, wsID)
	if err != nil {
		t.Fatalf("InferDefaultCountry: %v", err)
	}
	if got != "EG" {
		t.Fatalf("InferDefaultCountry = %q, want EG (2 EG phones vs 1 SA)", got)
	}
}

func TestMH646ResolveCountryPrefersOverrideThenStoredThenInferred(t *testing.T) {
	db, owner := appDB(t)
	wsID := workspaceIn(t, owner) // default_country = 'EG' per workspaceIn
	ctx := withWS(context.Background(), db, wsID)

	// Stored default_country wins over inference.
	got, err := vcard.ResolveCountry(ctx, db, wsID, "")
	if err != nil {
		t.Fatalf("ResolveCountry (stored): %v", err)
	}
	if got != "EG" {
		t.Fatalf("ResolveCountry with a stored default = %q, want EG", got)
	}

	// An explicit override beats the stored value.
	got, err = vcard.ResolveCountry(ctx, db, wsID, "sa")
	if err != nil {
		t.Fatalf("ResolveCountry (override): %v", err)
	}
	if got != "SA" {
		t.Fatalf("ResolveCountry with override = %q, want SA (upper-cased)", got)
	}

	// Cleared default_country falls back to inference from existing phones.
	if _, err := owner.ExecContext(ctx, `UPDATE workspaces SET default_country = '' WHERE id = $1`, wsID); err != nil {
		t.Fatalf("clear default_country: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO contacts (workspace_id, name, phone) VALUES ($1, 'Seed', '+201001112222')`, wsID); err != nil {
		t.Fatalf("seed contact: %v", err)
	}
	got, err = vcard.ResolveCountry(ctx, db, wsID, "")
	if err != nil {
		t.Fatalf("ResolveCountry (inferred): %v", err)
	}
	if got != "EG" {
		t.Fatalf("ResolveCountry with no stored default = %q, want EG inferred", got)
	}
}
