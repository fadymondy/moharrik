package contactcontext

import (
	"os"
	"strings"
	"testing"
	"time"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

// TestParseAndroid checks message count, sender identification, timestamps
// and multi-line assembly against the Android fixture (MH-586): 24h clock,
// dd/mm/yy dates, Arabic text, a multi-line message, a media-omitted line and
// a system line.
func TestParseAndroid(t *testing.T) {
	data := readFixture(t, "android_export.txt")
	p, err := ParseText("android_export.txt", data)
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	if !p.DayFirst {
		t.Fatalf("expected day-first detection (13/03/24 appears), got month-first")
	}
	if len(p.Messages) != 8 {
		t.Fatalf("expected 8 messages, got %d: %+v", len(p.Messages), p.Messages)
	}
	if !p.Messages[0].System {
		t.Fatalf("first message should be the encryption system notice, got %+v", p.Messages[0])
	}

	senders := p.Senders()
	if len(senders) != 2 {
		t.Fatalf("expected 2 senders, got %v", senders)
	}
	if p.IsGroupOrAmbiguous() {
		t.Fatalf("a clean 2-party chat must not be flagged ambiguous")
	}

	// The multi-line message: "Here is the full plan..." + three continuation
	// lines with no timestamp prefix.
	var plan *Message
	for i := range p.Messages {
		if strings.HasPrefix(p.Messages[i].Text, "Here is the full plan") {
			plan = &p.Messages[i]
		}
	}
	if plan == nil {
		t.Fatalf("multi-line message not found")
	}
	if got := strings.Count(plan.Text, "\n"); got != 3 {
		t.Fatalf("expected 3 continuation lines folded in, got %d newlines in %q", got, plan.Text)
	}
	if !strings.Contains(plan.Text, "Day 3: send proposal") {
		t.Fatalf("last continuation line missing: %q", plan.Text)
	}

	// Media-omitted lines.
	mediaCount := 0
	for _, m := range p.Messages {
		if m.MediaOnly {
			mediaCount++
		}
	}
	if mediaCount != 2 {
		t.Fatalf("expected 2 media-omitted messages, got %d", mediaCount)
	}

	// Arabic text survives untouched.
	found := false
	for _, m := range p.Messages {
		if strings.Contains(m.Text, "ممتاز") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Arabic message text was not preserved")
	}

	// Timestamps: 12/03/24 day-first => 12 March 2024.
	first := p.Messages[0].Time
	want := time.Date(2024, time.March, 12, 9, 15, 0, 0, time.UTC)
	if !first.Equal(want) {
		t.Fatalf("first timestamp = %v, want %v", first, want)
	}
	last := p.Messages[len(p.Messages)-1].Time
	wantLast := time.Date(2024, time.March, 13, 14, 2, 0, 0, time.UTC)
	if !last.Equal(wantLast) {
		t.Fatalf("last timestamp = %v, want %v", last, wantLast)
	}
}

// TestParseIOS checks the bracketed iOS format: 12h AM/PM, m/d/yy dates,
// U+200E marks in the raw file, and an <attached: ...> line.
func TestParseIOS(t *testing.T) {
	data := readFixture(t, "ios_export.txt")
	if !strings.Contains(string(data), "‎") {
		t.Fatalf("fixture is supposed to carry U+200E marks")
	}
	p, err := ParseText("ios_export.txt", data)
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	if p.DayFirst {
		t.Fatalf("iOS fixture uses m/d/yy: expected month-first detection")
	}
	if len(p.Messages) != 6 {
		t.Fatalf("expected 6 messages, got %d: %+v", len(p.Messages), p.Messages)
	}
	for _, m := range p.Messages {
		if strings.ContainsRune(m.Text, '‎') || strings.ContainsRune(m.Sender, '‎') {
			t.Fatalf("U+200E leaked into parsed text/sender: %+v", m)
		}
	}

	var attached *Message
	for i := range p.Messages {
		if p.Messages[i].MediaFile != "" {
			attached = &p.Messages[i]
		}
	}
	if attached == nil {
		t.Fatalf("expected one <attached: ...> message")
	}
	if attached.MediaFile != "00000012-PHOTO.jpg" {
		t.Fatalf("media filename = %q, want 00000012-PHOTO.jpg", attached.MediaFile)
	}
	if attached.Sender != "Fady Mondy" {
		t.Fatalf("attached-media message sender = %q, want Fady Mondy", attached.Sender)
	}

	// 3/12/24 month-first => 12 March 2024, 9:15:00 AM.
	first := p.Messages[0].Time
	want := time.Date(2024, time.March, 12, 9, 15, 0, 0, time.UTC)
	if !first.Equal(want) {
		t.Fatalf("first timestamp = %v, want %v", first, want)
	}
	// 6:45:30 PM on the last line.
	last := p.Messages[len(p.Messages)-1].Time
	if last.Hour() != 18 || last.Minute() != 45 {
		t.Fatalf("PM conversion wrong: %v", last)
	}
}

func TestParseArchive(t *testing.T) {
	data := readFixture(t, "whatsapp_export.zip")
	p, err := ParseArchive("whatsapp_export.zip", data)
	if err != nil {
		t.Fatalf("ParseArchive: %v", err)
	}
	if len(p.Messages) != 8 {
		t.Fatalf("expected 8 messages from the bundled txt, got %d", len(p.Messages))
	}
	if len(p.Media) != 1 {
		t.Fatalf("expected 1 media file in the zip, got %d", len(p.Media))
	}
	if _, ok := p.Media["00000012-PHOTO.jpg"]; !ok {
		t.Fatalf("expected 00000012-PHOTO.jpg in extracted media, got keys %v", keysOf(p.Media))
	}
}

func TestParseUploadDispatch(t *testing.T) {
	zipData := readFixture(t, "whatsapp_export.zip")
	p, err := ParseUpload("whatsapp_export.zip", zipData)
	if err != nil {
		t.Fatalf("ParseUpload(zip): %v", err)
	}
	if len(p.Media) != 1 {
		t.Fatalf("expected media from zip dispatch")
	}

	txtData := readFixture(t, "android_export.txt")
	p2, err := ParseUpload("android_export.txt", txtData)
	if err != nil {
		t.Fatalf("ParseUpload(txt): %v", err)
	}
	if len(p2.Messages) != 8 {
		t.Fatalf("expected 8 messages from txt dispatch, got %d", len(p2.Messages))
	}
}

func TestGuessContactSender(t *testing.T) {
	data := readFixture(t, "android_export.txt")
	p, _ := ParseText("android_export.txt", data)
	guess, ok := p.GuessContactSender("sara ali")
	if !ok || guess != "Sara Ali" {
		t.Fatalf("GuessContactSender = %q, %v; want Sara Ali, true", guess, ok)
	}
	if _, ok := p.GuessContactSender("Someone Else"); ok {
		t.Fatalf("should not guess a sender that is not in the chat")
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
