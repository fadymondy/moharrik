package inbox

import (
	"strings"
	"testing"
	"time"
)

const plainMail = "From: Dana Customer <Dana@Example.TEST>\r\n" +
	"To: support@shop.test\r\n" +
	"Subject: =?utf-8?B?2LPYpNin2YQ=?= about my invoice\r\n" +
	"Message-Id: <first@example.test>\r\n" +
	"Date: Tue, 15 Sep 2026 09:30:00 +0000\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"My invoice says 300 but I paid 200.\r\n\r\n\r\n\r\nThanks,\r\nDana\r\n"

const htmlOnlyMail = "From: dana@example.test\r\n" +
	"Subject: Re: about my invoice\r\n" +
	"Message-Id: <second@example.test>\r\n" +
	"In-Reply-To: <ours@shop.test>\r\n" +
	"References: <first@example.test>\r\n\t<ours@shop.test>\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n" +
	"\r\n" +
	"<html><body><p>Still <b>wrong</b> &amp; unpaid</p></body></html>\r\n"

func TestParseEmailDecodesHeadersAndBody(t *testing.T) {
	e, err := ParseEmail(strings.NewReader(plainMail))
	if err != nil {
		t.Fatal(err)
	}
	if e.MessageID != "first@example.test" {
		t.Errorf("message id = %q — the angle brackets must be gone, it is compared exactly", e.MessageID)
	}
	if e.From != "dana@example.test" {
		t.Errorf("from = %q — the address is lowercased so matching is case-insensitive", e.From)
	}
	if e.FromName != "Dana Customer" {
		t.Errorf("from name = %q", e.FromName)
	}
	if !strings.HasPrefix(e.Subject, "سؤال") {
		t.Errorf("subject = %q — RFC 2047 should be decoded", e.Subject)
	}
	if !strings.Contains(e.Body, "300 but I paid 200") {
		t.Errorf("body = %q", e.Body)
	}
	if strings.Contains(e.Body, "\n\n\n") {
		t.Errorf("runs of blank lines should be collapsed: %q", e.Body)
	}
	if !e.Date.Equal(time.Date(2026, 9, 15, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("date = %s", e.Date)
	}
}

func TestParseEmailFallsBackToHTML(t *testing.T) {
	e, err := ParseEmail(strings.NewReader(htmlOnlyMail))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.Body, "<b>") || !strings.Contains(e.Body, "Still") {
		t.Errorf("html should be stripped to text, got %q", e.Body)
	}
	if !strings.Contains(e.Body, "&") || strings.Contains(e.Body, "&amp;") {
		t.Errorf("entities should be decoded, got %q", e.Body)
	}
	// Folded References must both be read.
	if len(e.References) != 2 || e.References[0] != "first@example.test" || e.References[1] != "ours@shop.test" {
		t.Errorf("references = %v — a folded header spans lines", e.References)
	}
}

func TestParseEmailSurvivesAMessageWithNothingInIt(t *testing.T) {
	// No Message-ID, no Date, no content type. Refusing this would mean
	// silently dropping a customer's email.
	e, err := ParseEmail(strings.NewReader("From: nobody@example.test\r\nSubject: hi\r\n\r\nbody\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if e.MessageID != "" {
		t.Errorf("message id = %q, want empty", e.MessageID)
	}
	if e.Date.IsZero() {
		t.Error("a message with no date must be dated now, not 1970")
	}
	if e.Body != "body" {
		t.Errorf("body = %q", e.Body)
	}
}

func TestThreadKeysPreferTheDirectParent(t *testing.T) {
	e := &Email{
		InReplyTo:  "parent@shop.test",
		References: []string{"root@example.test", "middle@shop.test", "parent@shop.test"},
	}
	keys := e.ThreadKeys()
	if len(keys) != 3 {
		t.Fatalf("keys = %v — the duplicate of In-Reply-To must not be repeated", keys)
	}
	if keys[0] != "parent@shop.test" {
		t.Errorf("first key = %q, want the In-Reply-To", keys[0])
	}
	// References is walked backwards: the nearest ancestor is its LAST entry,
	// and trying the root first would attach a reply to the wrong thread when
	// two threads share a root.
	if keys[1] != "middle@shop.test" || keys[2] != "root@example.test" {
		t.Errorf("keys = %v, want nearest ancestor first", keys)
	}

	if got := (&Email{}).ThreadKeys(); len(got) != 0 {
		t.Errorf("an email claiming nothing should thread on nothing, got %v", got)
	}
}

func TestNormalisedSubjectStripsReplyPrefixes(t *testing.T) {
	for in, want := range map[string]string{
		"Re: Fwd: Invoice #12":    "invoice #12",
		"RE:  invoice #12":        "invoice #12",
		"رد: Invoice #12":         "invoice #12",
		"Invoice   #12":           "invoice #12",
		"Fw: Re: Re: Invoice #12": "invoice #12",
		"":                        "",
	} {
		if got := NormalisedSubject(in); got != want {
			t.Errorf("NormalisedSubject(%q) = %q, want %q", in, got, want)
		}
	}
	// It must NOT eat a subject that merely starts with those letters.
	if got := NormalisedSubject("Refund request"); got != "refund request" {
		t.Errorf("got %q — 'Refund' is not a reply prefix", got)
	}
}

func TestReplyHeadersThreadOnTheCustomersSide(t *testing.T) {
	h := ReplyHeaders("theirs@example.test", []string{"root@example.test"})
	if h["In-Reply-To"] != "<theirs@example.test>" {
		t.Errorf("in-reply-to = %q", h["In-Reply-To"])
	}
	if h["References"] != "<root@example.test> <theirs@example.test>" {
		t.Errorf("references = %q — the message being answered goes last", h["References"])
	}
	// Already present: not added twice.
	h = ReplyHeaders("a@x", []string{"a@x"})
	if h["References"] != "<a@x>" {
		t.Errorf("references = %q", h["References"])
	}
	if ReplyHeaders("", nil) != nil {
		t.Error("with nothing to answer, a reply carries no threading headers")
	}
}
