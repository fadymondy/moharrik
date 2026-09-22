package inbox

import (
	"strings"
	"testing"
)

func TestVisitorContext(t *testing.T) {
	const origin = "https://shop.test"
	cases := []struct {
		name                        string
		page, referer, locale, lang string
		wantPage, wantLocale        string
	}{
		{"page on the origin, fragment and credentials dropped", "https://u:p@shop.test/a?b=1#c", "", "", "", "https://shop.test/a?b=1", ""},
		{"page on another site falls back to the referer", "https://evil.test/x", "https://shop.test/", "", "", "https://shop.test/", ""},
		{"nothing on the origin: nothing kept", "https://evil.test/x", "https://evil.test/y", "", "", "", ""},
		{"javascript: is not a page", "javascript:alert(1)", "", "", "", "", ""},
		{"origin match ignores case", "HTTPS://SHOP.TEST/Path", "", "", "", "https://SHOP.TEST/Path", ""},
		{"widget locale wins over the browser", "", "", "ar", "en-US,en;q=0.9", "", "ar"},
		{"Accept-Language first choice", "", "", "", "AR-eg, ar;q=0.9", "", "ar-eg"},
		{"underscore tag normalised", "", "", "en_GB", "", "", "en-GB"},
		{"garbage locale falls back", "", "", "<script>", "fr-FR", "", "fr-FR"},
		{"wildcard is not a language", "", "", "", "*", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vc := visitorContextOf(origin, c.page, c.referer, "UA", c.locale, c.lang)
			if vc.PageURL != c.wantPage {
				t.Fatalf("page = %q, want %q", vc.PageURL, c.wantPage)
			}
			if vc.Locale != c.wantLocale {
				t.Fatalf("locale = %q, want %q", vc.Locale, c.wantLocale)
			}
		})
	}
}

func TestVisitorContextBounds(t *testing.T) {
	vc := visitorContextOf("https://shop.test", "https://shop.test/"+strings.Repeat("a", 3000), "",
		"Mozilla\x00/5.0\n"+strings.Repeat("x", 900), "", "")
	if len(vc.PageURL) > maxPageURL {
		t.Fatalf("page_url kept at %d bytes", len(vc.PageURL))
	}
	if n := len([]rune(vc.UserAgent)); n > maxUserAgent {
		t.Fatalf("user agent kept at %d runes", n)
	}
	if strings.ContainsAny(vc.UserAgent, "\x00\n") {
		t.Fatal("control characters survived in the user agent")
	}
}
