package vcard_test

import (
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/vcard"
)

// MH-585: ParseGoogleCSV against the Google CSV export column shape — BOM,
// multi-value cells, Arabic text, and custom fields. No database needed.

func TestParseGoogleCSVHeaderAndBasicRow(t *testing.T) {
	csv := "Name Prefix,First Name,Middle Name,Last Name,Name Suffix,Phonetic First Name,Phonetic Last Name," +
		"Nickname,File As,Birthday,Notes,Labels," +
		"E-mail 1 - Label,E-mail 1 - Value,Phone 1 - Label,Phone 1 - Value\n" +
		"Mr,Sam,,Malek,,,,\"Sammy\",Sam Malek,1990-05-02,\"vip client\",* myContacts ::: Friends," +
		"Work,sam@example.test,Mobile,01001112222\n"

	cards, errs := vcard.ParseGoogleCSV([]byte(csv))
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %+v", errs)
	}
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want 1", len(cards))
	}
	c := cards[0]
	if c.N.Given != "Sam" || c.N.Family != "Malek" || c.N.Prefix != "Mr" {
		t.Errorf("name: %+v", c.N)
	}
	if c.Nickname != "Sammy" || c.BDay != "1990-05-02" || c.Note != "vip client" {
		t.Errorf("scalar fields: nickname=%q bday=%q note=%q", c.Nickname, c.BDay, c.Note)
	}
	// "* myContacts" is Google's own pseudo-label and must be stripped; the
	// real "Friends" label, multi-value-joined onto the same cell, survives.
	if len(c.Categories) != 1 || c.Categories[0] != "Friends" {
		t.Errorf("categories: %v", c.Categories)
	}
	if len(c.Emails) != 1 || c.Emails[0].Value != "sam@example.test" || c.Emails[0].Label != "Work" {
		t.Errorf("emails: %+v", c.Emails)
	}
	if len(c.Phones) != 1 || c.Phones[0].Value != "01001112222" || c.Phones[0].Label != "Mobile" {
		t.Errorf("phones: %+v", c.Phones)
	}
	if c.ImportSource != "google_csv" {
		t.Errorf("import source: %q", c.ImportSource)
	}
}

func TestParseGoogleCSVStripsUTF8BOM(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	csv := string(bom) + "First Name,Last Name\nNora,Adel\n"
	cards, errs := vcard.ParseGoogleCSV([]byte(csv))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %+v", errs)
	}
	if len(cards) != 1 || cards[0].N.Given != "Nora" {
		t.Fatalf("BOM was not stripped cleanly: %+v", cards)
	}
}

func TestParseGoogleCSVArabicText(t *testing.T) {
	csv := "First Name,Last Name,Notes\nمحمد,علي,ملاحظة بالعربية\n"
	cards, errs := vcard.ParseGoogleCSV([]byte(csv))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %+v", errs)
	}
	if len(cards) != 1 {
		t.Fatalf("got %d cards", len(cards))
	}
	c := cards[0]
	if c.N.Given != "محمد" || c.N.Family != "علي" {
		t.Errorf("arabic name not preserved: %+v", c.N)
	}
	if !strings.Contains(c.Note, "العربية") {
		t.Errorf("arabic note not preserved: %q", c.Note)
	}
}

func TestParseGoogleCSVMultiValuePhonesAndEmails(t *testing.T) {
	csv := "First Name,Phone 1 - Label,Phone 1 - Value,E-mail 1 - Label,E-mail 1 - Value\n" +
		"Lina,Home,\"0223334444 ::: 0225556666\",Home,\"lina@home.test ::: lina@work.test\"\n"
	cards, errs := vcard.ParseGoogleCSV([]byte(csv))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %+v", errs)
	}
	c := cards[0]
	if len(c.Phones) != 2 || c.Phones[0].Value != "0223334444" || c.Phones[1].Value != "0225556666" {
		t.Errorf("phones did not split on ' ::: ': %+v", c.Phones)
	}
	if len(c.Emails) != 2 || c.Emails[0].Value != "lina@home.test" || c.Emails[1].Value != "lina@work.test" {
		t.Errorf("emails did not split on ' ::: ': %+v", c.Emails)
	}
}

func TestParseGoogleCSVCustomFields(t *testing.T) {
	csv := "First Name,Custom Field 1 - Label,Custom Field 1 - Value,Custom Field 2 - Label,Custom Field 2 - Value\n" +
		"Omar,Employee ID,EMP-4021,google_relations,not-really-a-relation\n"
	cards, errs := vcard.ParseGoogleCSV([]byte(csv))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %+v", errs)
	}
	c := cards[0]
	if c.Meta["Employee ID"] != "EMP-4021" {
		t.Errorf("custom field 1: %+v", c.Meta)
	}
	// A custom field label that collides with one of this app's own
	// "google_"-prefixed meta keys must be prefixed, not overwrite it.
	if c.Meta["custom_google_relations"] != "not-really-a-relation" {
		t.Errorf("collision-prefixed custom field: %+v", c.Meta)
	}
}

func TestParseGoogleCSVBlankRowSkipped(t *testing.T) {
	csv := "First Name,Last Name\nAda,Fahmy\n,\n\n"
	cards, errs := vcard.ParseGoogleCSV([]byte(csv))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %+v", errs)
	}
	if len(cards) != 1 {
		t.Fatalf("blank rows must be skipped, got %d cards", len(cards))
	}
}
