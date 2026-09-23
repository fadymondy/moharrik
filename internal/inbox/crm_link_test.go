package inbox

import (
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // the zone below exists on every machine the tests run on
)

func TestReplyLanguageFollowsTheCustomerNotTheMember(t *testing.T) {
	cases := []struct {
		name                                 string
		chatLocale, lastInbound, contact, ws string
		want                                 string
	}{
		{"the widget ran in Arabic", "ar-EG", "hello", "en", "en", "ar"},
		{"the widget ran in English, whatever they typed", "en-GB", "مرحبا", "ar", "ar", "en"},
		{"no widget language: the script they wrote in", "", "مرحبا، أريد طلبية", "en", "en", "ar"},
		{"Latin script", "", "Hello there", "ar", "ar", "en"},
		{"no letters: the contact's language", "", "+20 100 000 0000 👍", "ar", "en", "ar"},
		{"nothing at all: the workspace's", "", "", "", "ar", "ar"},
		{"and English as the last resort", "", "", "", "", "en"},
	}
	for _, c := range cases {
		if got := replyLanguage(c.chatLocale, c.lastInbound, c.contact, c.ws); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestGuessVisitorReadsWhatTheyWrote(t *testing.T) {
	msgs := []MessageOut{
		{Direction: "in", Body: "Hi, I want a quote"},
		{Direction: "out", Body: "Sure, what is your email? ours is sales@us.test, call +1 555 000 1111"},
		{Direction: "in", Body: "It's Sara.K@Example.test — or call ٠١٠١٢٣٤٥٦٧٨"},
	}
	g := guessVisitor(ConversationOut{CustomerName: "Sara"}, msgs, "en")
	if g.Email != "sara.k@example.test" {
		t.Errorf("email: %q (the team's own address must not be picked)", g.Email)
	}
	if g.Phone != "01012345678" {
		t.Errorf("phone written in Arabic-Indic digits: %q", g.Phone)
	}
	// What the conversation already holds wins, and a WhatsApp number gets its plus back.
	g = guessVisitor(ConversationOut{CustomerEmail: "known@x.test", CustomerPhone: "966512345678"}, msgs, "ar")
	if g.Email != "known@x.test" || g.Phone != "+966512345678" || g.Locale != "ar" {
		t.Errorf("conversation fields first: %+v", g)
	}
	// A short number is not a phone number.
	if p := phoneFrom("order 12345 please"); p != "" {
		t.Errorf("an order number is not a phone: %q", p)
	}
}

func TestTranscriptSaysWhoSaidWhatAndWhen(t *testing.T) {
	cairo, err := time.LoadLocation("Africa/Cairo")
	if err != nil {
		t.Fatal(err)
	}
	uid := "u1"
	at := time.Date(2026, 9, 23, 8, 5, 0, 0, time.UTC)
	c := ConversationOut{Channel: "chat", CustomerName: "Mona", CustomerEmail: "mona@x.test", PageURL: "https://shop.test/pricing"}
	msgs := []MessageOut{
		{Direction: "in", Body: "Hello\nsecond line", CreatedAt: at},
		{Direction: "out", AuthorUserID: &uid, Body: "Hi Mona", CreatedAt: at.Add(3 * time.Minute)},
		{Direction: "out", Body: "From the API", CreatedAt: at.Add(4 * time.Minute)},
	}
	got := Transcript(c, msgs, map[string]string{"u1": "owner@team.test"}, "en", cairo)
	for _, want := range []string{
		"Chat transcript with Mona (mona@x.test)",
		"Started on: https://shop.test/pricing",
		"Times are Africa/Cairo",
		"[2026-09-23 11:05] Mona:\nHello\nsecond line",
		"[2026-09-23 11:08] owner@team.test (team):\nHi Mona",
		"[2026-09-23 11:09] Team (team):\nFrom the API",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	ar := Transcript(c, msgs, nil, "ar", time.UTC)
	if !strings.Contains(ar, "نص محادثة الدردشة مع Mona") || !strings.Contains(ar, "الفريق (الفريق)") {
		t.Errorf("the Arabic transcript:\n%s", ar)
	}

	// A long thread is cut, saying how much is left out.
	long := make([]MessageOut, 400)
	for i := range long {
		long[i] = MessageOut{Direction: "in", Body: strings.Repeat("x", 100), CreatedAt: at}
	}
	cut := Transcript(c, long, nil, "en", time.UTC)
	if n := len([]rune(cut)); n > maxTranscript+200 || !strings.Contains(cut, "more messages — the whole thread is in the inbox") {
		t.Errorf("a long transcript should be cut with a note (%d runes)", n)
	}
}
