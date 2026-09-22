package marketing

import (
	"strings"
	"testing"
)

/*
The origin allow-list, which is the whole authorization of the public subscribe
endpoint once the key is known — and the key is embedded in a page anyone can
view, so this check is not a formality.
*/

func TestOriginAllowedDeniesByDefault(t *testing.T) {
	// A form with no origins set is CLOSED. This is the one that matters: a
	// newly created form must not accept submissions from anywhere until
	// somebody says where it lives.
	if originAllowed("", "https://example.com") {
		t.Fatal("an empty allow-list accepted an origin; a new form would be open to the whole web")
	}
	// A request with no Origin header at all — curl, a script, a server — is
	// not a browser on an allowed page.
	if originAllowed("https://example.com", "") {
		t.Fatal("a missing Origin was accepted")
	}
}

func TestOriginAllowedMatchesExactlyAndNotBySuffix(t *testing.T) {
	list := "https://example.com, https://www.example.com"

	for _, ok := range []string{
		"https://example.com",
		"https://www.example.com",
		"https://EXAMPLE.com",  // case is not significant in a host
		"https://example.com/", // a trailing slash is not a different origin
	} {
		if !originAllowed(list, ok) {
			t.Errorf("%q should be allowed", ok)
		}
	}

	for _, bad := range []string{
		// The classic bug in this kind of check: a textual suffix match would
		// let an attacker register notexample.com and post to somebody's list.
		"https://notexample.com",
		"https://example.com.evil.test",
		"https://evil.test",
		// A different scheme or port is a different origin, and http is not
		// https — a form on a page served over http is not the page allowed.
		"http://example.com",
		"https://example.com:8443",
		// A subdomain is NOT covered by its parent here. Unlike the browser
		// agents' host allow-list, which deliberately matches subdomains
		// because a site redirects between apex and www, an origin is exact.
		"https://blog.example.com",
	} {
		if originAllowed(list, bad) {
			t.Errorf("%q must not be allowed", bad)
		}
	}
}

func TestSplitOriginsTidiesWhatSomebodyPasted(t *testing.T) {
	got := splitOrigins(" https://a.test/ , ,https://b.test ,  ")
	want := []string{"https://a.test", "https://b.test"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	// An empty string is no origins, not one empty origin — otherwise the
	// allow-list would contain "" and match a missing Origin header.
	if len(splitOrigins("")) != 0 {
		t.Fatal("an empty list should produce no origins")
	}
}

func TestDefaultThanksNeverRevealsWhetherTheAddressWasAlreadyKnown(t *testing.T) {
	// The copy may differ by whether the LIST asks for confirmation — "check
	// your email" is wrong for a list that sends none. It must not differ by
	// whether THIS address was already on it, or the form becomes a way to
	// find out who is subscribed.
	newPending := defaultThanks("en", StatusPending)
	repeatPending := defaultThanks("en", StatusPending)
	if newPending != repeatPending {
		t.Fatal("the same status produced two different answers")
	}
	if defaultThanks("ar", StatusPending) == "" || defaultThanks("ar", StatusSubscribed) == "" {
		t.Fatal("Arabic copy is missing; the product is bilingual end to end")
	}
}

/*
Stop words (MH-320): the only way out of a WhatsApp broadcast.

Email carries a one-click unsubscribe header. WhatsApp has nothing of the kind,
so a reply is the whole mechanism — which makes these string comparisons the
thing standing between a customer and messages they cannot stop.
*/

func TestIsStopWordAcceptsBothLanguagesAndTidyPunctuation(t *testing.T) {
	for _, yes := range []string{
		"stop", "STOP", " Stop ", "stop.", "STOP!",
		"unsubscribe", "cancel", "quit", "end",
		"إيقاف", "ايقاف", "توقف", "إلغاء", "الغاء", " إيقاف ",
	} {
		if !IsStopWord(yes) {
			t.Errorf("%q should stop the messages", yes)
		}
	}
}

func TestIsStopWordDoesNotFireOnASentenceThatMerelyContainsIt(t *testing.T) {
	// Matched on the whole message, not as a substring. Somebody writing
	// "please don't stop sending these" must not be unsubscribed by it — and
	// the opposite mistake, a missed STOP, is caught by the test above.
	for _, no := range []string{
		"please don't stop sending these",
		"stop by the shop tomorrow",
		"can you cancel my order",
		"لا توقف الرسائل من فضلك",
		"", "  ", "hello",
	} {
		if IsStopWord(no) {
			t.Errorf("%q must not be read as a stop word", no)
		}
	}
}

func TestStopConfirmationIsWrittenInBothLanguages(t *testing.T) {
	en, ar := StopConfirmation("en"), StopConfirmation("ar")
	if en == "" || ar == "" || en == ar {
		t.Fatalf("a STOP must be confirmed in the contact's own language: en=%q ar=%q", en, ar)
	}
}

func TestOnlyDigitsMakesTwoSpellingsOfANumberComparable(t *testing.T) {
	// The number WhatsApp delivers is rarely the one somebody typed into the
	// CRM, so a STOP would never match its contact without this.
	want := "201234567890"
	for _, spelling := range []string{
		"201234567890", "+20 123 456 7890", "+20-123-456-7890", "(20) 1234567890",
	} {
		if got := OnlyDigits(spelling); got != want {
			t.Errorf("OnlyDigits(%q) = %q, want %q", spelling, got, want)
		}
	}
}

func TestNormChannelDefaultsToEmail(t *testing.T) {
	// An unspecified channel must mean the thing that was always true. If it
	// ever defaulted to whatsapp, an existing caller would start broadcasting
	// to phones without changing a line.
	for _, v := range []string{"", "email", "EMAIL", "nonsense", "sms"} {
		if got := normChannel(v); got != "email" {
			t.Errorf("normChannel(%q) = %q, want email", v, got)
		}
	}
	if normChannel("whatsapp") != "whatsapp" || normChannel(" WhatsApp ") != "whatsapp" {
		t.Fatal("an explicit whatsapp channel was not honoured")
	}
}

func TestStopHintIsOnEveryBroadcast(t *testing.T) {
	// The way out has to be IN the message; there is no header to put it in.
	en, ar := stopHint("en"), stopHint("ar")
	if !strings.Contains(strings.ToUpper(en), "STOP") {
		t.Errorf("the English hint does not say how to stop: %q", en)
	}
	if !strings.Contains(ar, "إيقاف") {
		t.Errorf("the Arabic hint does not say how to stop: %q", ar)
	}
}
