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
	if OriginAllowed("", "https://example.com") {
		t.Fatal("an empty allow-list accepted an origin; a new form would be open to the whole web")
	}
	// A request with no Origin header at all — curl, a script, a server — is
	// not a browser on an allowed page.
	if OriginAllowed("https://example.com", "") {
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
		if !OriginAllowed(list, ok) {
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
		if OriginAllowed(list, bad) {
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

func TestSubscribeSnippetPostsToItsOwnKeyAndKeepsWhatsAppSeparate(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://app.example.test/")
	s := subscribeSnippet("sf_k1")
	for _, want := range []string{
		`fetch("https://app.example.test/api/subscribe/sf_k1"`,
		`name="whatsapp_opt_in"`,
		// This API's errors carry "error"; the visitor should see the reason.
		`j.error ||`,
		// Consent to WhatsApp is sent only alongside a phone, and never pre-ticked.
		`whatsapp_opt_in: !!phone && d.get("whatsapp_opt_in") === "on"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("snippet missing %q", want)
		}
	}
	if strings.Contains(s, "checked") {
		t.Error("the WhatsApp box must not be pre-ticked")
	}
}

func TestInquirySnippetPostsToTheInquiryEndpointWithAttributionAndAHoneypot(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://app.example.test/")
	s := inquirySnippet("if_k1")
	for _, want := range []string{
		`fetch("https://app.example.test/api/inquiry/if_k1"`,
		// Every attribution field the endpoint takes is collected in the browser.
		`"utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "gclid"`,
		`referrer: document.referrer`,
		`landing_page: location.href`,
		// The honeypot is present, out of sight and out of the tab order.
		`name="nickname" tabindex="-1" autocomplete="off"`,
		`aria-hidden="true"`,
		// The two required fields are required in the markup too.
		`name="email" required`,
		`name="message" required`,
		`j.error ||`,
		// The optional budget (MH-464) is a field, and it is sent.
		`name="budget"`,
		`"company", "budget", "message"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("inquiry snippet missing %q", want)
		}
	}
	if strings.Contains(s, "/api/subscribe/") {
		t.Error("an inquiry snippet must never post to the subscribe endpoint")
	}
}

func TestWithEmbedPicksTheSnippetForTheFormKind(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://app.example.test")
	sub := withEmbed(FormOut{Kind: FormSubscribe, PublicKey: "sf_a"}, "")
	inq := withEmbed(FormOut{Kind: FormInquiry, PublicKey: "if_b"}, "")
	if sub.Endpoint != "https://app.example.test/api/subscribe/sf_a" || !strings.Contains(sub.Embed, "whatsapp_opt_in") {
		t.Errorf("subscribe form got the wrong embed: %q", sub.Endpoint)
	}
	if inq.Endpoint != "https://app.example.test/api/inquiry/if_b" || !strings.Contains(inq.Embed, `name="message"`) {
		t.Errorf("inquiry form got the wrong embed: %q", inq.Endpoint)
	}
}

// MH-449: with the workspace's measurement id, both snippets start with the GA4 tag under
// Consent Mode v2 (everything denied until the site's banner grants it); without one, neither
// carries a tag. The inquiry snippet always sends the GA client id and the ads consent it can
// read, so a site's own GA tag is enough for the lead to join the visitor's journey.
func TestEmbedsCarryTheGA4TagOnlyWhenAsked(t *testing.T) {
	t.Setenv("PUBLIC_URL", "https://app.example.test")
	for _, kind := range []string{FormSubscribe, FormInquiry} {
		plain := withEmbed(FormOut{Kind: kind, PublicKey: "k"}, "")
		if strings.Contains(plain.Embed, "googletagmanager") || strings.Contains(plain.Embed, "gtag(") {
			t.Errorf("%s: a snippet without a measurement id carries a tag", kind)
		}
		tagged := withEmbed(FormOut{Kind: kind, PublicKey: "k"}, "G-TEST1234")
		for _, want := range []string{
			`https://www.googletagmanager.com/gtag/js?id=G-TEST1234`,
			`gtag("config", "G-TEST1234")`,
			`ad_storage: "denied", ad_user_data: "denied", ad_personalization: "denied"`,
			`analytics_storage: "denied"`,
			// A page that already runs gtag keeps its own consent defaults.
			`if (typeof window.gtag !== "function")`,
		} {
			if !strings.Contains(tagged.Embed, want) {
				t.Errorf("%s: tagged snippet missing %q", kind, want)
			}
		}
		if !strings.HasPrefix(tagged.Embed, "<!-- GA4 (G-TEST1234)") {
			t.Errorf("%s: the tag must come first, before the form", kind)
		}
	}
	if s := withEmbed(FormOut{Kind: FormSubscribe, PublicKey: "k"}, "not-an-id").Embed; strings.Contains(s, "gtag") {
		t.Error("an invalid measurement id must not be written into a snippet")
	}
	inq := inquirySnippet("if_k1")
	for _, want := range []string{`_ga=`, `body.ga_client_id`, `e[0] === "consent"`, `body["ga_" + k]`} {
		if !strings.Contains(inq, want) {
			t.Errorf("inquiry snippet missing %q", want)
		}
	}
}
