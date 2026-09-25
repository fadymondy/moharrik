package vcard

import (
	"strings"
	"testing"
)

// fixtureAndroid21 mimics an actual Android 2.1 contacts export: CRLF lines,
// QUOTED-PRINTABLE + CHARSET=UTF-8 on FN/N (the classic mangled-Arabic-name
// bug this app has to get right), and a bare TYPE token (2.1 has no "TYPE=").
const fixtureAndroid21 = "BEGIN:VCARD\r\n" +
	"VERSION:2.1\r\n" +
	"N;ENCODING=QUOTED-PRINTABLE;CHARSET=UTF-8:=D9=85=D9=86=D8=AF=D9=8A;=D9=81=D8=A7=D8=AF=D9=8A;;;\r\n" +
	"FN;ENCODING=QUOTED-PRINTABLE;CHARSET=UTF-8:=D9=81=D8=A7=D8=AF=D9=8A =D9=85=D9=86=D8=AF=D9=8A\r\n" +
	"TEL;CELL:01001234567\r\n" +
	"EMAIL;INTERNET:fady@example.test\r\n" +
	"END:VCARD\r\n"

// fixtureIPhone3 mimics an iPhone 3.0 export: Apple's item-group
// X-ABLabel convention on a TEL, and a small embedded PHOTO.
const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

var fixtureIPhone3 = "BEGIN:VCARD\r\n" +
	"VERSION:3.0\r\n" +
	"N:Hassan;Laila;;;\r\n" +
	"FN:Laila Hassan\r\n" +
	"item1.TEL:+201098765432\r\n" +
	"item1.X-ABLabel:_$!<Mobile>!$_\r\n" +
	"EMAIL;type=INTERNET;type=WORK;type=pref:laila@example.test\r\n" +
	"PHOTO;ENCODING=b;TYPE=PNG:" + tinyPNGBase64 + "\r\n" +
	"UID:iphone-laila-0001\r\n" +
	"END:VCARD\r\n"

// fixtureGoogle3 mimics a Google Contacts 3.0 export: ORG, TITLE, multiple
// TEL/ADR with TYPE lists, CATEGORIES and a NOTE.
const fixtureGoogle3 = "BEGIN:VCARD\r\n" +
	"VERSION:3.0\r\n" +
	"N:Fathy;Rana;;;\r\n" +
	"FN:Rana Fathy\r\n" +
	"ORG:Acme Trading;Sales\r\n" +
	"TITLE:Account Manager\r\n" +
	"TEL;TYPE=CELL,PREF:+201015550101\r\n" +
	"TEL;TYPE=HOME:+201028889999\r\n" +
	"EMAIL;TYPE=WORK:rana@example.test\r\n" +
	"ADR;TYPE=WORK:;;12 Tahrir St;Cairo;Cairo Governorate;11511;Egypt\r\n" +
	"CATEGORIES:Customer,VIP\r\n" +
	"NOTE:Met at the Cairo trade fair.\r\n" +
	"BDAY:1990-05-12\r\n" +
	"END:VCARD\r\n"

// fixtureOutlook21 mimics an Outlook 2.1 export: folded lines (leading
// space continuation) and a bare "WORK" TYPE token.
const fixtureOutlook21 = "BEGIN:VCARD\r\n" +
	"VERSION:2.1\r\n" +
	"N:Mondy;Fady;;;\r\n" +
	"FN:Fady Mondy\r\n" +
	"TEL;WORK:+20223456789\r\n" +
	"EMAIL;WORK:fady.mondy\r\n" +
	" @example.test\r\n" +
	"NOTE:A note that was\r\n" +
	"  folded across two physical lines by Outlook.\r\n" +
	"END:VCARD\r\n"

func TestParseAndroid21QuotedPrintableArabic(t *testing.T) {
	cards, errs := ParseCards([]byte(fixtureAndroid21))
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	if len(cards) != 1 {
		t.Fatalf("want 1 card, got %d", len(cards))
	}
	c := cards[0]
	if c.FN != "فادي مندي" {
		t.Errorf("FN = %q, want فادي مندي", c.FN)
	}
	if c.N.Family != "مندي" || c.N.Given != "فادي" {
		t.Errorf("N = %+v, want family=مندي given=فادي", c.N)
	}
	if len(c.Phones) != 1 || c.Phones[0].Value != "01001234567" {
		t.Errorf("Phones = %+v", c.Phones)
	}
	if len(c.Emails) != 1 || c.Emails[0].Value != "fady@example.test" {
		t.Errorf("Emails = %+v", c.Emails)
	}
}

func TestParseIPhone3ItemLabelsAndPhoto(t *testing.T) {
	cards, errs := ParseCards([]byte(fixtureIPhone3))
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	if len(cards) != 1 {
		t.Fatalf("want 1 card, got %d", len(cards))
	}
	c := cards[0]
	if c.FN != "Laila Hassan" {
		t.Errorf("FN = %q", c.FN)
	}
	if len(c.Phones) != 1 || c.Phones[0].Value != "+201098765432" || c.Phones[0].Label != "Mobile" {
		t.Errorf("Phones = %+v, want value=+201098765432 label=Mobile", c.Phones)
	}
	if len(c.Emails) != 1 || !c.Emails[0].Preferred {
		t.Errorf("Emails = %+v, want a preferred email", c.Emails)
	}
	if !strings.HasPrefix(c.PhotoURL, "data:image/png;base64,") {
		t.Errorf("PhotoURL = %q, want a data:image/png;base64 URI", c.PhotoURL)
	}
	if c.UID != "iphone-laila-0001" {
		t.Errorf("UID = %q", c.UID)
	}
}

func TestParseGoogle3(t *testing.T) {
	cards, errs := ParseCards([]byte(fixtureGoogle3))
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	if len(cards) != 1 {
		t.Fatalf("want 1 card, got %d", len(cards))
	}
	c := cards[0]
	if c.Org != "Acme Trading" {
		t.Errorf("Org = %q", c.Org)
	}
	if c.Title != "Account Manager" {
		t.Errorf("Title = %q", c.Title)
	}
	if len(c.Phones) != 2 {
		t.Fatalf("want 2 phones, got %d: %+v", len(c.Phones), c.Phones)
	}
	if !c.Phones[0].Preferred {
		t.Errorf("first TEL should be preferred: %+v", c.Phones[0])
	}
	if len(c.Addresses) != 1 || c.Addresses[0].Street != "12 Tahrir St" || c.Addresses[0].Country != "Egypt" {
		t.Errorf("Addresses = %+v", c.Addresses)
	}
	if len(c.Categories) != 2 || c.Categories[0] != "Customer" || c.Categories[1] != "VIP" {
		t.Errorf("Categories = %+v", c.Categories)
	}
	if c.Note != "Met at the Cairo trade fair." {
		t.Errorf("Note = %q", c.Note)
	}
	if c.BDay != "1990-05-12" {
		t.Errorf("BDay = %q", c.BDay)
	}
}

func TestParseOutlook21Folding(t *testing.T) {
	cards, errs := ParseCards([]byte(fixtureOutlook21))
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	if len(cards) != 1 {
		t.Fatalf("want 1 card, got %d", len(cards))
	}
	c := cards[0]
	if c.FN != "Fady Mondy" {
		t.Errorf("FN = %q", c.FN)
	}
	if len(c.Emails) != 1 || c.Emails[0].Value != "fady.mondy@example.test" {
		t.Errorf("Emails = %+v, want the folded line rejoined", c.Emails)
	}
	if c.Note != "A note that was folded across two physical lines by Outlook." {
		t.Errorf("Note = %q, want the fold rejoined with the leading space stripped", c.Note)
	}
}

func TestParseMultipleCardsInOneFile(t *testing.T) {
	all := fixtureAndroid21 + fixtureIPhone3 + fixtureGoogle3 + fixtureOutlook21
	cards, errs := ParseCards([]byte(all))
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	if len(cards) != 4 {
		t.Fatalf("want 4 cards, got %d", len(cards))
	}
}

func TestEscapedCommaSemicolonNewline(t *testing.T) {
	data := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:A\\, B\\; C\r\nNOTE:line one\\nline two\r\nCATEGORIES:Tag A\\, still one tag\r\nEND:VCARD\r\n"
	cards, errs := ParseCards([]byte(data))
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	c := cards[0]
	if c.FN != "A, B; C" {
		t.Errorf("FN = %q", c.FN)
	}
	if c.Note != "line one\nline two" {
		t.Errorf("Note = %q", c.Note)
	}
	if len(c.Categories) != 1 || c.Categories[0] != "Tag A, still one tag" {
		t.Errorf("Categories = %+v, want one tag with an escaped comma preserved", c.Categories)
	}
}

func TestUnknownXPropKeptInMeta(t *testing.T) {
	data := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Test\r\nX-SKYPE:fady.mondy\r\nEND:VCARD\r\n"
	cards, _ := ParseCards([]byte(data))
	if cards[0].XProps["skype"] != "fady.mondy" {
		t.Errorf("XProps = %+v, want skype=fady.mondy", cards[0].XProps)
	}
}

func TestToE164(t *testing.T) {
	tests := []struct {
		raw, country, want string
		wasLocal, wantErr  bool
	}{
		{"+201015550101", "", "+201015550101", false, false},
		{"00201015550101", "", "+201015550101", false, false},
		{"01015550101", "EG", "+201015550101", true, false},
		{"01015550101", "", "", true, true},
		{"01015550101", "ZZ", "", true, true},
	}
	for _, tt := range tests {
		got, wasLocal, err := ToE164(tt.raw, tt.country)
		if (err != nil) != tt.wantErr {
			t.Errorf("ToE164(%q,%q) err=%v, wantErr=%v", tt.raw, tt.country, err, tt.wantErr)
			continue
		}
		if err != nil {
			continue
		}
		if got != tt.want || wasLocal != tt.wasLocal {
			t.Errorf("ToE164(%q,%q) = (%q,%v), want (%q,%v)", tt.raw, tt.country, got, wasLocal, tt.want, tt.wasLocal)
		}
	}
}

func TestExportRoundTrip(t *testing.T) {
	// ExportVCard needs a DB, so this only checks that the writer's output
	// re-parses into the fields it wrote — the format-level half of "round
	// trip safe with the importer" that does not need a database.
	var out strings.Builder
	writeCard(&out, exportRow{
		name: "Rana Fathy", givenName: "Rana", familyName: "Fathy",
		email: "rana@example.test", phone: "+201015550101",
		companyName: "Acme Trading", title: "Account Manager",
		tags: []string{"Customer", "VIP"},
	})
	cards, errs := ParseCards([]byte(out.String()))
	if len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	if len(cards) != 1 {
		t.Fatalf("want 1 card, got %d", len(cards))
	}
	c := cards[0]
	if c.FN != "Rana Fathy" || c.Emails[0].Value != "rana@example.test" || c.Phones[0].Value != "+201015550101" {
		t.Errorf("round-tripped card = %+v", c)
	}
	if c.Org != "Acme Trading" || c.Title != "Account Manager" {
		t.Errorf("round-tripped Org/Title = %q/%q", c.Org, c.Title)
	}
	if len(c.Categories) != 2 {
		t.Errorf("round-tripped Categories = %+v", c.Categories)
	}
}
